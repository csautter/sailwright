# Legacy Azure Blob build cache for Windows ISOs and other build artifacts.
#
# CI workflows use the FTP backend (storage-backend: ftp), so this storage account is
# no longer read or written by any in-repo workflow. It is disabled by default via
# var.build_cache_enabled; applying with the default DELETES the resource group, the
# storage account and all cached blobs. prevent_destroy cannot be made conditional,
# so it was removed to allow that deletion.
locals {
  build_cache_count = var.build_cache_enabled ? 1 : 0
}

resource "azurerm_resource_group" "cache_storage" {
  count    = local.build_cache_count
  name     = "gh-runner-storage-rg"
  location = var.runner_location
}

resource "azurerm_storage_account" "cache" {
  count                           = local.build_cache_count
  name                            = substr(replace("ghrunner${data.azurerm_client_config.current.subscription_id}", "-", ""), 0, 24)
  resource_group_name             = azurerm_resource_group.cache_storage[0].name
  location                        = azurerm_resource_group.cache_storage[0].location
  account_tier                    = "Standard"
  account_replication_type        = "LRS"
  allow_nested_items_to_be_public = false
  min_tls_version                 = "TLS1_2"

  blob_properties {
    versioning_enabled = false
  }
}

# General-purpose build cache container for ISOs, toolchain archives, and other
# large build dependencies.
resource "azurerm_storage_container" "build_cache" {
  count                 = local.build_cache_count
  name                  = "build-cache"
  storage_account_id    = azurerm_storage_account.cache[0].id
  container_access_type = "private"
}

# Grant the GitHub Actions service principal permission to read the resource group
resource "azurerm_role_assignment" "gh_actions_cache_rg_reader" {
  count                = local.build_cache_count
  scope                = azurerm_resource_group.cache_storage[0].id
  role_definition_name = "Reader"
  principal_id         = azuread_service_principal.gh_actions_runner_broker.object_id
}

# Grant the GitHub Actions service principal permission to read and upload blobs to the cache storage account
resource "azurerm_role_assignment" "gh_actions_cache_blob_contributor" {
  count                = local.build_cache_count
  scope                = azurerm_storage_account.cache[0].id
  role_definition_name = "Storage Blob Data Contributor"
  principal_id         = azuread_service_principal.gh_actions_runner_broker.object_id
}

# Grant the current user (deploying Terraform) permission to upload blobs to the cache storage account
resource "azurerm_role_assignment" "current_user_cache_blob_contributor" {
  count                = local.build_cache_count
  scope                = azurerm_storage_account.cache[0].id
  role_definition_name = "Storage Blob Data Contributor"
  principal_id         = data.azurerm_client_config.current.object_id
}

# Keep existing state addresses so re-enabling the cache does not recreate it.
moved {
  from = azurerm_resource_group.cache_storage
  to   = azurerm_resource_group.cache_storage[0]
}

moved {
  from = azurerm_storage_account.cache
  to   = azurerm_storage_account.cache[0]
}

moved {
  from = azurerm_storage_container.build_cache
  to   = azurerm_storage_container.build_cache[0]
}

moved {
  from = azurerm_role_assignment.gh_actions_cache_rg_reader
  to   = azurerm_role_assignment.gh_actions_cache_rg_reader[0]
}

moved {
  from = azurerm_role_assignment.gh_actions_cache_blob_contributor
  to   = azurerm_role_assignment.gh_actions_cache_blob_contributor[0]
}

moved {
  from = azurerm_role_assignment.current_user_cache_blob_contributor
  to   = azurerm_role_assignment.current_user_cache_blob_contributor[0]
}
