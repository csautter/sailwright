include "root" {
  path = find_in_parent_folders("root.hcl")
}

include "env" {
  path   = "${get_terragrunt_dir()}/../../_env/azure_gh_runner.hcl"
  expose = true
}

include "provider_azure_config" {
  path   = "${get_terragrunt_dir()}/../../_env/provider_azure_config.hcl"
  expose = true
}

locals {
  env_local = try(read_terragrunt_config(find_in_parent_folders("env_local.hcl")), { locals = {} })
  env       = read_terragrunt_config(find_in_parent_folders("env.hcl"))
  config = merge(
    local.env.locals,
    local.env_local.locals
  )
}

inputs = {
  env = local.config.env
  # Legacy Azure Blob build cache; CI uses FTP. false deletes the cache storage account.
  build_cache_enabled = false
}