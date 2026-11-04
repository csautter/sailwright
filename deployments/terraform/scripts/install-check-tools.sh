#!/usr/bin/env bash
# Install the same Linux tools used by the devcontainer, without evaluating Dockerfile code.
set -euo pipefail
repo_root=$(git rev-parse --show-toplevel)
pin() {
    local value
    value=$(sed -n "s/^ENV $1=\"\([^\"]*\)\"$/\1/p" "$repo_root/.devcontainer/Dockerfile")
    [[ -n "$value" && "$value" != *$'\n'* ]] || { echo "Missing or ambiguous tool pin: $1" >&2; exit 1; }
    printf '%s' "$value"
}
case $(uname -m) in
    x86_64) arch=amd64; trivy_arch=64bit ;;
    aarch64|arm64) arch=arm64; trivy_arch=ARM64 ;;
    *) echo 'Unsupported Linux architecture' >&2; exit 1 ;;
esac
install_dir=${1:?Usage: install-check-tools.sh INSTALL_DIR}
mkdir -p "$install_dir"
install_dir=$(cd "$install_dir" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT
cd "$work_dir"
terraform_version=$(pin TERRAFORM_VERSION)
curl -fsSLO "https://releases.hashicorp.com/terraform/$terraform_version/terraform_${terraform_version}_linux_${arch}.zip"
curl -fsSLo SHA256SUMS "https://releases.hashicorp.com/terraform/$terraform_version/terraform_${terraform_version}_SHA256SUMS"
awk -v name="terraform_${terraform_version}_linux_${arch}.zip" '$2 == name {print}' SHA256SUMS > terraform.sha256
[[ -s terraform.sha256 ]]
sha256sum -c terraform.sha256
unzip -oq "terraform_${terraform_version}_linux_${arch}.zip" terraform -d "$install_dir"
for tool in TERRAGRUNT TFLINT TRIVY; do
    version=$(pin "${tool}_VERSION")
    checksum=$(pin "${tool}_SHA256_${arch^^}")
    case $tool in
        TERRAGRUNT) url="https://github.com/gruntwork-io/terragrunt/releases/download/v$version/terragrunt_linux_$arch" ;;
        TFLINT) url="https://github.com/terraform-linters/tflint/releases/download/v$version/tflint_linux_$arch.zip" ;;
        TRIVY) url="https://github.com/aquasecurity/trivy/releases/download/v$version/trivy_${version}_Linux-$trivy_arch.tar.gz" ;;
    esac
    curl -fsSL "$url" -o download
    printf '%s  download\n' "$checksum" | sha256sum -c -
    case $tool in
        TERRAGRUNT) install -m 755 download "$install_dir/terragrunt" ;;
        TFLINT) unzip -oq download tflint -d "$install_dir" ;;
        TRIVY) tar -xzf download -C "$install_dir" trivy ;;
    esac
done
