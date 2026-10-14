#!/bin/bash
set -e

# Space-separated list of images to build. VirtualBox runners are disabled in
# test-build-windows.yml, so only the Hyper-V image is built by default to avoid
# paying for an unused image. Set VIRTUALIZATION_FLAVORS="hyperv virtualbox" to
# build both again.
virtualization_flavors="${VIRTUALIZATION_FLAVORS:-hyperv}"

packer init windows-azure-gh-runner.pkr.hcl
subscription_id="$(packer inspect -var-file=windows-azure-gh-runner-secrets.pkrvars.hcl windows-azure-gh-runner.pkr.hcl | grep subscription_id | cut -d'"' -f2)"
location="eastus2"
image_resource_group="gh-actions-images-${location// /-}"
az group create --name "$image_resource_group" --location "$location" --subscription "$subscription_id"
for virtualization_flavor in $virtualization_flavors; do
  packer build -var-file=windows-azure-gh-runner-secrets.pkrvars.hcl -var="virtualization_flavor=$virtualization_flavor" -var="image_resource_group=$image_resource_group" -var="location=$location" -force windows-azure-gh-runner.pkr.hcl
done
