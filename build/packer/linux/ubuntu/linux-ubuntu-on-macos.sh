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
packer_start_only="${DEV_ALCHEMY_PACKER_START_ONLY:-false}"
packer_start_timeout="${DEV_ALCHEMY_PACKER_START_TIMEOUT:-180}"

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

file_size_bytes() {
	if [[ ! -f "$1" ]]; then
		echo "0"
		return 0
	fi
	stat -f%z "$1"
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

run_packer_build() {
	packer build \
		-var "host_os=darwin" \
		-var "host_arch=$host_arch" \
		-var "use_hardware_acceleration=true" \
			-var "cache_dir=$effective_cache_dir" \
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
		if grep -Eq "qemu\\.ubuntu:|Build 'qemu\\.ubuntu'" "$probe_log"; then
			packer_started="true"
		fi
		if grep -Eiq "Starting VM|Launching VM|Waiting for SSH|Connected to SSH|Using ssh communicator" "$probe_log"; then
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

			wait "$packer_pid"
			rc=$?
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

	wait "$packer_pid"
	rc=$?
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

if [[ "$(uname -s)" != "Darwin" ]]; then
	echo "This script only supports macOS hosts. Use linux-ubuntu-on-linux.sh on Linux." >&2
	exit 1
fi

host_arch="$(detect_host_arch)" || {
	echo "Unsupported host architecture: $(uname -m)" >&2
	exit 1
}

if [[ -z "$build_output_dir" ]]; then
	build_output_dir="/tmp/dev-alchemy/qemu-out-ubuntu-${ubuntu_type}-${arch}"
fi

app_data_dir="${DEV_ALCHEMY_APP_DATA_DIR:-$HOME/Library/Application Support/dev-alchemy}"
cache_dir="${DEV_ALCHEMY_CACHE_DIR:-$app_data_dir/cache}"
packer_cache_dir="${DEV_ALCHEMY_PACKER_CACHE_DIR:-$app_data_dir/packer_cache}"

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

if is_truthy "$packer_start_only"; then
	# Keep start-only paths short because QEMU's macOS QMP socket path limit is 104 bytes.
	start_only_cache_dir="$(mktemp -d "/tmp/da-pc.XXXXXX")" || exit 1
	effective_cache_dir="$start_only_cache_dir"
	build_output_dir="$effective_cache_dir/o"
	trap cleanup_start_only_cache EXIT
	echo "Using isolated cache directory for start-only Packer probe: $effective_cache_dir"
fi

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
	if is_truthy "$packer_start_only"; then
		ln -s "$cache_dir/qemu-uefi" "$effective_cache_dir/qemu-uefi"
	fi
fi

iso_path="$cache_dir/linux/ubuntu-${UBUNTU_LIVE_SERVER_AMD64_VERSION}-live-server-amd64.iso"
iso_checksum="${UBUNTU_LIVE_SERVER_AMD64_SHA256}"
if [[ -z "$artifact_output_path" ]]; then
	artifact_output_path="$effective_cache_dir/ubuntu/qemu-ubuntu-${ubuntu_type}-packer-${arch}.qcow2"
fi
if [[ "$arch" == "arm64" ]]; then
	iso_path="$cache_dir/linux/ubuntu-${UBUNTU_LIVE_SERVER_ARM64_VERSION}-live-server-arm64.iso"
	iso_url="https://cdimage.ubuntu.com/releases/${UBUNTU_LIVE_SERVER_ARM64_VERSION}/release/ubuntu-${UBUNTU_LIVE_SERVER_ARM64_VERSION}-live-server-arm64.iso"
	iso_checksum="${UBUNTU_LIVE_SERVER_ARM64_SHA256}"
	mkdir -p "$(dirname "$iso_path")"

	if [[ ! -f "$iso_path" || "$(file_size_bytes "$iso_path")" -lt 2500000000 ]]; then
		echo "Downloading Ubuntu ISO (supports resume)..."
		if ! curl --no-buffer --retry 10 --continue-at - -L -# -o "$iso_path" "$iso_url"; then
			echo "Failed to download Ubuntu ISO." >&2
			exit 1
		fi
	fi

	echo "Verifying ISO checksum..."
	downloaded_checksum=$(shasum -a 256 "$iso_path" | awk '{print $1}')
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
if ! packer init "$packer_file"; then
	echo "Packer init failed for $packer_file." >&2
	exit 1
fi

if [[ "$verbose" == "true" ]]; then
	export PACKER_LOG=1
fi

if is_truthy "$packer_start_only"; then
	run_packer_build_start_only
else
	run_packer_build
fi
