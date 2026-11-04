# GitHub Actions Runner Broker — Azure Function

This Azure Function App acts as the **control-plane broker** for on-demand Windows GitHub Actions runner VMs on Azure.
It exposes HTTP endpoints to create ephemeral runner VMs, observe provisioning, and delete their resource groups.

---

## Table of contents

1. [Architecture overview](#architecture-overview)
2. [API endpoints](#api-endpoints)
   - [POST /api/request_runner](#post-apirequest_runner)
   - [POST /api/runner_status](#post-apirunner_status)
   - [POST /api/delete_resource_group](#post-apidelete_resource_group)
3. [Authentication and security model](#authentication-and-security-model)
4. [Required app settings](#required-app-settings)
5. [Key Vault secrets contract](#key-vault-secrets-contract)
6. [Infrastructure deployment](#infrastructure-deployment)
7. [IAM roles granted](#iam-roles-granted)
8. [Local development](#local-development)
9. [Testing endpoints](#testing-endpoints)

---

## Architecture overview

```
GitHub Actions workflow
        │
        │  OIDC token (federated identity)
        ▼
Azure AD – gh-actions-runner-broker app registration
        │
        │  Bearer token  (audience: api://<client-id>)
        ▼
Azure Function App  (EasyAuth v2 → Return401 for unauthenticated)
        │  require_authenticated_caller() validates:
        │    • tid  == ALLOWED_TENANT_ID
        │    • aud  == ALLOWED_AUDIENCE
        │    • azp/oid in ALLOWED_CLIENT_IDS or ALLOWED_USER_OBJECT_IDS
        │
        ├─▶ request_runner       → Key Vault ──▶ GitHub API ──▶ Azure Compute API
        │                                         (register token)   (create VM)
        │
        ├─▶ runner_status         → Azure Resource Manager / Compute API
        │
        └─▶ delete_resource_group → Azure Resource Manager API
```

The Function App uses a **system-assigned managed identity** to access Key Vault and the Azure subscription.

---

## API endpoints

Base URL: `https://<function-app-name>.azurewebsites.net/api`

All endpoints require a valid Bearer token issued by Azure AD for the `api://<client-id>` audience (see [Authentication](#authentication-and-security-model)).

### POST /api/request_runner

Registers a GitHub Actions runner token, creates a resource group, and provisions a Windows VM pre-configured as a self-hosted runner.

**Request body (JSON)**

| Field                  | Type   | Required | Default                             | Description |
|------------------------|--------|----------|-------------------------------------|-------------|
| `repo`                 | string | ✅        | —                                   | GitHub repository in `owner/repo` format |
| `resource-group`       | string | ❌        | `RESOURCE_GROUP` env var            | Target resource group name. Must start with `gh-runner-tmp`, use letters, digits, `_-.()` and not end in a period (up to 90 characters). Prefer a separate group per request for independent cleanup. |
| `runner-name`          | string | ❌        | `"gh-runner-vm"`                    | Runner display name (letters, digits, hyphens; starts with a letter or digit, up to 64 characters). Truncated to 15 chars for the Windows computer name, which cannot be all numeric. Use a distinct name for each concurrent runner. |
| `virtualization-flavor`| string | ❌        | `"hyperv"`                          | Virtualization technology. Allowed values: `hyperv`, `virtualbox`. |

**Example**

```json
{
  "repo": "owner/repo",
  "resource-group": "gh-runner-tmp-ci-run-12345",
  "runner-name": "ci-run-12345",
  "virtualization-flavor": "hyperv"
}
```

**Responses**

| Status | Meaning |
|--------|---------|
| `202` | VM creation accepted. The existing `message` and `vm` fields remain; `resource-group`, `resources`, `status: "provisioning"` and `status-url: "/api/runner_status"` identify the resources and how to monitor them. |
| `400` | Invalid input, or GitHub returned `404` for the repository. |
| `401` | Authentication/authorization check failed. |
| `409` | Azure resource conflict; inspect the returned group and VM before retrying. |
| `502` | Key Vault/Azure request rejected, GitHub authentication/authorization failed (`401`/`403`), invalid GitHub token response, or GitHub request failed after retries. |
| `503` | Missing app settings (names in `missing`), Azure unavailable, or GitHub rate limited (`429`). Transient failures include a `Retry-After` header. |
| `504` | GitHub request or network provisioning timed out. |
| `500` | Unexpected internal preparation error. |

**What the endpoint does**

1. Validates request and caller identity.
2. Retrieves `github-runner-pat` from Key Vault.
3. Calls `POST https://api.github.com/repos/{repo}/actions/runners/registration-token` to obtain a short-lived runner token.
4. Loads and parameterises `runner-setup.ps1` with the token, repo URL, and virtualization flavor; base64-encodes it as VM `custom_data`.
5. Creates (or reuses) the resource group in `LOCATION`.
6. Provisions a VNet (`10.0.0.0/16`), subnet, public IP, NSG (no inbound rules), and NIC.
7. Creates the VM using the image referenced by `CUSTOM_IMAGE_ID` + `-` + `virtualization-flavor`.
8. If `VM_USE_SPOT=true`, first attempts a spot VM; an ARM error on the initial submission triggers an on-demand fallback. Failures after acceptance are reported by `runner_status`.
9. The VM boots, runs `runner-setup.ps1` via `custom_data`, installs Hyper-V or VirtualBox, and registers itself as a GitHub Actions runner.

**Diagnostics and retries**

Send an optional `X-Correlation-ID` (1–64 letters, digits, `.`, `_`, or `-`),
or let the broker generate one. Every creation/status response includes it in
the header and `correlation-id` field, alongside the current `stage`. JSON logs
identify auth, input, PAT, GitHub token, network and VM stages. Failures log the
exception class and ARM error code when available; upstream bodies and exception
messages are omitted to keep credentials out of logs.

The broker checks required settings on first use and caches the result per
worker. Restart the Function App after correcting settings. Resource settings
`VM_NAME`, `VNET_NAME`, `IP_NAME`, `NIC_NAME` and `NSG_NAME` are prefixes: each
request gets a UUID suffix within Azure name limits. The `resources` response
field and input-stage log contain the actual generated names.

Key Vault transport failures and transient HTTP failures, plus GitHub network
errors and `5xx` responses, get at most three attempts with 0.5 s and 1 s backoff.
GitHub `429` is returned immediately with `Retry-After` (60 s if unavailable).
SDK and GitHub connections use a 3 s connect timeout and a 5 s read timeout.
Network pollers wait at most 20 s each, shortened by a shared 180 s request
budget with 20 s reserved for VM submission. VM provisioning is asynchronous.

A retry of `request_runner` creates new resources; it is **not idempotent**.
After an uncertain response or network timeout, inspect the returned group/VM
and clean up before submitting again. Accepted Azure operations may still finish
following an HTTP timeout. Use a dedicated group per request when cleanup must
not affect other runners.

> **Security note:** The NSG adds no inbound rules — the runner only needs outbound connectivity to GitHub, and the default rules deny all inbound. RDP (3389) is intentionally **not** opened. For the rare debug session, use Azure Bastion / just-in-time VM access rather than a standing inbound rule.

---

### POST /api/runner_status

Poll this endpoint after a `202` creation response to detect provisioning
failures without holding the creation HTTP request open. It reads ARM directly,
so it works across Function worker restarts and requires no in-memory poller.
It uses the same caller authentication as `request_runner`, and checks the
resource group's `managed-by: gh-runner-broker` tag before reading the VM.

Send the `resource-group` and actual `vm` from the creation response:

```json
{
  "resource-group": "gh-runner-tmp-ci-run-12345",
  "vm": "gh-runner-vm-<generated-suffix>"
}
```

| HTTP status | Body `status` | Action |
|-------------|---------------|--------|
| `202` | `provisioning` | Poll again after `Retry-After` (10 seconds), with an overall caller deadline. |
| `200` | `succeeded` | ARM provisioning succeeded. Continue checking GitHub runner registration separately. |
| `200` | `failed` | Stop waiting and clean up. `error-code` contains a provisioning/power-state code. Failed/canceled provisioning and stopped/deallocated VMs are reported here, including Spot eviction. |
| `404` | `not-found` | The group or VM no longer exists. Stop waiting and inspect cleanup. |
| `400` / `401` / `403` | — | Invalid input, unauthenticated caller, or group not owned by the broker. |
| `502` / `503` / `504` | — | Status lookup failed; use `stage` and correlation ID for diagnosis. |

Callers must inspect the JSON `status`, including on HTTP `200`, to fail CI early.
Existing creation-only callers must add this polling step to observe late
failures. This endpoint reports Azure VM state; it does not check the bootstrap
script or GitHub registration and does not automatically retry late Spot
failures on demand. Continue polling while waiting for registration if Spot
eviction needs to be detected after provisioning succeeds.

---

### POST /api/delete_resource_group

Starts deleting a resource group and all resources within it. Intended to be called by the workflow after the runner job completes.

The endpoint does not wait for the deletion to finish: deleting a runner VM takes longer than the ~230 s request limit of the Function App front end, which would turn a successful deletion into a `504 Gateway Time-out`. Callers poll the endpoint until it reports `"status": "deleted"`; repeated calls are idempotent. [`delete-resource-group.sh`](delete-resource-group.sh) implements this loop and is what the workflows use.

**Request body (JSON)**

| Field            | Type   | Required | Default                  | Description |
|------------------|--------|----------|--------------------------|-------------|
| `resource-group` | string | ❌        | `RESOURCE_GROUP` env var | Resource group to delete. Must start with `gh-runner-tmp` **and** carry the broker's `managed-by=gh-runner-broker` tag (applied at creation); otherwise deletion is refused. |

**Example**

```json
{
  "resource-group": "gh-runner-tmp-ci-run-12345"
}
```

**Responses**

| Status | Meaning |
|--------|---------|
| `202`  | Deletion started or still in progress. Body: `{"message": "...", "status": "deleting", "resource-group": "<name>"}` — call again later. |
| `200`  | The resource group no longer exists. Body: `{"message": "Resource group '<name>' deleted.", "status": "deleted", "resource-group": "<name>"}` |
| `401`  | Authentication/authorization check failed. |
| `500`  | Internal error, invalid name, or the resource group is not owned by the broker. |

> **Safety guard:** The function raises an exception if the resource group name does not start with `gh-runner-tmp`, preventing accidental deletion of unrelated groups.

---

## Authentication and security model

### EasyAuth v2 (platform layer)

The Function App is configured with Azure App Service Authentication v2:

- `auth_enabled = true`
- `unauthenticated_action = "Return401"` — requests without a valid Bearer token are rejected at the platform before reaching function code.
- Identity provider: Azure Active Directory, audience `api://<client-id>`.

### Application-layer claim validation (`require_authenticated_caller`)

Even after EasyAuth validates the signature, every endpoint re-validates the decoded `X-MS-CLIENT-PRINCIPAL` header:

| Claim checked | Source env var            | Requirement |
|---------------|---------------------------|-------------|
| `tid`         | `ALLOWED_TENANT_ID`       | Must match exactly |
| `aud`         | `ALLOWED_AUDIENCE`        | Must match exactly (e.g. `api://<client-id>`) |
| `azp`/`appid` | `ALLOWED_CLIENT_IDS`      | Comma-separated list; caller's client ID must be present **or** |
| `oid`         | `ALLOWED_USER_OBJECT_IDS` | Comma-separated list; caller's object ID must be present |

Both `ALLOWED_CLIENT_IDS` and `ALLOWED_USER_OBJECT_IDS` are evaluated; the caller is accepted if it matches either list.

### GitHub Actions OIDC (CI callers)

GitHub Actions workflows authenticate using **Workload Identity Federation** (no long-lived secrets):

| Federated credential | Subject |
|----------------------|---------|
| `github-actions`     | `repo:csautter/sailwright:ref:refs/heads/main` |
| `github-actions-pr`  | `repo:csautter/sailwright:pull_request` |

Terraform builds these subjects from the `github_repository` module variable.

The workflow must request the OIDC token with `permissions: id-token: write` and exchange it for an Azure AD token scoped to `api://<client-id>`.

The golden image build ([build-azure-runner-image.yml](../../.github/workflows/build-azure-runner-image.yml)) uses a separate app, `gh-actions-image-builder`, whose only federated credential (`github-actions-main`) trusts `repo:csautter/sailwright:ref:refs/heads/main`. Pull request workflows therefore cannot replace the image. Its client ID (Terraform output `image_builder_client_id`) is stored as the repository secret `AZURE_IMAGE_BUILDER_CLIENT_ID`.

---

## Required app settings

These must be present in the Function App's **Application Settings** (set by Terraform; see `deployments/terraform/modules/azure_gh_runner/manager.tf`).

### Required — runtime and broker settings

| Setting                  | Description |
|--------------------------|-------------|
| `FUNCTIONS_WORKER_RUNTIME` | Must be `python`. |
| `VAULT_URL`              | Key Vault URI, e.g. `https://gh-runner-kv-b-<suffix>.vault.azure.net/`. |
| `SUBSCRIPTION_ID`        | Azure subscription ID where runner VMs are created. |
| `LOCATION`               | Azure region for ephemeral runner VMs, e.g. `eastus2`. |
| `RESOURCE_GROUP`         | Default resource group for runner VMs. Must start with `gh-runner-tmp`. |
| `ALLOWED_TENANT_ID`      | Entra tenant ID. Requests from other tenants are rejected. |
| `ALLOWED_AUDIENCE`       | Expected token audience, e.g. `api://<app-client-id>`. |
| `ALLOWED_CLIENT_IDS`     | Comma-separated list of allowed application (client) IDs. |

### Required — VM provisioning will fail without these

| Setting          | Description |
|------------------|-------------|
| `CUSTOM_IMAGE_ID`| ARM resource ID prefix for the custom Windows image, e.g. `/subscriptions/<sub>/resourceGroups/gh-actions-images-<region>/providers/Microsoft.Compute/images/Win2022GHAzureRunner`. The function appends `-<virtualization-flavor>` at runtime. |
| `VM_SIZE`        | Azure VM SKU, e.g. `Standard_D4ds_v5`. |
| `ADMIN_USERNAME` | VM local administrator username, e.g. `azureuser`. |
| `VM_NAME`        | VM resource name prefix, e.g. `gh-runner-vm`; a unique suffix is appended per request. |

### Optional — have safe defaults

| Setting          | Default            | Description |
|------------------|--------------------|-------------|
| `VM_USE_SPOT`    | `false`            | Set to `true` to use Azure Spot VMs with on-demand fallback for initial submission errors. Poll `runner_status` for late failures. |
| `VNET_NAME`      | `gh-runner-vnet`   | Virtual network name prefix within the runner resource group. |
| `SUBNET_NAME`    | `default`          | Subnet name. |
| `IP_NAME`        | `gh-runner-ip`     | Public IP resource name prefix. |
| `NIC_NAME`       | `gh-runner-nic`    | Network interface resource name prefix. |
| `NSG_NAME`       | `gh-runner-nsg`    | Network security group name prefix. |
| `ALLOWED_USER_OBJECT_IDS` | deploying user's OID | Comma-separated Entra object IDs of users allowed to call endpoints directly. |

---

## Key Vault secrets contract

The Function App's managed identity must have the **Key Vault Secrets User** role on the Key Vault. The following secrets must exist before the function app can serve requests:

| Secret name                  | Description |
|------------------------------|-------------|
| `github-runner-pat`          | GitHub Personal Access Token with `repo` scope, used to call the runner registration-token API. |
| `github-runner-vm-admin-pw`  | Password for the local administrator account on provisioned VMs. Must meet Azure VM password complexity requirements. |

Secrets are stored in the Key Vault deployed by Terraform: `gh-runner-kv-b-<suffix>` in resource group `gh-runner-manager`.

---

## Infrastructure deployment

The full infrastructure is managed by Terraform in `deployments/terraform/modules/azure_gh_runner/`.

### Resources created

| Resource | Name pattern | Notes |
|----------|-------------|-------|
| Resource group (manager) | `gh-runner-manager` | Holds all persistent resources. `prevent_destroy = true` |
| Key Vault | `gh-runner-kv-b-<suffix>` | RBAC-enabled; soft-delete 7 days. `prevent_destroy = true` |
| Storage Account | `ghrunnerstorage<suffix>` | Function App backing storage. `prevent_destroy = true` |
| Function App | `gh-runner-func-app-<suffix>` | Linux, Python 3.11, Consumption (Y1) plan. |
| App Insights | `gh-runner-func-app-<suffix>` | Linked to Function App. |
| Entra app registration | `gh-actions-runner-broker` | API audience; pre-authorises Azure CLI client. |
| Entra app registration | `gh-actions-image-builder` | Golden image build in CI; federated credential for `main` only. |
| Resource group (images) | `gh-actions-images-<runner_location>` | Managed images referenced by `CUSTOM_IMAGE_ID`. |
| Resource group (image build) | `gh-actions-image-build-<runner_location>` | Packer's temporary build VM, network and Key Vault. |
| Federated credentials | `github-actions`, `github-actions-pr` | Workload identity for CI. |

The random suffix is stable after initial deployment (`ignore_changes = all`). New environments get a random value in the range `10000–99999`.

### Deploy

```bash
# From repo root
make deploy-apply-terraform-azure-gh-runner
```

Or manually with Terragrunt from `deployments/terraform/env/azure_dev/azure_gh_runner/`.

### Required Terraform variables

| Variable | Description |
|----------|-------------|
| `env` | Environment name, e.g. `dev`. |
| `runner_location` | Azure region for runner VMs, e.g. `eastus2`. |
| `allowed_user_object_ids` | (Optional) Entra object IDs of human operators allowed to call endpoints. |

---

## IAM roles granted

| Principal | Scope | Role | Purpose |
|-----------|-------|------|---------|
| Function App managed identity | Key Vault | Key Vault Secrets User | Read `github-runner-pat` and `github-runner-vm-admin-pw` |
| Function App managed identity | Subscription | `gh-runner-broker` (custom) | Create/delete runner resource groups and their VM/network resources — no data-plane or IAM access |
| `gh-actions-runner-broker` service principal | Function App | Contributor | Allow Entra app to manage its own Function App |
| Deploying identity | Key Vault | Key Vault Secrets Officer | Seed secrets via Terraform/CLI |
| `gh-actions-image-builder` service principal | Image and image-build resource groups | Contributor | Build and replace the golden image with Packer |

> The Function App identity uses a least-privilege custom role (`gh-runner-broker`, defined in [`manager.tf`](../../deployments/terraform/modules/azure_gh_runner/manager.tf)) scoped to resource-group create/delete plus `Microsoft.Compute/*` and `Microsoft.Network/*`. It replaces the previous subscription-wide `Contributor` grant, so a compromised broker cannot read secrets or assign roles outside the runner domain.

---

## Local development

### Prerequisites

- Python 3.11
- [Azure Functions Core Tools v4](https://learn.microsoft.com/azure/azure-functions/functions-run-local)
- Azure CLI (`az`)
- Azure Cosmos DB Emulator or real Azure resources

### Setup

```powershell
# Create and activate a virtual environment
python -m venv .venv
.\.venv\Scripts\Activate.ps1

# Install dependencies
pip install -r requirements.txt
```

### Dependencies

`requirements.in` lists the direct dependencies, pinned to exact versions.
`requirements.txt` is the hash-pinned lock generated from it, and it's what the
Function App deployment installs. Dependabot proposes upgrades to both. To
change a dependency by hand, edit `requirements.in` and regenerate the lock with
Python 3.11, the Function App runtime:

```bash
pip install pip-tools
pip-compile --generate-hashes --strip-extras requirements.in
```

Build ARM request bodies from SDK model classes (`azure.mgmt.*.models`), never
plain dicts: current Azure SDKs send dict bodies to ARM unchanged, without mapping
snake_case keys to the camelCase `properties` payload.

### Configure local settings
Copy `local.settings.json.example` to `local.settings.json` and fill in the required values:

```json
{
    "IsEncrypted": false,
    "Values": {
        "FUNCTIONS_WORKER_RUNTIME": "python",
        "AzureWebJobsStorage": "UseDevelopmentStorage=true",
        "VAULT_URL": "https://<your-kv>.vault.azure.net/",
        "SUBSCRIPTION_ID": "<your-subscription-id>",
        "LOCATION": "eastus2",
        "RESOURCE_GROUP": "gh-runner-tmp-local",
        "VM_SIZE": "Standard_D4ds_v5",
        "VM_NAME": "gh-runner-vm",
        "ADMIN_USERNAME": "azureuser",
        "CUSTOM_IMAGE_ID": "/subscriptions/<sub>/resourceGroups/gh-actions-images-eastus2/providers/Microsoft.Compute/images/Win2022GHAzureRunner",
        "ALLOWED_TENANT_ID": "<tenant-id>",
        "ALLOWED_AUDIENCE": "api://<app-client-id>",
        "ALLOWED_CLIENT_IDS": "<app-client-id>",
        "ALLOWED_USER_OBJECT_IDS": "<your-object-id>"
    }
}
```

> **Note:** In production, all these values are managed exclusively by Terraform (see `deployments/terraform/modules/azure_gh_runner/manager.tf`) and injected as Function App Application Settings. `local.settings.json` is **only** used for local development and is not the intended place to manage these values permanently.

> `local.settings.json` is git-ignored. Never commit secrets.

### Run locally

```powershell
# With the VS Code task:
# Terminal → Run Task → "func: host start"

# Or directly:
func host start
```

---

## Testing endpoints

Unit tests for the handlers stub the Azure SDKs and need only Python 3. They cover dependency failures, retries, invalid input, missing configuration, concurrent resource naming and late provisioning failures:

```bash
python3 -m unittest discover -s scripts/gh-runner-func
```

Contract tests check the JSON request bodies the real, pinned Azure SDKs send to
ARM, exercise accepted-then-failed provisioning through a fake SDK transport,
and verify transport timeouts, so SDK changes show up before deployment. Run them in a separate virtual environment with the lock installed.
The stub-based tests above must not run in the same process:

```bash
python3.11 -m venv /tmp/gh-runner-func-venv
/tmp/gh-runner-func-venv/bin/pip install --require-hashes -r scripts/gh-runner-func/requirements.txt
/tmp/gh-runner-func-venv/bin/python -I -m unittest discover -s scripts/gh-runner-func -p 'contract_test*.py'
```

To call the deployed endpoints, use the provided `test-endpoints.sh` script. It automatically resolves the function app name and API client ID from Terragrunt output or the Azure CLI.

```bash
# Request a runner (auto-generates resource group and runner name)
bash scripts/gh-runner-func/test-endpoints.sh --request-runner

# Request a runner with explicit parameters
bash scripts/gh-runner-func/test-endpoints.sh \
  --request-runner \
  --repo owner/repo \
  --resource-group gh-runner-tmp-local-$(date +%s) \
  --runner-name local-test \
  --flavor hyperv

# Delete a resource group
bash scripts/gh-runner-func/test-endpoints.sh \
  --resource-group gh-runner-tmp-local-12345 \
  --delete-resource-group

# Override function app and client ID explicitly
bash scripts/gh-runner-func/test-endpoints.sh \
  --function-app gh-runner-func-app-18632 \
  --api-client-id <client-id> \
  --request-runner
```

Or using the Makefile targets:

```bash
make test-gh-runner-func-delete \
  FUNCTION_APP_NAME=gh-runner-func-app-18632 \
  RESOURCE_GROUP=gh-runner-tmp-local-1771769397
```

The script uses `az rest` with `--resource api://<client-id>`, so you must be logged in with an identity that is in `ALLOWED_CLIENT_IDS` or `ALLOWED_USER_OBJECT_IDS`.
