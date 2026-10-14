# Golden image build in GitHub Actions (.github/workflows/build-azure-runner-image.yml).
#
# The image builder gets its own identity, trusted only for the main branch, so pull
# request workflows (which can use the runner broker identity) cannot replace the image.

# Holds the managed images referenced by CUSTOM_IMAGE_ID in manager.tf. It was created
# by the build script before it was managed here; import it once on existing environments:
#   terragrunt import azurerm_resource_group.images /subscriptions/<sub>/resourceGroups/gh-actions-images-<location>
resource "azurerm_resource_group" "images" {
  name     = "gh-actions-images-${var.runner_location}"
  location = var.runner_location
}

# Packer creates its temporary VM, network and Key Vault here (build_resource_group_name),
# so the builder needs no subscription-level permission to create resource groups.
resource "azurerm_resource_group" "image_build" {
  name     = "gh-actions-image-build-${var.runner_location}"
  location = var.runner_location
}

resource "azuread_application" "gh_actions_image_builder" {
  display_name = "gh-actions-image-builder"
}

resource "azuread_service_principal" "gh_actions_image_builder" {
  client_id = azuread_application.gh_actions_image_builder.client_id
}

resource "azuread_application_federated_identity_credential" "gh_actions_image_builder_main" {
  application_id = azuread_application.gh_actions_image_builder.id
  display_name   = "github-actions-main"
  issuer         = "https://token.actions.githubusercontent.com"
  subject        = "repo:${var.github_repository}:ref:refs/heads/main"
  audiences      = ["api://AzureADTokenExchange"]
}

resource "azurerm_role_assignment" "image_builder_images_contributor" {
  scope                = azurerm_resource_group.images.id
  role_definition_name = "Contributor"
  principal_id         = azuread_service_principal.gh_actions_image_builder.object_id
}

resource "azurerm_role_assignment" "image_builder_build_contributor" {
  scope                = azurerm_resource_group.image_build.id
  role_definition_name = "Contributor"
  principal_id         = azuread_service_principal.gh_actions_image_builder.object_id
}
