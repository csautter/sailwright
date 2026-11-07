resource "random_uuid" "gh_actions_runner_broker_scope_id" {}

resource "azuread_application" "gh_actions_runner_broker" {
  display_name = "gh-actions-runner-broker"
  lifecycle {
    ignore_changes = [identifier_uris]
  }

  api {
    oauth2_permission_scope {
      admin_consent_description  = "Allow the application to call the GitHub Actions runner broker API"
      admin_consent_display_name = "Access gh-actions-runner-broker"
      enabled                    = true
      id                         = random_uuid.gh_actions_runner_broker_scope_id.result
      type                       = "User"
      user_consent_description   = "Allow the application to call the GitHub Actions runner broker API on your behalf"
      user_consent_display_name  = "Access gh-actions-runner-broker"
      value                      = "user_impersonation"
    }
  }
}

# Pre-authorise the Azure CLI public client so `az rest --resource api://<id>` works
# without any user/admin consent prompt.
resource "azuread_application_pre_authorized" "azure_cli" {
  application_id       = azuread_application.gh_actions_runner_broker.id
  authorized_client_id = "04b07795-8ddb-461a-bbee-02f9e1bf7b46" # Well-known Azure CLI app ID
  permission_ids       = [random_uuid.gh_actions_runner_broker_scope_id.result]
}

resource "azuread_application_identifier_uri" "gh_actions_runner_broker" {
  application_id = azuread_application.gh_actions_runner_broker.id
  identifier_uri = "api://${azuread_application.gh_actions_runner_broker.client_id}"
}

resource "azuread_service_principal" "gh_actions_runner_broker" {
  client_id = azuread_application.gh_actions_runner_broker.client_id
}

# The broker identity deliberately has no Azure RBAC role on the Function App
# (or anywhere else in the subscription). Workflows only need a token for the
# api://<client-id> audience to call the broker over HTTPS; `azure/login` uses
# `allow-no-subscriptions: true`. A Contributor assignment here would let any
# pull request workflow redeploy the Function App and act as its managed
# identity, which can manage VMs and networks across the subscription and read
# the GitHub PAT from Key Vault.

resource "azuread_application_federated_identity_credential" "gh_actions_github_actions" {
  application_id = azuread_application.gh_actions_runner_broker.id
  display_name   = "github-actions"
  issuer         = "https://token.actions.githubusercontent.com"
  subject        = "repo:${var.github_repository}:ref:refs/heads/main"
  audiences      = ["api://AzureADTokenExchange"]
}

# Allow GitHub Actions pull request workflows to access as well
resource "azuread_application_federated_identity_credential" "gh_actions_github_actions_pr" {
  application_id = azuread_application.gh_actions_runner_broker.id
  display_name   = "github-actions-pr"
  issuer         = "https://token.actions.githubusercontent.com"
  subject        = "repo:${var.github_repository}:pull_request"
  audiences      = ["api://AzureADTokenExchange"]
}
