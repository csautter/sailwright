# Terraform checks and local deployment

Infrastructure changes are applied locally by a maintainer; CI only validates.

## Checks

Pull requests and pushes to `main` that change this directory run Terraform and
Terragrunt format checks, module validation, TFLint, Trivy and the local plan
safeguard tests. Validation uses temporary module copies and each unit's committed
provider lock file with `terraform init -backend=false -lockfile=readonly`.
It needs no local environment settings, Azure login or remote state access.
Formatting checks do not rewrite files; the existing local Terragrunt hooks still
format files as before. Trivy findings fail the check at every severity; accepted
exceptions have resource-local comments explaining why they are needed.

The shared tool version source is `.devcontainer/Dockerfile`. Terragrunt, TFLint
and Trivy archives also have SHA-256 pins there, maintained by Renovate for Linux
amd64 and arm64. Terraform is installed at the exact pinned version from the
signed HashiCorp apt repository in the devcontainer; the CI installer verifies
the release archive against HashiCorp's HTTPS SHA256SUMS file.

To run the same checks locally with those tools on `PATH`:

```bash
make test-terraform
make check-terraform
```

On Linux, `bash deployments/terraform/scripts/install-check-tools.sh <directory>`
installs the pinned tools into a chosen directory. Add it to `PATH` before running
the checks. The installer needs curl, unzip and tar.

## Plan, review, apply

Use Python 3 and Terragrunt on Linux, macOS or in the devcontainer. Configure your
ignored `env_local.hcl` and authenticate locally with `az login` for the intended
tenant and subscription. Commit the infrastructure changes before planning.
From the repository root:

```bash
make deploy-plan-terraform-azure-gh-runner
make deploy-summary-terraform-azure-gh-runner
# Review the full plan locally, then apply that saved plan:
make deploy-apply-terraform-azure-gh-runner
```

Before merging an infrastructure PR, apply its reviewed plan locally or include
its plan summary in the PR. The summary prints add/change/destroy counts and
resource addresses with actions. Replacements count as both add and destroy.
String instance keys are masked as `[*]`, because they can contain private values.
Copy only this summary into a public PR; keep the full plan, Terraform JSON,
state and local configuration private.

Apply requires a saved `tf.plan` no older than 15 minutes, with matching plan
hash, Git commit and configuration fingerprint (including local HCL/variable
files and uncommitted changes). A changed commit or input requires a new plan.
If formatting hooks change inputs during planning, review those changes and
rerun the plan. Failed replanning invalidates the previous plan. Concurrent plan
commands for the same unit are refused.

The helper inspects and applies the same private copy of the saved plan. It
refuses every managed-resource deletion, replacement or state removal by default.
This includes Entra apps, federated credentials, service principals, Azure role
assignments, custom roles and resources whose `prevent_destroy` block was removed.
After reviewing an intentional destructive change locally, explicitly opt in:

```bash
ALLOW_DESTRUCTIVE=1 make deploy-apply-terraform-azure-gh-runner
```

This override does not bypass Terraform's own `prevent_destroy` checks or permit
an expired plan. The saved plan and provenance file are ignored by Git, created
with owner-only permissions and deleted after any apply attempt, including a
failed apply. Generate a new plan before retrying after a failure. Do not bypass
the helper with a direct apply command.

## Maintainer permissions

Prefer delegated permissions limited to this deployment. The human running
Terraform needs resource management, custom-role definition and role-assignment
permissions at the required scopes, plus permissions to manage the Entra app
registrations, service principals and federated credentials in this module.
Subscription Owner (or Contributor plus User Access Administrator) and Entra Cloud
Application Administrator are broad built-in options; use narrower delegation
where possible. Azure RBAC and Entra directory roles are separate.
See Microsoft's [custom role permissions](https://learn.microsoft.com/en-us/azure/role-based-access-control/custom-roles)
and [application management delegation](https://learn.microsoft.com/en-us/entra/identity/role-based-access-control/delegate-app-roles).
Backend access and reading the existing Key Vault secrets also require the
corresponding data-plane permissions.

If Microsoft Entra ID P2/PIM is available, prefer eligible privileged roles and
[activate them temporarily](https://learn.microsoft.com/en-us/entra/id-governance/privileged-identity-management/pim-how-to-activate-role)
for the local deployment instead of keeping them permanently active.
