# Accepted trivy findings:
# AZU-0012: state is accessed from devcontainers with changing public IPs; a default-deny
#   network rule would lock operators out. Access is restricted via Entra ID RBAC instead.
# AZU-0057: classic storage analytics logging is not worth the cost for a tiny state account.
# AZU-0058: LRS is sufficient for the dev environment.
# AZU-0060: a customer-managed key needs a Key Vault before the state backend exists (bootstrap).
#trivy:ignore:AZU-0012
#trivy:ignore:AZU-0057
#trivy:ignore:AZU-0058
#trivy:ignore:AZU-0060
resource "azurerm_storage_account" "this" {
  name                              = var.storage_account_name
  resource_group_name               = azurerm_resource_group.state.name
  location                          = data.azurerm_location.this.location
  account_tier                      = "Standard"
  account_replication_type          = "LRS"
  infrastructure_encryption_enabled = true
  allow_nested_items_to_be_public   = false

  # Keep previous state versions recoverable after corruption or accidental deletion.
  blob_properties {
    versioning_enabled = true

    delete_retention_policy {
      days = 30
    }

    container_delete_retention_policy {
      days = 30
    }
  }

  tags = {
    shortname = var.storage_account_name
    env       = var.env
  }

  lifecycle {
    prevent_destroy = true
  }
}

resource "azurerm_storage_encryption_scope" "this" {
  name                               = "defaultencryptionscope"
  storage_account_id                 = azurerm_storage_account.this.id
  source                             = "Microsoft.Storage"
  infrastructure_encryption_required = true

  lifecycle {
    prevent_destroy = true
  }
}

resource "azurerm_storage_container" "this" {
  name                  = var.container_name
  storage_account_id    = azurerm_storage_account.this.id
  container_access_type = "private"

  lifecycle {
    prevent_destroy = true
  }
}

# Assign Storage Blob Data Contributor role to the current user for the storage account
data "azurerm_client_config" "current" {}

resource "azurerm_role_assignment" "blob_data_contributor" {
  scope                = azurerm_storage_account.this.id
  role_definition_name = "Storage Blob Data Contributor"
  principal_id         = data.azurerm_client_config.current.object_id
}
