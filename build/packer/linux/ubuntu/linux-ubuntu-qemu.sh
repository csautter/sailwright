#!/usr/bin/env bash

set +e
set -x

arch="arm64"
headless="false"
ubuntu_type="server"
vnc_port="5901"
cpus="4"
memory="4096"
verbose="false"
build_output_dir=""
artifact_output_path=""
use_hardware_acceleration="true"

script_dir=$(
	cd "$(dirname "$0")" || exit 1
	pwd -P
)
project_root=$(
	cd "${script_dir}/../../../.." || exit 1
	pwd -P
)

# renovate: datasource=custom.ubuntu-live-server-amd64 depName=ubuntu-live-server-amd64 versioning=loose
UBUNTU_LIVE_SERVER_AMD64_VERSION="24.04.5"
UBUNTU_LIVE_SERVER_AMD64_SHA256="97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8"
# renovate: datasource=custom.ubuntu-live-server-arm64 depName=ubuntu-live-server-arm64 versioning=loose
UBUNTU_LIVE_SERVER_ARM64_VERSION="24.04.5"
UBUNTU_LIVE_SERVER_ARM64_SHA256="f12484464c7d4d73e2167e0a7dbda1e669f8fae9bcf778dc0944937034b320cb"

detect_host_arch() {
	case "$(uname -m)" in
	x86_64 | amd64)
		echo "amd64"
		;;
	aarch64 | arm64)
		echo "arm64"
		;;
	*)
		return 1
		;;
	esac
}

default_app_data_dir() {
	printf '%s\n' "${XDG_DATA_HOME:-$HOME/.local/share}/dev-alchemy"
}

file_size_bytes() {
	if [[ ! -f "$1" ]]; then
		echo "0"
		return 0
	fi
	stat -c%s "$1"
}

is_truthy() {
	case "$1" in
	1 | true | TRUE | True | yes | YES | on | ON)
		return 0
		;;
	*)
		return 1
		;;
	esac
}

qemu_binary_for_arch() {
	case "$1" in
	amd64)
		echo "qemu-system-x86_64"
		;;
	arm64)
		echo "qemu-system-aarch64"
		;;
	*)
		return 1
		;;
	esac
}

detect_virtualization_type() {
	if command -v systemd-detect-virt >/dev/null 2>&1; then
		local detected
		detected="$(systemd-detect-virt 2>/dev/null)"
		if [[ $? -eq 0 && -n "$detected" ]]; then
			printf '%s\n' "$detected"
			return 0
		fi
	fi

	if grep -qiE '(^flags|^Features).*(^| )hypervisor( |$)' /proc/cpuinfo 2>/dev/null; then
		printf '%s\n' "generic-vm"
		return 0
	fi

	if [[ -r /sys/class/dmi/id/product_name ]] &&
		grep -qiE 'kvm|qemu|vmware|virtualbox|hyper-v|virtual machine|bochs|xen' /sys/class/dmi/id/product_name 2>/dev/null; then
		cat /sys/class/dmi/id/product_name
		return 0
	fi

	printf '%s\n' "none"
}

probe_kvm_with_qemu() {
	local target_arch="$1"
	local qemu_binary
	local -a probe_args
	local probe_rc

	qemu_binary="$(qemu_binary_for_arch "$target_arch")" || return 1
	if ! command -v "$qemu_binary" >/dev/null 2>&1; then
		echo "Skipping KVM runtime probe because $qemu_binary is not available in PATH yet." >&2
		return 0
	fi

	if [[ "$target_arch" == "amd64" ]]; then
		probe_args=(-accel kvm -machine q35 -cpu host -display none -nodefaults -nographic -monitor none -serial none -S)
	else
		probe_args=(-accel kvm -machine virt -cpu host -display none -nodefaults -nographic -monitor none -serial none -S)
	fi

	if command -v timeout >/dev/null 2>&1; then
		timeout 3s "$qemu_binary" "${probe_args[@]}" >/dev/null 2>&1
		probe_rc=$?
		[[ $probe_rc -eq 0 || $probe_rc -eq 124 ]]
		return
	fi

	"$qemu_binary" -accel help 2>/dev/null | grep -qw kvm
}

linux_kvm_is_usable() {
	local target_arch="$1"

	if [[ ! -e /dev/kvm ]]; then
		echo "KVM device /dev/kvm is missing." >&2
		return 1
	fi

	if [[ ! -r /dev/kvm || ! -w /dev/kvm ]]; then
		echo "KVM device /dev/kvm is not accessible for the current user." >&2
		return 1
	fi

	if ! probe_kvm_with_qemu "$target_arch"; then
		echo "QEMU could not initialize KVM for architecture $target_arch." >&2
		return 1
	fi

	return 0
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--arch)
		if [[ -n "$2" && ("$2" == "amd64" || "$2" == "arm64") ]]; then
			arch="$2"
			shift 2
		else
			echo "Invalid value for --arch: $2. Allowed values are 'amd64' or 'arm64'." >&2
			exit 1
		fi
		;;
	--headless)
		headless="true"
		shift
		;;
	--cpus)
		if [[ -n "$2" && "$2" =~ ^[0-9]+$ ]]; then
			cpus="$2"
			shift 2
		else
			echo "Invalid value for --cpus: $2. It must be a number." >&2
			exit 1
		fi
		;;
	--memory)
		if [[ -n "$2" && "$2" =~ ^[0-9]+$ ]]; then
			memory="$2"
			shift 2
		else
			echo "Invalid value for --memory: $2. It must be a number." >&2
			exit 1
		fi
		;;
	--vnc-port)
		if [[ -n "$2" && "$2" =~ ^[0-9]+$ ]]; then
			vnc_port="$2"
			shift 2
		else
			echo "Invalid value for --vnc-port: $2. It must be a number." >&2
			exit 1
		fi
		;;
	--ubuntu-type)
		if [[ -n "$2" && ("$2" == "server" || "$2" == "desktop") ]]; then
			ubuntu_type="$2"
			shift 2
		else
			echo "Invalid value for --ubuntu-type: $2. Allowed values are 'server' or 'desktop'." >&2
			exit 1
		fi
		;;
	--project-root)
		if [[ -n "$2" ]]; then
			project_root="$2"
			shift 2
		else
			echo "Invalid value for --project-root: $2." >&2
			exit 1
		fi
		;;
		--build-output-dir)
			if [[ -n "$2" ]]; then
				build_output_dir="$2"
				shift 2
			else
				echo "Invalid value for --build-output-dir: $2." >&2
				exit 1
			fi
			;;
		--artifact-output-path)
			if [[ -n "$2" ]]; then
				artifact_output_path="$2"
				shift 2
			else
				echo "Invalid value for --artifact-output-path: $2." >&2
				exit 1
			fi
			;;
		--verbose)
			set -x
			verbose="true"
			shift
			;;
	*)
		echo "Unknown option: $1" >&2
		exit 1
		;;
	esac
done

if [[ "$(uname -s)" != "Linux" ]]; then
	echo "This script only supports Linux hosts. Use linux-ubuntu-on-macos.sh on macOS." >&2
	exit 1
fi

host_arch="$(detect_host_arch)" || {
	echo "Unsupported host architecture: $(uname -m)" >&2
	exit 1
}

if is_truthy "${DEV_ALCHEMY_QEMU_FORCE_SOFTWARE_EMULATION:-}"; then
	use_hardware_acceleration="false"
	echo "DEV_ALCHEMY_QEMU_FORCE_SOFTWARE_EMULATION is set; forcing software emulation."
elif [[ "$host_arch" == "$arch" ]]; then
	virtualization_type="$(detect_virtualization_type)"
	if [[ "$virtualization_type" != "none" ]]; then
		echo "Detected virtualized host environment: $virtualization_type"
	fi

	if linux_kvm_is_usable "$arch"; then
		echo "KVM acceleration probe succeeded; using hardware acceleration."
	else
		use_hardware_acceleration="false"
		echo "KVM acceleration is unavailable or incomplete on this host; falling back to software emulation."
	fi
fi

if [[ -z "$build_output_dir" ]]; then
	build_output_dir="/tmp/dev-alchemy/qemu-out-ubuntu-${ubuntu_type}-${arch}"
fi

app_data_dir="${DEV_ALCHEMY_APP_DATA_DIR:-$(default_app_data_dir)}"
cache_dir="${DEV_ALCHEMY_CACHE_DIR:-$app_data_dir/cache}"
packer_cache_dir="${DEV_ALCHEMY_PACKER_CACHE_DIR:-$app_data_dir/packer_cache}"

mkdir -p "$cache_dir" "$packer_cache_dir"
export DEV_ALCHEMY_APP_DATA_DIR="$app_data_dir"
export DEV_ALCHEMY_CACHE_DIR="$cache_dir"
export DEV_ALCHEMY_PACKER_CACHE_DIR="$packer_cache_dir"
export PACKER_CACHE_DIR="$packer_cache_dir"

if [[ "$arch" == "arm64" ]]; then
	if ! bash "$project_root/scripts/macos/download-arm64-uefi.sh"; then
		echo "Failed to prepare ARM64 UEFI firmware." >&2
		exit 1
	fi
	for firmware_path in \
		"$cache_dir/qemu-uefi/usr/share/AAVMF/AAVMF_CODE.no-secboot.fd" \
		"$cache_dir/qemu-uefi/usr/share/AAVMF/AAVMF_VARS.fd"; do
		if [[ ! -f "$firmware_path" ]]; then
			echo "ARM64 UEFI firmware is missing: $firmware_path" >&2
			exit 1
		fi
	done
fi

iso_path="$cache_dir/linux/ubuntu-${UBUNTU_LIVE_SERVER_AMD64_VERSION}-live-server-amd64.iso"
iso_checksum="${UBUNTU_LIVE_SERVER_AMD64_SHA256}"
if [[ -z "$artifact_output_path" ]]; then
	artifact_output_path="$cache_dir/ubuntu/qemu-ubuntu-${ubuntu_type}-packer-${arch}.qcow2"
fi
if [[ "$arch" == "arm64" ]]; then
	iso_path="$cache_dir/linux/ubuntu-${UBUNTU_LIVE_SERVER_ARM64_VERSION}-live-server-arm64.iso"
	iso_url="https://cdimage.ubuntu.com/ubuntu/releases/${UBUNTU_LIVE_SERVER_ARM64_VERSION}/release/ubuntu-${UBUNTU_LIVE_SERVER_ARM64_VERSION}-live-server-arm64.iso"
	iso_checksum="${UBUNTU_LIVE_SERVER_ARM64_SHA256}"
	mkdir -p "$(dirname "$iso_path")"

	if [[ ! -f "$iso_path" || "$(file_size_bytes "$iso_path")" -lt 2500000000 ]]; then
		echo "Downloading Ubuntu ISO (supports resume)..."
		if ! curl --fail --no-buffer --retry 10 --continue-at - -L -# -o "$iso_path" "$iso_url"; then
			echo "Failed to download Ubuntu ISO." >&2
			exit 1
		fi
	fi

	echo "Verifying ISO checksum..."
	downloaded_checksum=$(sha256sum "$iso_path" | awk '{print $1}')
	if [[ "$downloaded_checksum" != "$iso_checksum" ]]; then
		echo "Checksum mismatch for $iso_path" >&2
		exit 1
	fi
fi

if [[ "$arch" == "arm64" ]]; then
	echo "Creating QCOW2 disk image..."
	output_directory="$(dirname "$artifact_output_path")"
	mkdir -p "$output_directory"
	echo "Removing existing QCOW2 disk image if it exists..."
	rm -f "$artifact_output_path"
	qemu-img create -f qcow2 -o compression_type=zstd "$artifact_output_path" 64G
	qemu-img info "$artifact_output_path"
fi

if [[ "$arch" == "arm64" ]]; then
	cd "$project_root/build/packer/linux/ubuntu/cloud-init/qemu-${ubuntu_type}" || exit 1
	rm -f cidata.iso
	xorriso -as mkisofs -V cidata -o cidata.iso user-data meta-data
	cd "$project_root" || exit 1
fi

output_dir="$build_output_dir"
if [[ -d "$output_dir" ]]; then
	echo "Removing existing Packer output directory..."
	rm -rf "$output_dir"
fi
mkdir -p "$(dirname "$output_dir")"

packer_file="build/packer/linux/ubuntu/linux-ubuntu-qemu.pkr.hcl"
packer init "$packer_file"

if [[ "$verbose" == "true" ]]; then
	export PACKER_LOG=1
fi

packer build \
	-var "host_os=linux" \
	-var "host_arch=$host_arch" \
	-var "use_hardware_acceleration=$use_hardware_acceleration" \
	-var "cache_dir=$cache_dir" \
	-var "build_output_dir=$build_output_dir" \
	-var "artifact_output_path=$artifact_output_path" \
	-var "iso_url=$iso_path" \
	-var "iso_checksum=sha256:$iso_checksum" \
	-var "ubuntu_type=$ubuntu_type" \
	-var "headless=$headless" \
	-var "vnc_port=$vnc_port" \
	-var "arch=$arch" \
	-var "cpus=$cpus" \
	-var "memory=$memory" \
	"$packer_file"
