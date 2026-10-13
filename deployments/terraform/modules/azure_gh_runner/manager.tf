resource "random_integer" "suffix" {
  min = 10000
  max = 99999

  # ignore_changes prevents Terraform from replacing this resource when min/max
  # change, which would regenerate the suffix and force recreation of all named
  # resources (storage account, key vault, function app). The value already in
  # state stays stable.
  # New environments will receive a genuinely random suffix from the full range.
  lifecycle {
    ignore_changes = all
  }
}

resource "azurerm_resource_group" "gh_runner_manager" {
  name     = "gh-runner-manager"
  location = "francecentral"
}

resource "azurerm_key_vault" "gh_runner_kv" {
  name                       = "gh-runner-kv-b-${random_integer.suffix.result}"
  location                   = azurerm_resource_group.gh_runner_manager.location
  resource_group_name        = azurerm_resource_group.gh_runner_manager.name
  tenant_id                  = data.azurerm_client_config.current.tenant_id
  sku_name                   = "standard"
  rbac_authorization_enabled = true
  # Purge protection prevents a deleted secret/vault from being permanently purged
  # before the soft-delete retention window elapses. This is irreversible once
  # enabled, which is acceptable for this long-lived control-plane vault (it already
  # carries prevent_destroy below).
  purge_protection_enabled   = true
  soft_delete_retention_days = 7

  lifecycle {
    prevent_destroy = true
  }
}

data "azurerm_client_config" "current" {}

resource "azurerm_role_assignment" "keyvault_secrets_officer" {
  scope                = azurerm_key_vault.gh_runner_kv.id
  role_definition_name = "Key Vault Secrets Officer"
  principal_id         = data.azurerm_client_config.current.object_id
}
resource "azurerm_storage_account" "gh_runner_storage" {
  name                            = "ghrunnerstorage${random_integer.suffix.result}"
  resource_group_name             = azurerm_resource_group.gh_runner_manager.name
  location                        = azurerm_resource_group.gh_runner_manager.location
  account_tier                    = "Standard"
  account_replication_type        = "LRS"
  allow_nested_items_to_be_public = false

  lifecycle {
    prevent_destroy = true
  }
}

ephemeral "azurerm_key_vault_secret" "github_runner_pat" {
  name         = "github-runner-pat"
  key_vault_id = azurerm_key_vault.gh_runner_kv.id
}

ephemeral "azurerm_key_vault_secret" "github_runner_vm_admin_pw" {
  name         = "github-runner-vm-admin-pw"
  key_vault_id = azurerm_key_vault.gh_runner_kv.id
}

resource "azurerm_linux_function_app" "gh_runner_func_app" {
  name                                           = "gh-runner-func-app-${random_integer.suffix.result}"
  resource_group_name                            = azurerm_resource_group.gh_runner_manager.name
  location                                       = azurerm_resource_group.gh_runner_manager.location
  storage_account_name                           = azurerm_storage_account.gh_runner_storage.name
  storage_account_access_key                     = azurerm_storage_account.gh_runner_storage.primary_access_key
  service_plan_id                                = azurerm_service_plan.gh_runner_func_plan.id
  builtin_logging_enabled                        = false
  ftp_publish_basic_authentication_enabled       = false
  webdeploy_publish_basic_authentication_enabled = false
  site_config {
    http2_enabled                          = true
    ftps_state                             = "FtpsOnly"
    application_insights_connection_string = azurerm_application_insights.gh_runner_func_app.connection_string
    application_stack {
      python_version = "3.11"
    }
  }

  app_settings = {
    FUNCTIONS_WORKER_RUNTIME = "python"
    VAULT_URL                = azurerm_key_vault.gh_runner_kv.vault_uri
    SUBSCRIPTION_ID          = data.azurerm_client_config.current.subscription_id
    LOCATION                 = var.runner_location
    RESOURCE_GROUP           = "gh-runner-tmp-rg"
    VM_NAME                  = "gh-runner-vm"
    VM_SIZE                  = "Standard_D4ds_v5"
    VM_USE_SPOT              = "true"
    ADMIN_USERNAME           = "azureuser"
    CUSTOM_IMAGE_ID          = "/subscriptions/${data.azurerm_client_config.current.subscription_id}/resourceGroups/gh-actions-images-${var.runner_location}/providers/Microsoft.Compute/images/Win2022GHAzureRunner" # gets extended with virtualization flavor in function app code like this: CUSTOM_IMAGE_ID + "-" + virtualization_flavor
    ALLOWED_TENANT_ID        = data.azurerm_client_config.current.tenant_id
    ALLOWED_AUDIENCE         = "api://${azuread_application.gh_actions_runner_broker.client_id}"
    ALLOWED_CLIENT_IDS       = azuread_application.gh_actions_runner_broker.client_id
    ALLOWED_USER_OBJECT_IDS  = length(var.allowed_user_object_ids) > 0 ? join(",", var.allowed_user_object_ids) : data.azurerm_client_config.current.object_id
  }

  identity {
    type = "SystemAssigned"
  }

  tags = {
    "hidden-link: /app-insights-resource-id" = azurerm_application_insights.gh_runner_func_app.id
  }

  auth_settings_v2 {
    auth_enabled           = true
    default_provider       = "azureactivedirectory"
    unauthenticated_action = "Return401"
    active_directory_v2 {
      client_id            = azuread_application.gh_actions_runner_broker.client_id
      tenant_auth_endpoint = "https://sts.windows.net/${data.azurerm_client_config.current.tenant_id}/"
      allowed_audiences    = ["api://${azuread_application.gh_actions_runner_broker.client_id}"]
    }
    login {

    }
  }
}

resource "azurerm_service_plan" "gh_runner_func_plan" {
  name                     = "EastUSLinuxConsumption"
  resource_group_name      = azurerm_resource_group.gh_runner_manager.name
  location                 = azurerm_resource_group.gh_runner_manager.location
  os_type                  = "Linux"
  sku_name                 = "Y1"
  per_site_scaling_enabled = false
  lifecycle {
    ignore_changes = [worker_count]
  }
}

resource "azurerm_role_assignment" "keyvault_secrets_user" {
  scope                = azurerm_key_vault.gh_runner_kv.id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_linux_function_app.gh_runner_func_app.identity[0].principal_id
}

# Least-privilege role for the runner broker in place of subscription-wide
# Contributor. It can create/delete the per-job resource groups and manage the
# compute + networking resources inside them, but has no data-plane access (e.g.
# it cannot read Key Vault secrets outside its dedicated data-plane assignment)
# and no IAM (role assignment) permissions, shrinking the blast radius if the
# broker were ever compromised.
resource "azurerm_role_definition" "gh_runner_broker" {
  name        = "gh-runner-broker-${random_integer.suffix.result}"
  scope       = data.azurerm_subscription.current.id
  description = "Manage per-job GitHub runner resource groups and their VM/network resources. No data-plane or IAM access."

  permissions {
    actions = [
      "Microsoft.Resources/subscriptions/resourceGroups/read",
      "Microsoft.Resources/subscriptions/resourceGroups/write",
      "Microsoft.Resources/subscriptions/resourceGroups/delete",
      "Microsoft.Compute/*",
      "Microsoft.Network/*",
    ]
    not_actions = []
  }

  assignable_scopes = [
    data.azurerm_subscription.current.id,
  ]
}

resource "azurerm_role_assignment" "function_runner_broker" {
  scope              = data.azurerm_subscription.current.id
  role_definition_id = azurerm_role_definition.gh_runner_broker.role_definition_resource_id
  principal_id       = azurerm_linux_function_app.gh_runner_func_app.identity[0].principal_id
}

resource "azurerm_application_insights" "gh_runner_func_app" {
  name                = "gh-runner-func-app-${random_integer.suffix.result}"
  location            = azurerm_resource_group.gh_runner_manager.location
  resource_group_name = azurerm_resource_group.gh_runner_manager.name
  application_type    = "web"
}

data "azurerm_subscription" "current" {}

data "azurerm_role_definition" "monitoring_contributor" {
  name  = "Monitoring Contributor"
  scope = data.azurerm_subscription.current.id
}

data "azurerm_role_definition" "monitoring_reader" {
  name  = "Monitoring Reader"
  scope = data.azurerm_subscription.current.id
}

resource "azurerm_monitor_action_group" "smart_detection" {
  name                = "Application Insights Smart Detection"
  resource_group_name = azurerm_resource_group.gh_runner_manager.name
  short_name          = "SmartDetect"
  arm_role_receiver {
    name                    = "Monitoring Contributor"
    role_id                 = basename(data.azurerm_role_definition.monitoring_contributor.role_definition_id)
    use_common_alert_schema = true
  }
  arm_role_receiver {
    name                    = "Monitoring Reader"
    role_id                 = basename(data.azurerm_role_definition.monitoring_reader.role_definition_id)
    use_common_alert_schema = true
  }
}
