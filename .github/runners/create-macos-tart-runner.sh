#!/bin/bash
set -e

# This script creates a new ephemeral runner on macOS using tart and registers it with GitHub
# Actions. After each job completes the runner deregisters itself, the VM is shut down, and a
# fresh VM + runner is started automatically to replenish the runner pool.
#
# Required environment variables (or set defaults below):
#   GITHUB_SCOPE      - "repo" (default) or "org"
#   GITHUB_REPO       - owner/repo  (required when GITHUB_SCOPE=repo, e.g. "myorg/myrepo")
#   GITHUB_ORG        - org name    (required when GITHUB_SCOPE=org,  e.g. "myorg")
#   RUNNER_LABELS     - comma-separated runner labels/tags (default: "macos,tart,arm64,macos-26-tart")
#   RUNNER_NAME_BASE  - base name; a timestamp suffix is appended each run (default: <hostname>-tart)
#   VM_SSH_USER       - SSH user inside the tart VM        (default: admin)
#   VM_SSH_PASS       - SSH password inside the tart VM    (default: admin)
#   RUNNER_DIR        - install directory inside the VM    (default: /Users/admin/actions-runner)
#   VM_BASE_IMAGE     - tart base image name               (default: tahoe-base)
#   VM_CLONE_PER_RUN  - clone base image for every run so the VM starts clean (default: true)
#   MAX_RUNS          - maximum number of runner cycles per worker before this script exits (default: 0 = infinite)
#   RUNNER_POOL_SIZE  - number of parallel runner workers / VMs to run simultaneously (default: 1)
#   VM_CPU_COUNT      - number of vCPU cores to assign to each VM (default: image default)
#   VM_MEMORY_MB      - memory in MiB to assign to each VM (default: image default)
#   GITHUB_CANCEL_RUN_ON_SHUTDOWN       - cancel active workflow run before VM teardown (default: true)
#   GITHUB_FORCE_CANCEL_RUN_ON_SHUTDOWN - force-cancel if normal cancel does not settle (default: false)
#   RUNNER_SHUTDOWN_GRACE_SECONDS       - seconds to wait for cancel/deregister before preserving the VM (default: 0 = wait indefinitely)
#   RUNNER_FORCE_CANCEL_AFTER_SECONDS   - seconds to wait before optional force-cancel (default: 30)
#   RUNNER_SHUTDOWN_POLL_SECONDS        - seconds between runner-state polls during shutdown (default: 5, minimum: 1)
#   PREPARE_UTM_AUTOMATION              - clear UTM quarantine and grant Automation in each VM before registration (default: true)
#   RUNNER_NETWORK                      - "nat" (default) or "bridged:<host interface>", e.g. "bridged:Wi-Fi"
#   RUNNER_DNS_SERVERS                  - guest DNS: "router" (host's default gateway, default with NAT), "dhcp"
#                                         (default when bridged), or space/comma-separated resolver IPs
#   RUNNER_DNS_SERVICE                  - macOS network service inside the guest (default: Ethernet)
#   RUNNER_FIXED_MAC                    - give each worker slot a stable MAC so DHCP leases are reused (default: true)
#   RUNNER_MAC_ADDRESSES                - optional comma-separated MACs per worker slot (default: derived from host + slot)
#   RUNNER_NETWORK_CHECK                - check DNS + HTTPS from the guest before registering (default: true)
#   RUNNER_NETWORK_CHECK_URLS           - space-separated HTTPS URLs for that check (default: GitHub web and API)
#   RUNNER_NETWORK_CHECK_TIMEOUT        - seconds the check may take (default: 90)

# ─── Configuration ─────────────────────────────────────────────────────────────
GITHUB_SCOPE="${GITHUB_SCOPE:-repo}"
GITHUB_REPO="${GITHUB_REPO:-csautter/sailwright}"
GITHUB_ORG="${GITHUB_ORG:-}"
RUNNER_LABELS="${RUNNER_LABELS:-macos,tart,arm64,macos-26-tart}"
RUNNER_NAME_BASE="${RUNNER_NAME_BASE:-$(hostname -s)-tart}"
VM_SSH_USER="${VM_SSH_USER:-admin}"
VM_SSH_PASS="${VM_SSH_PASS:-admin}"
RUNNER_DIR="${RUNNER_DIR:-/Users/admin/actions-runner}"
VM_BASE_IMAGE="${VM_BASE_IMAGE:-tahoe-runner}"
VM_CLONE_PER_RUN="${VM_CLONE_PER_RUN:-true}"
MAX_RUNS="${MAX_RUNS:-0}"
# Number of parallel runner workers; each worker manages its own VM and runner cycle.
RUNNER_POOL_SIZE="${RUNNER_POOL_SIZE:-1}"
# VM resource overrides — leave empty to use the image defaults.
VM_CPU_COUNT="${VM_CPU_COUNT:-}"   # vCPU cores,   e.g. 4
VM_MEMORY_MB="${VM_MEMORY_MB:-}"   # memory (MiB), e.g. 8192
GITHUB_CANCEL_RUN_ON_SHUTDOWN="${GITHUB_CANCEL_RUN_ON_SHUTDOWN:-true}"
GITHUB_FORCE_CANCEL_RUN_ON_SHUTDOWN="${GITHUB_FORCE_CANCEL_RUN_ON_SHUTDOWN:-false}"
RUNNER_SHUTDOWN_GRACE_SECONDS="${RUNNER_SHUTDOWN_GRACE_SECONDS:-0}"
RUNNER_FORCE_CANCEL_AFTER_SECONDS="${RUNNER_FORCE_CANCEL_AFTER_SECONDS:-30}"
RUNNER_SHUTDOWN_POLL_SECONDS="${RUNNER_SHUTDOWN_POLL_SECONDS:-5}"
# Optional: path on the HOST for the general-purpose build cache (ISOs, toolchain
# archives, and other large build dependencies). When set, the directory is shared
# into each VM as /Volumes/My Shared Files/build-cache/ via VirtioFS so workflows
# can symlink cached files instead of downloading them from Azure Blob Storage.
BUILD_CACHE_DIR="${BUILD_CACHE_DIR:-}"
PREPARE_UTM_AUTOMATION="${PREPARE_UTM_AUTOMATION:-true}"
RUNNER_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
UTM_AUTOMATION_SCRIPT="${UTM_AUTOMATION_SCRIPT:-${RUNNER_SCRIPT_DIR}/prepare-utm-automation.sh}"
# NAT keeps runner VMs off the LAN: no DHCP lease per job and no VM with the
# default SSH password reachable from other machines.
RUNNER_NETWORK="${RUNNER_NETWORK:-nat}"
RUNNER_DNS_SERVERS="${RUNNER_DNS_SERVERS:-}"
RUNNER_DNS_SERVICE="${RUNNER_DNS_SERVICE:-Ethernet}"
RUNNER_FIXED_MAC="${RUNNER_FIXED_MAC:-true}"
RUNNER_MAC_ADDRESSES="${RUNNER_MAC_ADDRESSES:-}"
RUNNER_NETWORK_CHECK="${RUNNER_NETWORK_CHECK:-true}"
RUNNER_NETWORK_CHECK_URLS="${RUNNER_NETWORK_CHECK_URLS:-https://github.com/ https://api.github.com/}"
RUNNER_NETWORK_CHECK_TIMEOUT="${RUNNER_NETWORK_CHECK_TIMEOUT:-90}"
GUEST_NETWORK_SCRIPT="${GUEST_NETWORK_SCRIPT:-${RUNNER_SCRIPT_DIR}/prepare-guest-network.sh}"
TART_VMS_DIR="${TART_HOME:-${HOME}/.tart}/vms"
# ───────────────────────────────────────────────────────────────────────────────

preflight_checks() {
	local cmd
	for cmd in tart gh sshpass; do
		if ! command -v "$cmd" &>/dev/null; then
			echo "ERROR: '$cmd' could not be found."
			case "$cmd" in
				tart)     echo "  Install: brew install tart" ;;
				gh)       echo "  Install: brew install gh" ;;
				sshpass)  echo "  Install: brew install sshpass" ;;
			esac
			exit 1
		fi
	done
	if ! validate_runner_network; then
		exit 1
	fi
	if env_truthy "${RUNNER_FIXED_MAC}" && ! command -v python3 &>/dev/null; then
		echo "ERROR: RUNNER_FIXED_MAC needs python3 on the host to edit the clone's Tart config."
		exit 1
	fi
}

ensure_base_image_exists() {
	if ! tart list | awk 'NR>1 && $1=="local" {print $2}' | grep -qx "${VM_BASE_IMAGE}"; then
		echo "ERROR: Base image '${VM_BASE_IMAGE}' not found."
		echo "  Run './prepare-tart-base.sh' first to build the golden image."
		echo "  Or set VM_BASE_IMAGE=tahoe-base to use the upstream image directly."
		exit 1
	fi
	echo "Base image '${VM_BASE_IMAGE}' present."
}

# ─── SSH helper ────────────────────────────────────────────────────────────────
# Usage: vm_ssh <ip> [ssh-args...]
vm_ssh() {
	(vm_ssh_exec "$@")
}

# Replaces the calling (sub)shell with ssh, so the PID of a background call is
# the ssh client itself and a signal reaches it directly.
vm_ssh_exec() {
	local ip="$1"; shift
	# Keepalives end a session whose VM stopped answering (about one minute).
	# Password only: keys offered from an ssh-agent first can use up the
	# guest's MaxAuthTries before sshpass gets to send the password.
	exec sshpass -p "${VM_SSH_PASS}" ssh \
		-o "StrictHostKeyChecking=no" \
		-o "UserKnownHostsFile=/dev/null" \
		-o "ConnectTimeout=5" \
		-o "PubkeyAuthentication=no" \
		-o "PreferredAuthentications=password,keyboard-interactive" \
		-o "ServerAliveInterval=15" \
		-o "ServerAliveCountMax=4" \
		"${VM_SSH_USER}@${ip}" "$@"
}

# Run a guest script over vm_ssh in the background and wait for it. Bash runs
# a worker's TERM/INT trap only after a foreground command returns, so a
# foreground SSH call would delay pool shutdown; with `wait` the trap fires at
# once and _worker_cleanup terminates the tracked PID.
# Usage: vm_ssh_script <script file> <ip> [ssh-args...]
vm_ssh_script() {
	local script="$1"; shift
	local status=0
	vm_ssh_exec "$@" <"$script" &
	current_prep_ssh_pid=$!
	wait "$current_prep_ssh_pid" || status=$?
	current_prep_ssh_pid=""
	return "$status"
}

run_in_new_session() {
	if command -v setsid >/dev/null 2>&1; then
		exec setsid "$@"
	fi
	if command -v perl >/dev/null 2>&1; then
		exec perl -MPOSIX=setsid -e 'setsid() or die "setsid: $!"; exec @ARGV or die "exec: $!"' -- "$@"
	fi
	exec "$@"
}

# Images built before prepare-tart-base.sh learned about UTM still carry the
# quarantined cask and no Automation grant. Repair every clone before the runner
# registers; the step is idempotent and does not launch UTM. A failure only warns
# because jobs that do not use UTM must keep running.
prepare_utm_automation() {
	local label="$1" ip="$2"
	if ! env_truthy "${PREPARE_UTM_AUTOMATION}"; then
		return 0
	fi
	echo "[${label}] Preparing UTM for unattended AppleScript..."
	# SSH right after boot occasionally times out in the banner exchange or
	# rejects the correct password for a few seconds, even though the guest
	# network check just logged in; the guest script is idempotent, so retry a
	# few times.
	local attempt
	for attempt in 1 2 3; do
		if vm_ssh_script "${UTM_AUTOMATION_SCRIPT}" "$ip" /bin/bash -s; then
			return 0
		fi
		[[ "$attempt" -lt 3 ]] && sleep 5
	done
	echo "[${label}] WARNING: UTM preparation failed; UTM AppleScript steps may hang on GUI prompts." >&2
	return 0
}

# ─── Runner VM networking ──────────────────────────────────────────────────────
validate_runner_network() {
	case "${RUNNER_NETWORK}" in
		nat) ;;
		bridged:?*) ;;
		*)
			echo "ERROR: RUNNER_NETWORK must be 'nat' or 'bridged:<interface>', got '${RUNNER_NETWORK}'." >&2
			return 1
			;;
	esac
	if [[ ! "${RUNNER_NETWORK_CHECK_TIMEOUT}" =~ ^[0-9]+$ ]]; then
		echo "ERROR: RUNNER_NETWORK_CHECK_TIMEOUT must be whole seconds, got '${RUNNER_NETWORK_CHECK_TIMEOUT}'." >&2
		return 1
	fi
}

# Prints the `tart run` network flag (none for NAT).
tart_network_args() {
	case "${RUNNER_NETWORK}" in
		bridged:*) printf '%s\n' "--net-bridged=${RUNNER_NETWORK#bridged:}" ;;
	esac
}

# Prints the `tart ip` resolver flag. ARP only sees bridged guests; NAT guests
# are found through the host's DHCP leases (Tart's default).
tart_ip_args() {
	case "${RUNNER_NETWORK}" in
		bridged:*) printf '%s\n' "--resolver=arp" ;;
	esac
}

host_default_router() {
	route -n get default 2>/dev/null | awk '/gateway:/ {print $2; exit}'
}

# Prints the guest DNS setting for prepare-guest-network.sh: "keep", "dhcp",
# or space-separated resolver IPs. "router" resolves on every cycle, so a host
# that moves between networks keeps working.
resolve_guest_dns() {
	local setting="${RUNNER_DNS_SERVERS}" router
	if [[ -z "${setting}" ]]; then
		case "${RUNNER_NETWORK}" in
			nat) setting="router" ;;
			*) setting="keep" ;;
		esac
	fi
	case "${setting}" in
		keep|dhcp) printf '%s\n' "${setting}" ;;
		router)
			router="$(host_default_router)"
			if [[ ! "${router}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
				echo "ERROR: could not determine the host's default IPv4 router for guest DNS (got '${router}')." >&2
				return 1
			fi
			printf '%s\n' "${router}"
			;;
		*) printf '%s\n' "${setting//,/ }" ;;
	esac
}

# Stable, locally administered unicast MAC for a worker slot. Derived from the
# host name so two runner hosts on one network do not collide.
worker_mac_address() {
	local worker_id="$1" mac="" hash
	local -a macs
	if [[ -n "${RUNNER_MAC_ADDRESSES}" ]]; then
		IFS=',' read -r -a macs <<<"${RUNNER_MAC_ADDRESSES}"
		mac="${macs[$((worker_id - 1))]:-}"
		mac="$(printf '%s' "${mac}" | tr -d ' ' | tr 'A-F' 'a-f')"
	else
		hash="$(printf '%s-w%s' "$(hostname -s)" "${worker_id}" | { shasum -a 256 2>/dev/null || sha256sum; } | cut -c1-8)"
		mac="$(printf '02:%s:%s:%s:%s:%02x' "${hash:0:2}" "${hash:2:2}" "${hash:4:2}" "${hash:6:2}" "$((worker_id % 256))")"
	fi
	if [[ ! "${mac}" =~ ^[0-9a-f][02468ace](:[0-9a-f]{2}){5}$ ]]; then
		echo "ERROR: no valid unicast MAC for worker ${worker_id} (got '${mac}')." >&2
		return 1
	fi
	printf '%s\n' "${mac}"
}

# Name of another running local VM that already uses MAC $1, if any.
running_vm_with_mac() {
	local mac="$1" exclude="$2" config name
	for config in "${TART_VMS_DIR}"/*/config.json; do
		[[ -f "${config}" ]] || continue
		name="$(basename "$(dirname "${config}")")"
		[[ "${name}" == "${exclude}" ]] && continue
		grep -qi "\"macAddress\"[[:space:]]*:[[:space:]]*\"${mac}\"" "${config}" || continue
		if tart list | awk -v n="${name}" '$1=="local" && $2==n {print $NF}' | grep -qx running; then
			printf '%s\n' "${name}"
			return 0
		fi
	done
	return 1
}

# Give the fresh clone its slot's MAC so the DHCP server (LAN when bridged,
# the host's vmnet DHCP with NAT) reuses one lease per slot instead of one per
# job. Falls back to Tart's random MAC when the fixed one is still taken, e.g.
# by a VM that shutdown kept alive under an active job.
apply_worker_mac() {
	local label="$1" vm_name="$2" worker_id="$3" mac holder config
	env_truthy "${RUNNER_FIXED_MAC}" || return 0
	mac="$(worker_mac_address "${worker_id}")" || return 1
	if holder="$(running_vm_with_mac "${mac}" "${vm_name}")"; then
		echo "[${label}] WARNING: MAC ${mac} is in use by running VM '${holder}'; keeping a random MAC for this cycle." >&2
		return 0
	fi
	config="${TART_VMS_DIR}/${vm_name}/config.json"
	if ! python3 - "${config}" "${mac}" <<'PY'
import json, os, sys
path, mac = sys.argv[1], sys.argv[2]
with open(path) as handle:
    config = json.load(handle)
config['macAddress'] = mac
tmp = path + '.tmp'
with open(tmp, 'w') as handle:
    json.dump(config, handle, indent=2)
os.replace(tmp, path)
PY
	then
		echo "[${label}] ERROR: could not set MAC ${mac} in ${config}." >&2
		return 1
	fi
	echo "[${label}] Using fixed MAC ${mac} for this slot."
}

# Configure guest DNS and prove DNS + HTTPS from inside the guest before the
# runner registers, so a broken network never takes a job.
prepare_guest_network() {
	local label="$1" ip="$2" dns url
	local -a args
	dns="$(resolve_guest_dns)" || return 1
	args=(--dns "${dns}" --service "${RUNNER_DNS_SERVICE}" --timeout "${RUNNER_NETWORK_CHECK_TIMEOUT}")
	if env_truthy "${RUNNER_NETWORK_CHECK}"; then
		for url in ${RUNNER_NETWORK_CHECK_URLS}; do
			args+=(--url "${url}")
		done
	elif [[ "${dns}" == "keep" ]]; then
		return 0
	fi
	echo "[${label}] Preparing guest network (DNS: ${dns})..."
	# Exit 255 is ssh itself (e.g. a login hiccup right after boot); retry
	# that. Any other failure comes from the guest check and is final.
	local attempt status
	for attempt in 1 2 3; do
		status=0
		vm_ssh_script "${GUEST_NETWORK_SCRIPT}" "$ip" /bin/bash -s -- "$(printf '%q ' "${args[@]}")" || status=$?
		[[ "$status" -ne 255 ]] && return "$status"
		[[ "$attempt" -lt 3 ]] && sleep 5
	done
	return "$status"
}

# ─── GitHub Actions shutdown helpers ──────────────────────────────────────────
env_truthy() {
	case "$1" in
		1|true|TRUE|yes|YES|on|ON) return 0 ;;
		*) return 1 ;;
	esac
}

shell_number_or_default() {
	local value="$1"
	local default_value="$2"
	case "$value" in
		''|*[!0-9]*) printf '%s\n' "$default_value" ;;
		*) printf '%s\n' "$value" ;;
	esac
}

shell_number_at_least_or_default() {
	local value="$1"
	local default_value="$2"
	local minimum_value="$3"

	value="$(shell_number_or_default "$value" "$default_value")"
	if (( 10#$value < 10#$minimum_value )); then
		printf '%s\n' "$default_value"
		return
	fi

	printf '%s\n' "$value"
}

normalize_shutdown_settings() {
	RUNNER_SHUTDOWN_GRACE_SECONDS="$(shell_number_or_default "$RUNNER_SHUTDOWN_GRACE_SECONDS" 0)"
	RUNNER_FORCE_CANCEL_AFTER_SECONDS="$(shell_number_or_default "$RUNNER_FORCE_CANCEL_AFTER_SECONDS" 30)"
	RUNNER_SHUTDOWN_POLL_SECONDS="$(shell_number_at_least_or_default "$RUNNER_SHUTDOWN_POLL_SECONDS" 5 1)"
}

normalize_shutdown_settings

escape_jq_string() {
	local value="$1"
	value=${value//\\/\\\\}
	value=${value//\"/\\\"}
	printf '%s' "$value"
}

runner_list_api_path() {
	if [[ "$GITHUB_SCOPE" == "org" ]]; then
		printf '/orgs/%s/actions/runners\n' "$GITHUB_ORG"
	else
		printf '/repos/%s/actions/runners\n' "$GITHUB_REPO"
	fi
}

runner_delete_api_path() {
	local runner_id="$1"
	if [[ "$GITHUB_SCOPE" == "org" ]]; then
		printf '/orgs/%s/actions/runners/%s\n' "$GITHUB_ORG" "$runner_id"
	else
		printf '/repos/%s/actions/runners/%s\n' "$GITHUB_REPO" "$runner_id"
	fi
}

log_gh_api_failure() {
	local context="$1"
	local status="$2"
	local error="$3"

	error=${error//$'\n'/ }
	error=${error//$'\r'/ }
	error=$(printf '%s' "$error" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')

	if [[ -n "$error" ]]; then
		echo "Warning: GitHub API ${context} failed (exit ${status}): ${error}" >&2
	else
		echo "Warning: GitHub API ${context} failed (exit ${status})." >&2
	fi
}

# Lookup helpers print matching records to stdout and return:
# 0 = found, 1 = not found, 2 = GitHub API failure / unknown state.
find_runner_info() {
	local runner_name="$1"
	local runner_name_jq runner_lines api_err_file api_status api_error first_runner_line
	runner_name_jq=$(escape_jq_string "$runner_name")

	api_err_file=$(mktemp "${TMPDIR:-/tmp}/gh-api-runner-list.XXXXXX")
	if runner_lines=$(gh api "$(runner_list_api_path)" --paginate \
		--jq ".runners[] | select(.name == \"${runner_name_jq}\") | [.id, .busy, .status] | @tsv" \
		2>"$api_err_file"); then
		rm -f "$api_err_file"
	else
		api_status=$?
		api_error=$(<"$api_err_file")
		rm -f "$api_err_file"
		log_gh_api_failure "runner lookup for '${runner_name}'" "$api_status" "$api_error"
		return 2
	fi

	if [[ -z "$runner_lines" ]]; then
		return 1
	fi

	IFS= read -r first_runner_line <<< "$runner_lines" || true
	printf '%s\n' "$first_runner_line"
	return 0
}

find_active_runner_job() {
	local runner_name="$1"
	if [[ "$GITHUB_SCOPE" != "repo" ]]; then
		return 1
	fi
	if [[ -z "$GITHUB_REPO" ]]; then
		return 1
	fi

	local runner_name_jq run_ids run_id job_lines job_line api_err_file api_status api_error
	runner_name_jq=$(escape_jq_string "$runner_name")

	api_err_file=$(mktemp "${TMPDIR:-/tmp}/gh-api-run-list.XXXXXX")
	if run_ids=$(gh api "/repos/${GITHUB_REPO}/actions/runs?status=in_progress&per_page=100" --paginate \
		--jq '.workflow_runs[].id' 2>"$api_err_file"); then
		rm -f "$api_err_file"
	else
		api_status=$?
		api_error=$(<"$api_err_file")
		rm -f "$api_err_file"
		log_gh_api_failure "workflow run lookup for runner '${runner_name}'" "$api_status" "$api_error"
		return 2
	fi
	[[ -z "$run_ids" ]] && return 1

	while IFS= read -r run_id; do
		[[ -z "$run_id" ]] && continue

		api_err_file=$(mktemp "${TMPDIR:-/tmp}/gh-api-job-list.XXXXXX")
		if job_lines=$(gh api "/repos/${GITHUB_REPO}/actions/runs/${run_id}/jobs?filter=latest&per_page=100" --paginate \
			--jq ".jobs[] | select(.status == \"in_progress\" and .runner_name == \"${runner_name_jq}\") | [(.run_id // ${run_id}), .id, .name, .html_url] | @tsv" \
			2>"$api_err_file"); then
			rm -f "$api_err_file"
		else
			api_status=$?
			api_error=$(<"$api_err_file")
			rm -f "$api_err_file"
			log_gh_api_failure "job lookup for workflow run ${run_id}" "$api_status" "$api_error"
			return 2
		fi

		IFS= read -r job_line <<< "$job_lines" || true
		if [[ -n "$job_line" ]]; then
			printf '%s\n' "$job_line"
			return 0
		fi
	done <<< "$run_ids"

	return 1
}

REQUESTED_CANCEL_RUN_ID=""

cancel_active_runner_job() {
	local vm="$1"
	local runner_name="$2"
	REQUESTED_CANCEL_RUN_ID=""

	if ! env_truthy "$GITHUB_CANCEL_RUN_ON_SHUTDOWN"; then
		echo "[${vm}] Active workflow cancellation disabled by GITHUB_CANCEL_RUN_ON_SHUTDOWN=${GITHUB_CANCEL_RUN_ON_SHUTDOWN}."
		return 1
	fi
	if [[ "$GITHUB_SCOPE" != "repo" ]]; then
		echo "[${vm}] Warning: automatic workflow cancellation currently requires GITHUB_SCOPE=repo."
		echo "[${vm}]          Set GITHUB_REPO to the repository that owns the job for clean Ctrl+C cancellation."
		return 1
	fi

	local job_line run_id job_id job_name job_url
	if job_line=$(find_active_runner_job "$runner_name"); then
		:
	else
		case "$?" in
			1)
				echo "[${vm}] No in-progress GitHub Actions job found for runner '${runner_name}'."
				;;
			*)
				echo "[${vm}] Warning: could not determine whether runner '${runner_name}' has an active job."
				;;
		esac
		return 1
	fi

	IFS=$'\t' read -r run_id job_id job_name job_url <<< "$job_line"
	REQUESTED_CANCEL_RUN_ID="$run_id"

	echo "[${vm}] Canceling workflow run ${run_id} for active job ${job_id} (${job_name})..."
	if gh api --method POST -H "Accept: application/vnd.github+json" \
		"/repos/${GITHUB_REPO}/actions/runs/${run_id}/cancel" >/dev/null 2>&1; then
		echo "[${vm}] Cancellation requested: ${job_url}"
		return 0
	fi

	echo "[${vm}] Warning: GitHub did not accept cancellation for workflow run ${run_id}."
	return 1
}

force_cancel_workflow_run() {
	local vm="$1"
	local run_id="$2"
	local api_err_file api_status api_error

	[[ -z "$run_id" ]] && return 1
	if ! env_truthy "$GITHUB_FORCE_CANCEL_RUN_ON_SHUTDOWN"; then
		return 1
	fi
	if [[ "$GITHUB_SCOPE" != "repo" ]]; then
		return 1
	fi

	echo "[${vm}] Workflow run ${run_id} is still active; requesting force-cancel..."
	api_err_file=$(mktemp "${TMPDIR:-/tmp}/gh-api-force-cancel.XXXXXX")
	if gh api --method POST -H "Accept: application/vnd.github+json" \
		"/repos/${GITHUB_REPO}/actions/runs/${run_id}/force-cancel" >/dev/null 2>"$api_err_file"; then
		rm -f "$api_err_file"
		echo "[${vm}] Force-cancel requested for workflow run ${run_id}."
		return 0
	else
		api_status=$?
	fi

	api_error=$(<"$api_err_file")
	rm -f "$api_err_file"
	log_gh_api_failure "force-cancel for workflow run ${run_id}" "$api_status" "$api_error"
	echo "[${vm}] Warning: force-cancel for workflow run ${run_id} was not accepted; manual cancellation may be required." >&2
	return 1
}

signal_runner_processes() {
	local vm="$1"
	local vm_ip="$2"
	[[ -z "$vm_ip" ]] && return

	echo "[${vm}] Sending SIGINT to runner process inside VM..."
	if ! vm_ssh "$vm_ip" bash -s -- "$RUNNER_DIR" <<'EOF' >/dev/null 2>&1; then
set +e
runner_dir="$1"
for pattern in \
	"${runner_dir}/bin/Runner.Listener" \
	"${runner_dir}/bin/Runner.Worker" \
	"${runner_dir}/run.sh"; do
	pkill -INT -f "$pattern" 2>/dev/null || true
done
EOF
		echo "[${vm}] Warning: could not signal runner process over SSH; continuing with GitHub-side cancellation."
	fi
}

wait_for_runner_to_settle() {
	local vm="$1"
	local runner_name="$2"
	local run_id="$3"
	local start_ts now deadline force_after_ts forced runner_state_unknown runner_info runner_id runner_busy runner_status
	local has_shutdown_deadline=false

	normalize_shutdown_settings

	start_ts=$(date +%s)
	if [[ "$RUNNER_SHUTDOWN_GRACE_SECONDS" -gt 0 ]]; then
		has_shutdown_deadline=true
		deadline=$(( start_ts + RUNNER_SHUTDOWN_GRACE_SECONDS ))
	else
		deadline=0
	fi
	force_after_ts=$(( start_ts + RUNNER_FORCE_CANCEL_AFTER_SECONDS ))
	forced=false
	runner_state_unknown=false

	while true; do
		now=$(date +%s)
		if runner_info=$(find_runner_info "$runner_name"); then
			runner_state_unknown=false
			IFS=$'\t' read -r runner_id runner_busy runner_status <<< "$runner_info"
			if [[ "$runner_busy" != "true" ]]; then
				echo "[${vm}] Runner '${runner_name}' is no longer busy (status: ${runner_status})."
				return 0
			fi

			if [[ -z "$run_id" && "$GITHUB_SCOPE" == "repo" ]] && env_truthy "$GITHUB_CANCEL_RUN_ON_SHUTDOWN"; then
				echo "[${vm}] Runner '${runner_name}' is busy; retrying active job lookup before teardown..."
				if cancel_active_runner_job "$vm" "$runner_name"; then
					run_id="$REQUESTED_CANCEL_RUN_ID"
				fi
			fi
		else
			case "$?" in
				1)
					echo "[${vm}] Runner '${runner_name}' is no longer registered."
					return 0
					;;
				*)
					runner_state_unknown=true
					echo "[${vm}] Runner '${runner_name}' state is unknown; continuing to wait for GitHub API recovery."
					;;
			esac
		fi

		if [[ "$forced" != "true" && -n "$run_id" && "$RUNNER_FORCE_CANCEL_AFTER_SECONDS" -gt 0 && "$now" -ge "$force_after_ts" ]]; then
			force_cancel_workflow_run "$vm" "$run_id" || true
			forced=true
		fi

		if [[ "$has_shutdown_deadline" == "true" && "$now" -ge "$deadline" ]]; then
			if [[ "$runner_state_unknown" == "true" ]]; then
				echo "[${vm}] Warning: could not confirm runner '${runner_name}' state after ${RUNNER_SHUTDOWN_GRACE_SECONDS}s."
			else
				echo "[${vm}] Warning: runner '${runner_name}' is still busy after ${RUNNER_SHUTDOWN_GRACE_SECONDS}s."
			fi
			return 1
		fi

		sleep "$RUNNER_SHUTDOWN_POLL_SECONDS"
	done
}

# ─── VM cleanup ────────────────────────────────────────────────────────────────
# Usage: cleanup_vm <vm-name> [runner-name] [vm-ip] [cancel-active-job]
cleanup_vm() {
	local vm="$1"
	local runner_name="${2:-}"
	local vm_ip="${3:-}"
	local cancel_active_job="${4:-false}"
	local preserve_busy_vm=false
	[[ -z "$vm" ]] && return

	# Deregister the runner before stopping the VM (handles Ctrl+C interruption).
	# Ephemeral runners deregister themselves when run.sh receives SIGINT, so we
	# wait briefly and only attempt an explicit removal if the runner is still listed.
	if [[ -n "$runner_name" ]]; then
		if [[ "$cancel_active_job" == "true" ]]; then
			signal_runner_processes "$vm" "$vm_ip"
			cancel_active_runner_job "$vm" "$runner_name" || true
			wait_for_runner_to_settle "$vm" "$runner_name" "$REQUESTED_CANCEL_RUN_ID" || true
		else
			# Give run.sh a moment to finish its own graceful shutdown / self-deregistration.
			sleep 3
		fi

		echo "[${vm}] Deregistering runner '${runner_name}'..."

		# Use the GitHub API directly via the local gh CLI as the last-resort removal
		# path if the ephemeral runner did not already deregister itself.
		local runner_info runner_id runner_busy runner_status
		if runner_info=$(find_runner_info "$runner_name"); then
			IFS=$'\t' read -r runner_id runner_busy runner_status <<< "$runner_info"

			if [[ "$runner_busy" == "true" ]]; then
				echo "[${vm}] Warning: runner '${runner_name}' is still busy; skipping GitHub runner deletion."
				if [[ "$cancel_active_job" == "true" ]]; then
					preserve_busy_vm=true
				fi
			elif [[ -n "$runner_id" ]]; then
				local api_path
				api_path=$(runner_delete_api_path "$runner_id")
				if gh api --method DELETE "$api_path" 2>/dev/null; then
					echo "[${vm}] Runner '${runner_name}' deregistered via GitHub API."
				else
					echo "[${vm}] Warning: Could not deregister runner '${runner_name}'; manual cleanup may be required."
				fi
			fi
		else
			case "$?" in
				1)
					echo "[${vm}] Runner '${runner_name}' not found; likely already self-deregistered."
					;;
				*)
					echo "[${vm}] Warning: Could not verify runner '${runner_name}' registration state; manual cleanup may be required."
					if [[ "$cancel_active_job" == "true" ]]; then
						preserve_busy_vm=true
					fi
					;;
			esac
		fi
	fi

	if [[ "$preserve_busy_vm" == "true" ]]; then
		echo "[${vm}] Warning: leaving VM running because GitHub still reports runner '${runner_name}' as busy or unknown."
		echo "[${vm}]          Cancel the workflow run or wait for it to finish, then remove the runner/VM manually."
		return 1
	fi

	echo "[${vm}] Stopping VM..."
	tart stop "${vm}" 2>/dev/null || true
	if [[ "$VM_CLONE_PER_RUN" == "true" && "$vm" != "$VM_BASE_IMAGE" ]]; then
		echo "[${vm}] Deleting ephemeral VM clone..."
		tart delete "${vm}" 2>/dev/null || true
	fi
}

wait_for_local_process_exit() {
	local pid="$1"
	local timeout_seconds="${2:-10}"
	local waited_seconds=0

	[[ -z "$pid" ]] && return 0
	while kill -0 "$pid" 2>/dev/null; do
		if [[ "$waited_seconds" -ge "$timeout_seconds" ]]; then
			kill -TERM "$pid" 2>/dev/null || true
			sleep 1
			kill -KILL "$pid" 2>/dev/null || true
			break
		fi
		sleep 1
		waited_seconds=$(( waited_seconds + 1 ))
	done
	wait "$pid" 2>/dev/null || true
}

# ─── Single worker (runs one runner cycle at a time, loops until MAX_RUNS) ─────
run_worker() {
	local worker_id="$1"
	local current_vm_name=""
	local current_runner_name=""
	local current_vm_ip=""
	local current_runner_ssh_pid=""
	local current_tart_run_pid=""
	local current_prep_ssh_pid=""
	local shutdown_requested=false
	local run_count=0

	# shellcheck disable=SC2317,SC2329 # Invoked by the EXIT trap.
	_worker_cleanup() {
		if [[ -n "$current_prep_ssh_pid" ]]; then
			kill -TERM "$current_prep_ssh_pid" 2>/dev/null || true
			wait "$current_prep_ssh_pid" 2>/dev/null || true
			current_prep_ssh_pid=""
		fi
		[[ -z "$current_vm_name" ]] && return
		local cancel_active_job=false
		local cleanup_status=0
		[[ "$shutdown_requested" == "true" ]] && cancel_active_job=true
		cleanup_vm "$current_vm_name" "$current_runner_name" "$current_vm_ip" "$cancel_active_job" || cleanup_status=$?
		if [[ -n "$current_runner_ssh_pid" && "$cleanup_status" -eq 0 ]]; then
			kill -TERM "$current_runner_ssh_pid" 2>/dev/null || true
			wait "$current_runner_ssh_pid" 2>/dev/null || true
		fi
		if [[ -n "$current_runner_ssh_pid" && "$cleanup_status" -ne 0 ]]; then
			disown "$current_runner_ssh_pid" 2>/dev/null || true
		fi
		if [[ -n "$current_tart_run_pid" && "$cleanup_status" -eq 0 ]]; then
			wait_for_local_process_exit "$current_tart_run_pid"
		fi
		if [[ -n "$current_tart_run_pid" && "$cleanup_status" -ne 0 ]]; then
			disown "$current_tart_run_pid" 2>/dev/null || true
		fi
		current_vm_name=""
		current_runner_name=""
		current_vm_ip=""
		current_runner_ssh_pid=""
		current_tart_run_pid=""
	}
	# EXIT runs the actual cleanup.
	# INT/TERM call exit so the EXIT trap fires and the worker subshell terminates;
	# without an explicit exit here, set +e causes the loop to continue after Ctrl+C.
	trap '_worker_cleanup' EXIT
	trap 'shutdown_requested=true; exit 130' INT
	trap 'shutdown_requested=true; exit 143' TERM

	# Disable set -e within the worker so that a single cycle failure (e.g. an SSH
	# authentication error, a failed clone, or a dropped VM) does not kill the
	# entire worker process.  Each critical step checks its own exit code and uses
	# `continue` to restart the cycle cleanly after VM cleanup.
	set +e

	while true; do
		run_count=$(( run_count + 1 ))
		local timestamp
		timestamp=$(date +%s)
		local runner_name="${RUNNER_NAME_BASE}-w${worker_id}-${timestamp}"

		echo ""
		echo "════════════════════════════════════════════════════════════"
		echo " Worker #${worker_id} | Cycle #${run_count} | Runner: ${runner_name}"
		echo "════════════════════════════════════════════════════════════"

		# ── Determine VM name for this cycle ────────────────────────
		local vm_name
		if [[ "$VM_CLONE_PER_RUN" == "true" ]]; then
			vm_name="runner-w${worker_id}-${timestamp}"
			echo "[worker-${worker_id}] Cloning '${VM_BASE_IMAGE}' → '${vm_name}'..."
			if ! tart clone "${VM_BASE_IMAGE}" "${vm_name}"; then
				echo "[worker-${worker_id}] ERROR: tart clone failed. Retrying in 15s..."
				sleep 15
				continue
			fi
		else
			vm_name="${VM_BASE_IMAGE}"
		fi
		current_vm_name="$vm_name"

		if [[ "$VM_CLONE_PER_RUN" == "true" ]] && ! apply_worker_mac "worker-${worker_id}" "${vm_name}" "${worker_id}"; then
			echo "[worker-${worker_id}] ERROR: Could not assign the slot MAC. Cleaning up and retrying in 15s..."
			cleanup_vm "$current_vm_name"
			current_vm_name=""
			sleep 15
			continue
		fi

		# ── Apply CPU / memory overrides ─────────────────────────────
		if [[ -n "$VM_CPU_COUNT" || -n "$VM_MEMORY_MB" ]]; then
			local set_args=()
			[[ -n "$VM_CPU_COUNT" ]] && set_args+=(--cpu "$VM_CPU_COUNT")
			[[ -n "$VM_MEMORY_MB" ]] && set_args+=(--memory "$VM_MEMORY_MB")
			echo "[worker-${worker_id}] Applying VM resource overrides: ${set_args[*]}"
			tart set "${vm_name}" "${set_args[@]}"
		fi

		# ── Fetch a fresh registration token (valid 1 hour) ─────────
		echo "[worker-${worker_id}] Fetching runner registration token..."
		local runner_token runner_registration_url
		if [[ "$GITHUB_SCOPE" == "org" ]]; then
			if [[ -z "$GITHUB_ORG" ]]; then
				echo "GITHUB_ORG must be set when GITHUB_SCOPE=org"
				exit 1
			fi
			runner_token=$(gh api \
				--method POST \
				-H "Accept: application/vnd.github+json" \
				"/orgs/${GITHUB_ORG}/actions/runners/registration-token" \
				--jq '.token')
			runner_registration_url="https://github.com/${GITHUB_ORG}"
		else
			if [[ -z "$GITHUB_REPO" ]]; then
				echo "GITHUB_REPO must be set (format: owner/repo)"
				exit 1
			fi
			runner_token=$(gh api \
				--method POST \
				-H "Accept: application/vnd.github+json" \
				"/repos/${GITHUB_REPO}/actions/runners/registration-token" \
				--jq '.token')
			runner_registration_url="https://github.com/${GITHUB_REPO}"
		fi
		if [[ -z "$runner_token" ]]; then
			echo "[worker-${worker_id}] ERROR: Failed to obtain registration token. Cleaning up and retrying in 30s..."
			cleanup_vm "$current_vm_name"
			current_vm_name=""
			sleep 30
			continue
		fi
		echo "[worker-${worker_id}] Registration token obtained."

		# ── Start VM ────────────────────────────────────────────────
		# Networking follows RUNNER_NETWORK (NAT by default). With bridged networking
		# the VM's SSH port and its default password are reachable from the LAN.
		echo "[worker-${worker_id}] Starting VM '${vm_name}'..."
		# macOS ships Bash 3.2 (no mapfile); each helper prints at most one flag.
		local tart_dir_flag=() tart_net_flag=() tart_ip_flag=() flag
		flag="$(tart_network_args)"; [[ -n "$flag" ]] && tart_net_flag=("$flag")
		flag="$(tart_ip_args)"; [[ -n "$flag" ]] && tart_ip_flag=("$flag")
		if [[ -n "$BUILD_CACHE_DIR" ]]; then
			echo "[worker-${worker_id}] Sharing build cache '${BUILD_CACHE_DIR}' → /Volumes/My Shared Files/build-cache/ inside VM"
			tart_dir_flag=("--dir=build-cache:${BUILD_CACHE_DIR}")
		fi
		(
			trap '' INT HUP
			run_in_new_session tart run --no-graphics ${tart_net_flag[@]+"${tart_net_flag[@]}"} ${tart_dir_flag[@]+"${tart_dir_flag[@]}"} "${vm_name}"
		) &
		current_tart_run_pid=$!

		# ── Wait for an IP (90 s timeout) ───────────────────────────
		current_vm_ip=""
		local ip_attempts=0
		while [[ -z "$current_vm_ip" ]]; do
			current_vm_ip=$(tart ip ${tart_ip_flag[@]+"${tart_ip_flag[@]}"} "${vm_name}" 2>/dev/null || true)
			if [[ -z "$current_vm_ip" ]]; then
				ip_attempts=$(( ip_attempts + 1 ))
				if [[ $ip_attempts -ge 90 ]]; then
					echo "[worker-${worker_id}] ERROR: Timed out waiting for VM IP after ${ip_attempts}s. Cleaning up and retrying..."
					break
				fi
				sleep 1
			fi
		done
		if [[ -z "$current_vm_ip" ]]; then
			cleanup_vm "$current_vm_name" "$current_runner_name"
			if [[ -n "$current_tart_run_pid" ]]; then
				wait_for_local_process_exit "$current_tart_run_pid"
			fi
			current_vm_name=""
			current_runner_name=""
			current_tart_run_pid=""
			continue
		fi
		echo "[worker-${worker_id}] VM IP: $current_vm_ip"

		# ── Wait for SSH (60 s timeout) ──────────────────────────────
		local ssh_attempts=0
		until vm_ssh "$current_vm_ip" "true" 2>/dev/null; do
			echo "[worker-${worker_id}] Waiting for SSH to become available..."
			ssh_attempts=$(( ssh_attempts + 1 ))
			if [[ $ssh_attempts -ge 30 ]]; then
				echo "[worker-${worker_id}] ERROR: Timed out waiting for SSH after $(( ssh_attempts * 2 ))s. Cleaning up and retrying..."
				current_vm_ip=""
				break
			fi
			sleep 2
		done
		if [[ -z "$current_vm_ip" ]]; then
			cleanup_vm "$current_vm_name" "$current_runner_name"
			if [[ -n "$current_tart_run_pid" ]]; then
				wait_for_local_process_exit "$current_tart_run_pid"
			fi
			current_vm_name=""
			current_runner_name=""
			current_tart_run_pid=""
			continue
		fi
		echo "[worker-${worker_id}] SSH connection successful."
		if ! prepare_guest_network "worker-${worker_id}" "$current_vm_ip"; then
			echo "[worker-${worker_id}] ERROR: Guest network is not ready. Cleaning up and retrying in 30s..."
			cleanup_vm "$current_vm_name" "$current_runner_name"
			if [[ -n "$current_tart_run_pid" ]]; then
				wait_for_local_process_exit "$current_tart_run_pid"
			fi
			current_vm_name=""
			current_runner_name=""
			current_vm_ip=""
			current_tart_run_pid=""
			sleep 30
			continue
		fi
		prepare_utm_automation "worker-${worker_id}" "$current_vm_ip"

		# ── Configure and run the runner ────────────────────────────
		# The runner binary is pre-installed in the golden image by prepare-tart-base.sh.
		# The tracked SSH process blocks this cycle until the ephemeral runner picks
		# up a job, completes it, and deregisters itself. Keeping it as a background
		# process lets TERM/Ctrl+C interrupt wait and run the cleanup trap promptly.
		current_runner_name="$runner_name"
		echo "[worker-${worker_id}] Configuring and starting GitHub Actions runner '${runner_name}'..."

		local runner_exit=0
		(
			trap '' INT HUP
			run_in_new_session sshpass -p "${VM_SSH_PASS}" ssh \
				-o "StrictHostKeyChecking=no" \
				-o "UserKnownHostsFile=/dev/null" \
				-o "ConnectTimeout=5" \
				"${VM_SSH_USER}@${current_vm_ip}" bash <<EOF
set -e

cd "${RUNNER_DIR}"

echo "Configuring runner..."
./config.sh \
	--url "${runner_registration_url}" \
	--token "${runner_token}" \
	--name "${runner_name}" \
	--labels "${RUNNER_LABELS}" \
	--ephemeral \
	--unattended

# Inject PATH into the runner .env so every job inherits Homebrew binaries.
# The runner process is a non-login shell and never sources ~/.zprofile or
# /etc/paths.d – writing to .env is the supported way to set env vars for
# self-hosted runners.
echo 'PATH=/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/go/bin:/Users/admin/go/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin' >> .env
echo 'GOPATH=/Users/admin/go' >> .env

echo "Runner configured. Waiting for a job (./run.sh)..."
./run.sh
echo "Runner finished."
EOF
		) &
		current_runner_ssh_pid=$!
		wait "$current_runner_ssh_pid" || runner_exit=$?
		current_runner_ssh_pid=""

		if [[ $runner_exit -ne 0 ]]; then
			echo "[worker-${worker_id}] ERROR: Runner SSH session failed (exit ${runner_exit}). Cleaning up and retrying in 10s..."
			local cleanup_status=0
			cleanup_vm "$current_vm_name" "$current_runner_name" "$current_vm_ip" "true" || cleanup_status=$?
			if [[ -n "$current_tart_run_pid" && "$cleanup_status" -eq 0 ]]; then
				wait_for_local_process_exit "$current_tart_run_pid"
			fi
			if [[ -n "$current_tart_run_pid" && "$cleanup_status" -ne 0 ]]; then
				disown "$current_tart_run_pid" 2>/dev/null || true
			fi
			current_vm_name=""
			current_runner_name=""
			current_vm_ip=""
			current_tart_run_pid=""
			sleep 10
			continue
		fi

		echo "[worker-${worker_id}] Runner '${runner_name}' completed its job and deregistered."

		# ── Shut down VM ─────────────────────────────────────────────
		cleanup_vm "$current_vm_name" "$current_runner_name"
		if [[ -n "$current_tart_run_pid" ]]; then
			wait_for_local_process_exit "$current_tart_run_pid"
		fi
		current_vm_name=""
		current_runner_name=""
		current_vm_ip=""
		current_tart_run_pid=""

		# ── Check cycle limit ────────────────────────────────────────
		if [[ "$MAX_RUNS" -gt 0 && "$run_count" -ge "$MAX_RUNS" ]]; then
			echo "[worker-${worker_id}] Reached MAX_RUNS=${MAX_RUNS}. Worker exiting."
			break
		fi

		echo "[worker-${worker_id}] Restarting runner cycle in 3 seconds..."
		sleep 3
	done

	echo "[worker-${worker_id}] Worker exited after ${run_count} run(s)."
}

cleanup_all_workers() {
	if [[ "$WORKER_CLEANUP_STARTED" == "true" ]]; then
		return
	fi
	WORKER_CLEANUP_STARTED=true
	[[ ${#WORKER_PIDS[@]} -eq 0 ]] && return

	echo "Shutting down all workers..."
	for pid in "${WORKER_PIDS[@]}"; do
		kill -TERM "$pid" 2>/dev/null || true
	done
	wait "${WORKER_PIDS[@]}" 2>/dev/null || true
	WORKER_PIDS=()
}

handle_parent_signal() {
	local exit_code="$1"
	cleanup_all_workers
	exit "$exit_code"
}

main() {
	preflight_checks
	ensure_base_image_exists

	# ─── Launch parallel workers ───────────────────────────────────────────
	if [[ "$VM_CLONE_PER_RUN" != "true" && "$RUNNER_POOL_SIZE" -gt 1 ]]; then
		echo "ERROR: VM_CLONE_PER_RUN=false with RUNNER_POOL_SIZE>1 — all workers would share"
		echo "       the same base VM, causing conflicts. Set VM_CLONE_PER_RUN=true."
		exit 1
	fi

	WORKER_PIDS=()
	WORKER_CLEANUP_STARTED=false

	trap 'cleanup_all_workers' EXIT
	trap 'handle_parent_signal 130' INT
	trap 'handle_parent_signal 143' TERM

	echo "Starting ${RUNNER_POOL_SIZE} parallel runner worker(s)..."
	local i worker_pid
	for i in $(seq 1 "$RUNNER_POOL_SIZE"); do
		run_worker "$i" &
		worker_pid=$!
		WORKER_PIDS+=("$worker_pid")
		echo "Worker #${i} started (PID: ${worker_pid})"
		# Stagger VM boots slightly to avoid simultaneous resource contention.
		[[ "$i" -lt "$RUNNER_POOL_SIZE" ]] && sleep 3
	done

	# Wait for all workers to finish.
	wait "${WORKER_PIDS[@]}" || true
	WORKER_PIDS=()
	echo "All ${RUNNER_POOL_SIZE} worker(s) have exited."
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	main "$@"
fi
