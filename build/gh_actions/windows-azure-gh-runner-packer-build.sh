#!/bin/bash
set -e

# Provisioner source paths in the template are relative to this directory.
cd "$(dirname "$0")"

# Space-separated list of images to build. VirtualBox runners are disabled in
# test-build-windows.yml, so only the Hyper-V image is built by default to avoid
# paying for an unused image. Set VIRTUALIZATION_FLAVORS="hyperv virtualbox" to
# build both again.
virtualization_flavors="${VIRTUALIZATION_FLAVORS:-hyperv}"

# Both resource groups are managed by Terraform (deployments/terraform/modules/azure_gh_runner).
location="${LOCATION:-eastus2}"
image_resource_group="gh-actions-images-${location}"
build_resource_group="gh-actions-image-build-${location}"

# Locally, tenant_id and subscription_id come from the secrets var file. In CI they
# are passed as PKR_VAR_* environment variables instead.
var_file="windows-azure-gh-runner-secrets.pkrvars.hcl"
var_file_args=()
if [ -f "$var_file" ]; then
  var_file_args=(-var-file="$var_file")
fi

packer init windows-azure-gh-runner.pkr.hcl
for virtualization_flavor in $virtualization_flavors; do
  packer build "${var_file_args[@]}" -var="virtualization_flavor=$virtualization_flavor" -var="image_resource_group=$image_resource_group" -var="build_resource_group=$build_resource_group" -force windows-azure-gh-runner.pkr.hcl
done
