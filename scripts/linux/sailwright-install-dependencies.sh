#!/usr/bin/env bash

set -euo pipefail

install_go="false"

while [[ $# -gt 0 ]]; do
	case "$1" in
	--with-go)
		install_go="true"
		shift
		;;
	*)
		echo "Unknown option: $1" >&2
		exit 1
		;;
	esac
done

if [[ "$(uname -s)" != "Linux" ]]; then
	echo "This installer only supports Linux hosts." >&2
	exit 1
fi

if ! command -v apt-get >/dev/null 2>&1; then
	echo "This installer currently supports Ubuntu/Debian hosts with apt-get." >&2
	exit 1
fi

if [[ "${EUID}" -eq 0 ]]; then
	SUDO=""
else
	if ! command -v sudo >/dev/null 2>&1; then
		echo "sudo is required to install Linux dependencies." >&2
		exit 1
	fi
	SUDO="sudo"
fi

host_id=""
host_like=""
host_codename=""
if [[ -f /etc/os-release ]]; then
	# shellcheck disable=SC1091
	source /etc/os-release
	host_id="${ID:-}"
	host_like="${ID_LIKE:-}"
	host_codename="${VERSION_CODENAME:-${UBUNTU_CODENAME:-}}"
fi

if [[ -z "${host_codename}" ]] && command -v lsb_release >/dev/null 2>&1; then
	host_codename="$(lsb_release -cs)"
fi

if [[ -z "${host_codename}" ]]; then
	echo "Could not determine the Linux distribution codename required for the HashiCorp apt repository." >&2
	exit 1
fi

if [[ "${host_id}" != "ubuntu" && "${host_id}" != "debian" && "${host_like}" != *"debian"* ]]; then
	echo "Warning: detected ${host_id:-unknown} (${host_like:-unknown}); continuing because apt-get is available, but Ubuntu/Debian hosts are the primary target." >&2
fi

hashicorp_keyring_path="/usr/share/keyrings/hashicorp-archive-keyring.gpg"
hashicorp_sources_path="/etc/apt/sources.list.d/hashicorp.list"
script_dir=$(
	cd "$(dirname "$0")"
	pwd -P
)
project_root=$(
	cd "${script_dir}/../.."
	pwd -P
)

install_hashicorp_apt_repo() {
	${SUDO} apt-get update
	${SUDO} apt-get install -y ca-certificates curl gpg

	curl -fsSL https://apt.releases.hashicorp.com/gpg |
		${SUDO} gpg --dearmor --yes -o "${hashicorp_keyring_path}"

	printf 'deb [signed-by=%s] https://apt.releases.hashicorp.com %s main\n' \
		"${hashicorp_keyring_path}" \
		"${host_codename}" |
		${SUDO} tee "${hashicorp_sources_path}" >/dev/null
}

install_linux_packages() {
	local packages=(
		ansible
		asciinema
		curl
		ffmpeg
		gpg
		libarchive-tools
		libvirt-clients
		libvirt-daemon-system
		packer
		python3
		python3-winrm
		python3-venv
		qemu-efi-aarch64
		qemu-system-arm
		qemu-system-x86
		qemu-utils
		sshpass
		swtpm
		tar
		virt-manager
		virtinst
		vncsnapshot
		xorriso
		xz-utils
	)

	if apt-cache show 7zip >/dev/null 2>&1; then
		packages+=(7zip)
	elif apt-cache show p7zip-full >/dev/null 2>&1; then
		packages+=(p7zip-full)
	else
		echo "Neither 7zip nor p7zip-full is available from the configured apt repositories." >&2
		exit 1
	fi

	${SUDO} apt-get update
	${SUDO} apt-get install -y "${packages[@]}"
}

install_hashicorp_apt_repo
install_linux_packages
if [[ "${install_go}" == "true" ]]; then
	bash "${script_dir}/install-go-from-go-dev.sh" "${project_root}"
fi

packer version
ansible --version
