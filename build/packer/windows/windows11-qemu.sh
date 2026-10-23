#!/usr/bin/env bash

set -e

arch="arm64"
headless="false"
vnc_port="5901"
cpus="4"
memory="4096"
verbose="false"
build_output_dir=""
artifact_output_path=""
use_hardware_acceleration="true"
packer_start_only="${DEV_ALCHEMY_PACKER_START_ONLY:-false}"
packer_start_timeout="${DEV_ALCHEMY_PACKER_START_TIMEOUT:-180}"

script_dir=$(
	cd "$(dirname "$0")" || exit 1
	pwd -P
)
project_root=$(
	cd "${script_dir}/../../.." || exit 1
	pwd -P
)

detect_host_os() {
	case "$(uname -s)" in
	Darwin)
		echo "darwin"
		;;
	Linux)
		echo "linux"
		;;
	*)
		return 1
		;;
	esac
}

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
	case "$host_os" in
	darwin)
		printf '%s\n' "$HOME/Library/Application Support/dev-alchemy"
		;;
	linux)
		printf '%s\n' "${XDG_DATA_HOME:-$HOME/.local/share}/dev-alchemy"
		;;
	*)
		return 1
		;;
	esac
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

duration_seconds() {
	local value="${1%s}"
	if [[ "$value" =~ ^[0-9]+$ && "$value" -gt 0 ]]; then
		printf '%s\n' "$value"
		return 0
	fi
	return 1
}

packer_probe_is_running() {
	jobs -pr | grep -qx "$1"
}

wait_for_packer_probe_settle() {
	local pid="$1"
	local settle_seconds="$2"
	local elapsed_seconds=0

	while packer_probe_is_running "$pid" && [[ "$elapsed_seconds" -lt "$settle_seconds" ]]; do
		sleep 1
		elapsed_seconds=$((elapsed_seconds + 1))
	done
}

stop_packer_probe_process() {
	local pid="$1"

	pkill -TERM -P "$pid" >/dev/null 2>&1 || true
	kill -TERM "$pid" >/dev/null 2>&1 || true

	for _ in 1 2 3 4 5; do
		if ! packer_probe_is_running "$pid"; then
			wait "$pid" >/dev/null 2>&1 || true
			return 0
		fi
		sleep 1
	done

	pkill -KILL -P "$pid" >/dev/null 2>&1 || true
	kill -KILL "$pid" >/dev/null 2>&1 || true
	wait "$pid" >/dev/null 2>&1 || true
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
		detected="$(systemd-detect-virt 2>/dev/null)" || detected=""
		if [[ -n "$detected" ]]; then
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

download_windows_iso_if_missing() {
	local target_arch="$1"
	local iso_path="$2"
	local -a playwright_args

	if [[ -f "$iso_path" ]]; then
		echo "Windows 11 $target_arch ISO already exists, skipping download"
		return 0
	fi

	echo "Downloading Windows 11 $target_arch ISO"
	mkdir -p "$(dirname "$iso_path")"
	cd "${project_root}/scripts/macos" || exit 1
	if [[ ! -d .venv ]]; then
		python3 -m venv .venv
	fi
	# shellcheck disable=SC1091
	source .venv/bin/activate

	pip install -r requirements.txt
	python -m playwright install chromium

	playwright_args=(playwright_win11_iso.py --download --save-path "$iso_path")
	if [[ "$target_arch" == "arm64" ]]; then
		playwright_args+=(--arm)
	fi
	python "${playwright_args[@]}"
	cd "${project_root}" || exit 1
}

run_packer_build() {
	packer build \
		-var "host_os=${host_os}" \
		-var "host_arch=${host_arch}" \
		-var "use_hardware_acceleration=${use_hardware_acceleration}" \
		-var "cache_dir=${effective_cache_dir}" \
		-var "build_output_dir=${build_output_dir}" \
		-var "qmp_socket_path=${qmp_socket_path}" \
		-var "artifact_output_path=${artifact_output_path}" \
		-var "iso_url=${win11_iso_path}" \
		-var "headless=${headless}" \
		-var "vnc_port=${vnc_port}" \
		-var "arch=${arch}" \
		-var "cpus=${cpus}" \
		-var "memory=${memory}" \
		"$packer_file"
}

run_packer_build_start_only() {
	local probe_log
	local packer_pid
	local elapsed_seconds
	local packer_started
	local vm_started
	local rc

	probe_log="$(mktemp "${TMPDIR:-/tmp}/dev-alchemy-packer-start-only.XXXXXX")" || return 1
	elapsed_seconds=0
	packer_started="false"
	vm_started="false"

	echo "Running Packer build start-only probe for up to ${packer_start_timeout_seconds}s."
	run_packer_build > >(tee "$probe_log") 2> >(tee -a "$probe_log" >&2) &
	packer_pid=$!

	while packer_probe_is_running "$packer_pid"; do
		if grep -Eq "qemu\\.win11:|Build 'qemu\\.win11'" "$probe_log"; then
			packer_started="true"
		fi
		if grep -Eiq "Starting VM|Launching VM|Waiting for WinRM|Connected to WinRM|Using winrm communicator" "$probe_log"; then
			vm_started="true"
			break
		fi
		if [[ "$elapsed_seconds" -ge "$packer_start_timeout_seconds" ]]; then
			break
		fi
		sleep 5
		elapsed_seconds=$((elapsed_seconds + 5))
	done

	if packer_probe_is_running "$packer_pid"; then
		if [[ "$vm_started" == "true" ]]; then
			wait_for_packer_probe_settle "$packer_pid" 15
			if packer_probe_is_running "$packer_pid"; then
				echo "Packer build start-only probe succeeded; VM startup was observed."
				stop_packer_probe_process "$packer_pid"
				return 0
			fi

			set +e
			wait "$packer_pid"
			rc=$?
			set -e
			if [[ "$rc" -eq 0 ]]; then
				echo "Packer build completed during start-only probe."
				return 0
			fi

			echo "Packer build start-only probe failed after VM startup was observed. Log: $probe_log" >&2
			return "$rc"
		fi
		if [[ "$packer_started" == "true" && "$elapsed_seconds" -ge "$packer_start_timeout_seconds" ]]; then
			echo "Packer build start-only probe succeeded; the qemu builder was still running after ${packer_start_timeout_seconds}s."
			stop_packer_probe_process "$packer_pid"
			return 0
		fi

		echo "Packer build start-only probe did not observe qemu builder startup within ${packer_start_timeout_seconds}s." >&2
		stop_packer_probe_process "$packer_pid"
		return 1
	fi

	set +e
	wait "$packer_pid"
	rc=$?
	set -e
	if [[ "$rc" -eq 0 ]]; then
		echo "Packer build completed during start-only probe."
		return 0
	fi

	echo "Packer build start-only probe failed before a successful startup could be confirmed. Log: $probe_log" >&2
	return "$rc"
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
	--vnc-port)
		if [[ -n "$2" && "$2" =~ ^[0-9]+$ ]]; then
			vnc_port="$2"
			shift 2
		else
			echo "Invalid value for --vnc-port: $2. It must be a number." >&2
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
	--packer-start-only)
		packer_start_only="true"
		shift
		;;
	--packer-start-timeout)
		if [[ -n "$2" ]]; then
			packer_start_timeout="$2"
			shift 2
		else
			echo "Invalid value for --packer-start-timeout: $2." >&2
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
	*)
		echo "Unknown option: $1" >&2
		exit 1
		;;
	esac
done

packer_start_timeout_seconds="$(duration_seconds "$packer_start_timeout")" || {
	echo "Invalid value for packer start timeout: $packer_start_timeout. Use a positive number of seconds." >&2
	exit 1
}

host_os="$(detect_host_os)" || {
	echo "Unsupported host OS: $(uname -s)" >&2
	exit 1
}
host_arch="$(detect_host_arch)" || {
	echo "Unsupported host architecture: $(uname -m)" >&2
	exit 1
}

if [[ "$host_os" == "linux" ]]; then
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
fi

app_data_dir="${DEV_ALCHEMY_APP_DATA_DIR:-$(default_app_data_dir)}"
cache_dir="${DEV_ALCHEMY_CACHE_DIR:-$app_data_dir/cache}"
packer_cache_dir="${DEV_ALCHEMY_PACKER_CACHE_DIR:-$app_data_dir/packer_cache}"

if [[ -z "$build_output_dir" ]]; then
	build_output_dir="$cache_dir/windows11/qemu-out-windows11-${arch}"
fi

echo "Using host OS: $host_os"
echo "Using host architecture: $host_arch"
echo "Using guest architecture: $arch"
echo "Headless mode: $headless"

cd "${project_root}" || exit 1
mkdir -p "$cache_dir" "$packer_cache_dir"
export DEV_ALCHEMY_APP_DATA_DIR="$app_data_dir"
export DEV_ALCHEMY_CACHE_DIR="$cache_dir"
export DEV_ALCHEMY_PACKER_CACHE_DIR="$packer_cache_dir"
export PACKER_CACHE_DIR="$packer_cache_dir"

effective_cache_dir="$cache_dir"
start_only_cache_dir=""
cleanup_start_only_cache() {
	if [[ -n "$start_only_cache_dir" && -d "$start_only_cache_dir" ]]; then
		rm -rf "$start_only_cache_dir"
	fi
}
# Packer puts the QMP socket into the output directory by default. UNIX socket paths are
# limited to 104 bytes on macOS and 108 on Linux, too short for an output directory in
# the cache, so the socket gets its own short directory.
qmp_socket_dir=""
create_qmp_socket_dir() {
	qmp_socket_dir="$(mktemp -d "/tmp/sw-qmp.XXXXXX")" || return 1
	qmp_socket_path="$qmp_socket_dir/qmp.sock"
}
cleanup_qmp_socket_dir() {
	if [[ -n "$qmp_socket_dir" && -d "$qmp_socket_dir" ]]; then
		rm -rf "$qmp_socket_dir"
	fi
}
cleanup_temp_dirs() {
	cleanup_start_only_cache
	cleanup_qmp_socket_dir
}
trap cleanup_temp_dirs EXIT

if is_truthy "$packer_start_only"; then
	start_only_cache_dir="$(mktemp -d "/tmp/da-pc.XXXXXX")" || exit 1
	effective_cache_dir="$start_only_cache_dir"
	build_output_dir="$effective_cache_dir/o"
	echo "Using isolated cache directory for start-only Packer probe: $effective_cache_dir"
fi

windows_source_iso_path="${cache_dir}/windows11/iso/win11_english_${arch}.iso"
legacy_windows_iso_path="${cache_dir}/windows11/iso/win11_25h2_english_${arch}.iso"
if [[ ! -f "$windows_source_iso_path" && -f "$legacy_windows_iso_path" ]]; then
	echo "Renaming legacy Windows 11 ISO $legacy_windows_iso_path to $windows_source_iso_path"
	mv "$legacy_windows_iso_path" "$windows_source_iso_path"
fi
download_windows_iso_if_missing "$arch" "$windows_source_iso_path"

bash "${project_root}/scripts/macos/download-utm-guest-tools.sh"
bash "${project_root}/scripts/macos/download-virtio-win-iso.sh"

if [[ "$arch" == "arm64" ]]; then
	if ! bash "${project_root}/scripts/macos/download-arm64-uefi.sh"; then
		echo "Failed to prepare ARM64 UEFI firmware." >&2
		exit 1
	fi
	for firmware_path in \
		"${cache_dir}/qemu-uefi/usr/share/AAVMF/AAVMF_CODE.no-secboot.fd" \
		"${cache_dir}/qemu-uefi/usr/share/AAVMF/AAVMF_VARS.fd"; do
		if [[ ! -f "$firmware_path" ]]; then
			echo "ARM64 UEFI firmware is missing: $firmware_path" >&2
			exit 1
		fi
	done

	echo "Creating Windows 11 ARM64 unattended ISO..."
	bash "${project_root}/scripts/macos/create-win11-autounattend-iso.sh"
fi

echo "Creating QCOW2 disk image..."
if [[ -z "$artifact_output_path" ]]; then
	artifact_output_path="${effective_cache_dir}/windows11/qemu-windows11-${arch}.qcow2"
fi
mkdir -p "$(dirname "$artifact_output_path")"
rm -f "$artifact_output_path"
qemu-img create -f qcow2 -o compression_type=zstd "$artifact_output_path" 64G
qemu-img info "$artifact_output_path"

packer_file="build/packer/windows/windows11-qemu.pkr.hcl"
packer init "$packer_file"

if [[ "$arch" == "amd64" ]]; then
	win11_iso_path="$windows_source_iso_path"
else
	win11_iso_path="${cache_dir}/windows11/iso/Win11_ARM64_Unattended.iso"
fi

if is_truthy "$packer_start_only"; then
	ln -s "$cache_dir/utm" "$effective_cache_dir/utm"
	ln -s "$cache_dir/windows" "$effective_cache_dir/windows"
	if [[ "$arch" == "arm64" ]]; then
		ln -s "$cache_dir/qemu-uefi" "$effective_cache_dir/qemu-uefi"
	fi
fi

output_dir="$build_output_dir"
if [[ -d "$output_dir" ]]; then
	echo "Removing existing Packer output directory..."
	rm -rf "$output_dir"
fi
mkdir -p "$(dirname "$output_dir")"
create_qmp_socket_dir || exit 1

if [[ "$verbose" == "true" ]]; then
	export PACKER_LOG=1
fi

if is_truthy "$packer_start_only"; then
	run_packer_build_start_only
else
	packer_rc=0
	run_packer_build || packer_rc=$?
	# On success the artifact is in the cache and the output directory only holds
	# Packer's scratch files; on failure they are of no use either.
	rm -rf "$output_dir"
	if [[ "$packer_rc" -ne 0 ]]; then
		exit "$packer_rc"
	fi
fi
