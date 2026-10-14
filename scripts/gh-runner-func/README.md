# GitHub Actions Runner Broker — Azure Function

This Azure Function App acts as the **control-plane broker** for on-demand Windows GitHub Actions runner VMs on Azure.
It exposes two HTTP endpoints that CI workflows call to create and destroy ephemeral runner VMs.

---

## Table of contents

1. [Architecture overview](#architecture-overview)
2. [API endpoints](#api-endpoints)
   - [POST /api/request_runner](#post-apirequest_runner)
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
| `resource-group`       | string | ❌        | `RESOURCE_GROUP` env var            | Target resource group name. Must start with `gh-runner-tmp`. |
| `runner-name`          | string | ❌        | `"gh-runner-vm"`                    | VM / runner display name. Truncated to 15 chars for the Windows computer name. |
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
| `202`  | VM creation started. Body: `{"message": "Runner VM creation started", "vm": "<vm-name>"}` |
| `400`  | Invalid request body, repo format, or virtualization flavor. |
| `401`  | Authentication/authorization check failed. |
| `500`  | Internal error (GitHub API failure, Key Vault access, VM provisioning, missing config). |

**What the endpoint does**

1. Validates request and caller identity.
2. Retrieves `github-runner-pat` from Key Vault.
3. Calls `POST https://api.github.com/repos/{repo}/actions/runners/registration-token` to obtain a short-lived runner token.
4. Loads and parameterises `runner-setup.ps1` with the token, repo URL, and virtualization flavor; base64-encodes it as VM `custom_data`.
5. Creates (or reuses) the resource group in `LOCATION`.
6. Provisions a VNet (`10.0.0.0/16`), subnet, public IP, NSG (no inbound rules), and NIC.
7. Creates the VM using the image referenced by `CUSTOM_IMAGE_ID` + `-` + `virtualization-flavor`.
8. If `VM_USE_SPOT=true`, first attempts a spot VM; falls back to a regular VM on failure.
9. The VM boots, runs `runner-setup.ps1` via `custom_data`, installs Hyper-V or VirtualBox, and registers itself as a GitHub Actions runner.

> **Security note:** The NSG adds no inbound rules — the runner only needs outbound connectivity to GitHub, and the default rules deny all inbound. RDP (3389) is intentionally **not** opened. For the rare debug session, use Azure Bastion / just-in-time VM access rather than a standing inbound rule.

---

### POST /api/delete_resource_group

Deletes a resource group and all resources within it. Intended to be called by the workflow after the runner job completes.

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
| `200`  | Deletion complete. Body: `{"message": "Resource group '<name>' deleted."}` |
| `401`  | Authentication/authorization check failed. |
| `500`  | Internal error during deletion. |

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

### Required — function cannot start without these

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
| `VM_NAME`        | Default VM name, e.g. `gh-runner-vm`. |

### Optional — have safe defaults

| Setting          | Default            | Description |
|------------------|--------------------|-------------|
| `VM_USE_SPOT`    | `false`            | Set to `true` to use Azure Spot VMs with automatic fallback to on-demand. |
| `VNET_NAME`      | `gh-runner-vnet`   | Virtual network name within the runner resource group. |
| `SUBNET_NAME`    | `default`          | Subnet name. |
| `IP_NAME`        | `gh-runner-ip`     | Public IP resource name. |
| `NIC_NAME`       | `gh-runner-nic`    | Network interface resource name. |
| `NSG_NAME`       | `gh-runner-nsg`    | Network security group name. |
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

Use the provided `test-endpoints.sh` script. It automatically resolves the function app name and API client ID from Terragrunt output or the Azure CLI.

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
