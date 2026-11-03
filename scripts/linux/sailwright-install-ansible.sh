#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd -P)"
project_root="$(cd "${script_dir}/../.." && pwd -P)"
# shellcheck source=scripts/common/ansible-core.sh
source "${project_root}/scripts/common/ansible-core.sh"

SUDO=""
if [[ "${EUID}" -ne 0 ]]; then
	SUDO="sudo"
fi

install_ansible() {
	# Explicit directories also support pipx versions without --global (Ubuntu 24.04).
	# pipx owns the isolated environment; the distribution Python stays untouched.
	${SUDO} env PIPX_HOME=/opt/sailwright/pipx PIPX_BIN_DIR=/usr/local/bin \
		pipx install --force --include-deps ansible
	${SUDO} env PIPX_HOME=/opt/sailwright/pipx PIPX_BIN_DIR=/usr/local/bin \
		pipx inject --force ansible pywinrm
	hash -r
}

install_ansible
check_ansible_core /usr/bin/python3
