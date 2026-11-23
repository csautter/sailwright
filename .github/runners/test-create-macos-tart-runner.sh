#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT_UNDER_TEST="${SCRIPT_DIR}/create-macos-tart-runner.sh"
ORIGINAL_PATH="${PATH}"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/macos-tart-runner-tests.XXXXXX")"
FAKE_BIN="${TEST_ROOT}/fake-bin"
FAKE_GUEST_BIN="${TEST_ROOT}/fake-guest-bin"
UTM_AUTOMATION_UNDER_TEST="${SCRIPT_DIR}/prepare-utm-automation.sh"
GUEST_NETWORK_UNDER_TEST="${SCRIPT_DIR}/prepare-guest-network.sh"
export TART_HOME="${TEST_ROOT}/tart"
TEST_LOG_DIR=""
CURRENT_PARENT_PID=""

cleanup() {
	if [[ -n "${CURRENT_PARENT_PID}" ]]; then
		kill "${CURRENT_PARENT_PID}" 2>/dev/null || true
	fi

	if [[ -d "${TEST_ROOT}" ]]; then
		while IFS= read -r pid_file; do
			kill "$(<"${pid_file}")" 2>/dev/null || true
		done < <(find "${TEST_ROOT}" -name remote_pid -type f 2>/dev/null || true)
		rm -rf "${TEST_ROOT}"
	fi
}
trap cleanup EXIT

fail() {
	printf 'not ok - %s\n' "$*" >&2
	exit 1
}

assert_contains() {
	local file="$1"
	local expected="$2"

	if ! grep -Fq -- "$expected" "$file"; then
		printf 'Expected to find:\n%s\n\nin %s:\n' "$expected" "$file" >&2
		sed -n '1,220p' "$file" >&2 || true
		fail "missing expected text"
	fi
}

assert_not_contains() {
	local file="$1"
	local unexpected="$2"

	if grep -Fq -- "$unexpected" "$file"; then
		printf 'Did not expect to find:\n%s\n\nin %s:\n' "$unexpected" "$file" >&2
		sed -n '1,220p' "$file" >&2 || true
		fail "unexpected text found"
	fi
}

assert_count() {
	local file="$1"
	local expected_text="$2"
	local expected_count="$3"
	local actual_count

	actual_count=$(grep -F -c -- "$expected_text" "$file" || true)
	if [[ "$actual_count" != "$expected_count" ]]; then
		printf 'Expected %s occurrence(s) of %s in %s, got %s:\n' \
			"$expected_count" "$expected_text" "$file" "$actual_count" >&2
		sed -n '1,220p' "$file" >&2 || true
		fail "unexpected occurrence count"
	fi
}

wait_for_file() {
	local file="$1"
	local attempts=0

	while [[ ! -e "$file" ]]; do
		attempts=$((attempts + 1))
		if [[ "$attempts" -gt 100 ]]; then
			fail "timed out waiting for ${file}"
		fi
		/bin/sleep 0.05
	done
}

write_fake_commands() {
	mkdir -p "${FAKE_BIN}"

	cat >"${FAKE_BIN}/gh" <<'EOF_GH'
#!/usr/bin/env bash
set -euo pipefail

log_dir="${TEST_LOG_DIR:?TEST_LOG_DIR is required}"
mkdir -p "${log_dir}"
{
	printf 'gh'
	for arg in "$@"; do
		printf '\t%s' "$arg"
	done
	printf '\n'
} >>"${log_dir}/gh.log"

if [[ "${1:-}" != "api" ]]; then
	printf 'fake gh only supports gh api\n' >&2
	exit 1
fi
shift

method="GET"
path=""
expect_method=false
for arg in "$@"; do
	if [[ "${expect_method}" == "true" ]]; then
		method="$arg"
		expect_method=false
		continue
	fi
	if [[ "$arg" == "--method" ]]; then
		expect_method=true
		continue
	fi
	if [[ "$arg" == /* ]]; then
		path="$arg"
	fi
done

if [[ "$path" == *"/registration-token" ]]; then
	printf '%s\n' "${FAKE_GH_REGISTRATION_TOKEN:-test-token}"
	exit 0
fi

if [[ "$method" == "GET" && "$path" == /repos/*/actions/runs*status=in_progress* ]]; then
	if [[ "${FAKE_GH_RUN_LIST_FAIL:-false}" == "true" ]]; then
		printf 'run list failed\n' >&2
		exit "${FAKE_GH_RUN_LIST_EXIT:-22}"
	fi
	if [[ -n "${FAKE_GH_RUN_IDS:-}" ]]; then
		printf '%s\n' "${FAKE_GH_RUN_IDS}"
	fi
	exit 0
fi

if [[ "$method" == "GET" && "$path" == /repos/*/actions/runs/*/jobs* ]]; then
	if [[ "${FAKE_GH_JOB_LIST_FAIL:-false}" == "true" ]]; then
		printf 'job list failed\n' >&2
		exit "${FAKE_GH_JOB_LIST_EXIT:-22}"
	fi

	line="${FAKE_GH_JOB_LINE:-}"
	queue="${FAKE_GH_JOB_LINE_QUEUE:-}"
	if [[ -n "$queue" && -f "$queue" ]]; then
		if IFS= read -r line <"$queue"; then
			tmp="${queue}.$$"
			tail -n +2 "$queue" >"$tmp" || true
			mv "$tmp" "$queue"
		else
			line=""
		fi
	fi

	case "$line" in
		__EMPTY__|"")
			;;
		*)
			printf '%s\n' "$line"
			;;
	esac
	exit 0
fi

if [[ "$method" == "POST" && "$path" == */actions/runs/*/cancel ]]; then
	exit "${FAKE_GH_CANCEL_EXIT:-0}"
fi

if [[ "$method" == "POST" && "$path" == */actions/runs/*/force-cancel ]]; then
	force_cancel_exit="${FAKE_GH_FORCE_CANCEL_EXIT:-0}"
	if [[ "$force_cancel_exit" != "0" ]]; then
		printf '%s\n' "${FAKE_GH_FORCE_CANCEL_ERROR:-force cancel failed}" >&2
	fi
	exit "$force_cancel_exit"
fi

if [[ "$method" == "GET" && "$path" == */actions/runners ]]; then
	if [[ "${FAKE_GH_RUNNER_LIST_FAIL:-false}" == "true" ]]; then
		printf 'runner list failed\n' >&2
		exit "${FAKE_GH_RUNNER_LIST_EXIT:-22}"
	fi

	line="${FAKE_GH_RUNNER_INFO:-}"
	queue="${FAKE_GH_RUNNER_INFO_QUEUE:-}"
	if [[ -n "$queue" && -f "$queue" ]]; then
		if IFS= read -r line <"$queue"; then
			tmp="${queue}.$$"
			tail -n +2 "$queue" >"$tmp" || true
			mv "$tmp" "$queue"
		else
			line=""
		fi
	fi

	case "$line" in
		__FAIL__:*)
			printf '%s\n' "${line#__FAIL__:}" >&2
			exit "${FAKE_GH_RUNNER_LIST_EXIT:-22}"
			;;
		__EMPTY__|"")
			exit 0
			;;
		*)
			printf '%s\n' "$line"
			exit 0
			;;
	esac
fi

if [[ "$method" == "DELETE" && "$path" == */actions/runners/* ]]; then
	exit "${FAKE_GH_DELETE_EXIT:-0}"
fi

printf 'unexpected gh api call: method=%s path=%s args=%s\n' "$method" "$path" "$*" >&2
exit 1
EOF_GH

	cat >"${FAKE_BIN}/tart" <<'EOF_TART'
#!/usr/bin/env bash
set -euo pipefail

log_dir="${TEST_LOG_DIR:?TEST_LOG_DIR is required}"
mkdir -p "${log_dir}"
{
	printf 'tart'
	for arg in "$@"; do
		printf '\t%s' "$arg"
	done
	printf '\n'
} >>"${log_dir}/tart.log"

case "${1:-}" in
	list)
		printf 'SOURCE\tNAME\tSTATE\n'
		printf 'local\t%s\tstopped\n' "${VM_BASE_IMAGE:-tahoe-runner}"
		for vm in ${FAKE_TART_RUNNING:-}; do
			printf 'local\t%s\trunning\n' "$vm"
		done
		;;
	clone)
		mkdir -p "${TART_HOME:?}/vms/${3}"
		printf '{\n  "macAddress" : "8e:00:00:00:00:01",\n  "os" : "darwin"\n}\n' >"${TART_HOME}/vms/${3}/config.json"
		;;
	set|stop|delete)
		exit 0
		;;
	run)
		exit 0
		;;
	ip)
		printf '%s\n' "${FAKE_TART_IP:-127.0.0.1}"
		;;
	*)
		printf 'unexpected tart call: %s\n' "$*" >&2
		exit 1
		;;
esac
EOF_TART

	cat >"${FAKE_BIN}/sshpass" <<'EOF_SSHPASS'
#!/usr/bin/env bash
set -euo pipefail

log_dir="${TEST_LOG_DIR:?TEST_LOG_DIR is required}"
mkdir -p "${log_dir}"
{
	printf 'sshpass'
	for arg in "$@"; do
		printf '\t%s' "$arg"
	done
	printf '\n'
} >>"${log_dir}/sshpass.log"

args=" $* "
if [[ "$args" == *" /bin/bash -s"* ]]; then
	stdin_file="${log_dir}/guest-stdin.$$"
	cat >"${stdin_file}"
	script="$(sed -n 's/^# runner-guest-script: //p' "${stdin_file}")"
	mv "${stdin_file}" "${log_dir}/${script}-stdin"
	printf '%s\n' "${script}" >>"${log_dir}/sshpass.log"
	case "${script}" in
		prepare-utm-automation|prepare-guest-network)
			if [[ "${script}" == prepare-utm-automation ]]; then
				queue="${FAKE_SSH_UTM_EXIT_QUEUE:-}"
				default_exit="${FAKE_SSH_UTM_EXIT:-0}"
			else
				queue="${FAKE_SSH_NETWORK_EXIT_QUEUE:-}"
				default_exit="${FAKE_SSH_NETWORK_EXIT:-0}"
			fi
			if [[ -n "$queue" && -s "$queue" ]]; then
				read -r code <"$queue"
				tail -n +2 "$queue" >"${queue}.$$" && mv "${queue}.$$" "$queue"
				exit "$code"
			fi
			exit "${default_exit}"
			;;
	esac
	exit 97
fi

if [[ "$args" == *" true"* ]]; then
	exit 0
fi

if [[ "$args" == *" bash -s -- "* ]]; then
	cat >/dev/null || true
	printf 'signal-runner\n' >>"${log_dir}/sshpass.log"
	exit 0
fi

if [[ "$args" == *" bash"* ]]; then
	cat >/dev/null || true
	touch "${log_dir}/remote_started"
	printf '%s\n' "$$" >"${log_dir}/remote_pid"
	if [[ "${FAKE_SSH_RUNNER_BLOCK:-false}" == "true" ]]; then
		trap 'printf "remote-term\n" >>"${log_dir}/sshpass.log"; exit 143' TERM INT
		while true; do
			/bin/sleep 1
		done
	fi
	exit "${FAKE_SSH_RUNNER_EXIT:-0}"
fi

exit 0
EOF_SSHPASS

	cat >"${FAKE_BIN}/date" <<'EOF_DATE'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "+%s" ]]; then
	sequence="${FAKE_DATE_SEQUENCE:-}"
	if [[ -n "$sequence" && -f "$sequence" ]]; then
		if IFS= read -r first <"$sequence"; then
			tmp="${sequence}.$$"
			tail -n +2 "$sequence" >"$tmp" || true
			mv "$tmp" "$sequence"
			printf '%s\n' "$first"
			exit 0
		fi
	fi
	if [[ -n "${FAKE_DATE_VALUE:-}" ]]; then
		printf '%s\n' "${FAKE_DATE_VALUE}"
		exit 0
	fi
fi

/bin/date "$@"
EOF_DATE

	cat >"${FAKE_BIN}/sleep" <<'EOF_SLEEP'
#!/usr/bin/env bash
set -euo pipefail

log_dir="${TEST_LOG_DIR:-}"
if [[ -n "$log_dir" ]]; then
	mkdir -p "$log_dir"
	{
		printf 'sleep'
		for arg in "$@"; do
			printf '\t%s' "$arg"
		done
		printf '\n'
	} >>"${log_dir}/sleep.log"
fi
exit 0
EOF_SLEEP

	cat >"${FAKE_BIN}/route" <<'EOF_ROUTE'
#!/usr/bin/env bash
printf '   route to: default\n'
if [[ -n "${FAKE_ROUTER-192.168.1.1}" ]]; then
	printf '    gateway: %s\n' "${FAKE_ROUTER-192.168.1.1}"
fi
printf '  interface: en0\n'
EOF_ROUTE

	chmod +x "${FAKE_BIN}/gh" "${FAKE_BIN}/tart" "${FAKE_BIN}/sshpass" "${FAKE_BIN}/date" "${FAKE_BIN}/sleep" "${FAKE_BIN}/route"
}

# Fakes for the guest-side prepare-utm-automation.sh. They model the macOS
# tools it calls; TCC writes are recorded as SQL instead of touching a database.
write_fake_guest_commands() {
	mkdir -p "${FAKE_GUEST_BIN}"

	cat >"${FAKE_GUEST_BIN}/sudo" <<'EOF_SUDO'
#!/usr/bin/env bash
exec "$@"
EOF_SUDO

	cat >"${FAKE_GUEST_BIN}/defaults" <<'EOF_DEFAULTS'
#!/usr/bin/env bash
printf '%s\n' "${FAKE_BUNDLE_ID:-com.utmapp.UTM}"
EOF_DEFAULTS

	cat >"${FAKE_GUEST_BIN}/xattr" <<'EOF_XATTR'
#!/usr/bin/env bash
set -euo pipefail
state="${TEST_LOG_DIR}/quarantined"
printf 'xattr %s\n' "$*" >>"${TEST_LOG_DIR}/guest.log"
case "${1:-}" in
	-dr) rm -f "$state" ;;
	-p) [[ -e "$state" ]] && printf '0181;test;Homebrew Cask;\n' || exit 1 ;;
esac
EOF_XATTR

	cat >"${FAKE_GUEST_BIN}/csreq" <<'EOF_CSREQ'
#!/usr/bin/env bash
set -euo pipefail
out=""
while [[ $# -gt 0 ]]; do
	case "$1" in
		-b) out="$2"; shift 2 ;;
		*) shift ;;
	esac
done
cat >"$out"
EOF_CSREQ

	cat >"${FAKE_GUEST_BIN}/codesign" <<'EOF_CODESIGN'
#!/usr/bin/env bash
printf 'Executable=/Applications/UTM.app/Contents/MacOS/UTM\n' >&2
printf 'designated => anchor apple generic and identifier "com.utmapp.UTM"\n'
EOF_CODESIGN

	cat >"${FAKE_GUEST_BIN}/sqlite3" <<'EOF_SQLITE'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\t%s\n' "$1" "$2" >>"${TEST_LOG_DIR}/sqlite.log"
if [[ "$2" == SELECT* ]]; then
	printf '%s\n' "${FAKE_TCC_AUTH_VALUE:-2}"
fi
EOF_SQLITE

	cat >"${FAKE_GUEST_BIN}/networksetup" <<'EOF_NETWORKSETUP'
#!/usr/bin/env bash
printf 'networksetup %s\n' "$*" >>"${TEST_LOG_DIR}/guest.log"
EOF_NETWORKSETUP

	cat >"${FAKE_GUEST_BIN}/dscacheutil" <<'EOF_DSCACHEUTIL'
#!/usr/bin/env bash
printf 'dscacheutil %s\n' "$*" >>"${TEST_LOG_DIR}/guest.log"
[[ "${1:-}" == "-q" ]] || exit 0
queue="${FAKE_DNS_QUEUE:-}"
answer="${FAKE_DNS_OK:-true}"
if [[ -n "$queue" && -s "$queue" ]]; then
	read -r answer <"$queue"
	tail -n +2 "$queue" >"${queue}.$$" && mv "${queue}.$$" "$queue"
fi
if [[ "$answer" == "true" ]]; then
	printf 'name: %s\nip_address: 140.82.121.3\n' "${@: -1}"
fi
EOF_DSCACHEUTIL

	cat >"${FAKE_GUEST_BIN}/curl" <<'EOF_CURL'
#!/usr/bin/env bash
printf 'curl %s\n' "$*" >>"${TEST_LOG_DIR}/guest.log"
printf '%s' "${FAKE_HTTP_CODE:-200}"
EOF_CURL

	cat >"${FAKE_GUEST_BIN}/scutil" <<'EOF_SCUTIL'
#!/usr/bin/env bash
printf 'resolver #1\n  nameserver[0] : %s\n' "${FAKE_RESOLVER:-192.168.1.1}"
EOF_SCUTIL

	cat >"${FAKE_GUEST_BIN}/killall" <<'EOF_KILLALL'
#!/usr/bin/env bash
printf 'killall %s\n' "$*" >>"${TEST_LOG_DIR}/guest.log"
EOF_KILLALL

	cat >"${FAKE_GUEST_BIN}/osascript" <<'EOF_OSASCRIPT'
#!/usr/bin/env bash
printf 'osascript %s\n' "$*" >>"${TEST_LOG_DIR}/guest.log"
exit "${FAKE_OSASCRIPT_EXIT:-0}"
EOF_OSASCRIPT

	chmod +x "${FAKE_GUEST_BIN}"/*
}

reset_script_state() {
	GITHUB_SCOPE="repo"
	GITHUB_REPO="owner/repo"
	GITHUB_ORG="octo-org"
	RUNNER_NAME_BASE="test-runner"
	VM_BASE_IMAGE="tahoe-runner"
	VM_CLONE_PER_RUN="true"
	VM_SSH_USER="admin"
	VM_SSH_PASS="admin"
	RUNNER_DIR="/Users/admin/actions-runner"
	RUNNER_POOL_SIZE="1"
	MAX_RUNS="0"
	GITHUB_CANCEL_RUN_ON_SHUTDOWN="true"
	GITHUB_FORCE_CANCEL_RUN_ON_SHUTDOWN="false"
	RUNNER_SHUTDOWN_GRACE_SECONDS="0"
	RUNNER_FORCE_CANCEL_AFTER_SECONDS="30"
	RUNNER_SHUTDOWN_POLL_SECONDS="5"
	REQUESTED_CANCEL_RUN_ID=""
	PREPARE_UTM_AUTOMATION="true"
	RUNNER_NETWORK="nat"
	RUNNER_DNS_SERVERS=""
	RUNNER_DNS_SERVICE="Ethernet"
	RUNNER_FIXED_MAC="true"
	RUNNER_MAC_ADDRESSES=""
	RUNNER_NETWORK_CHECK="true"
	RUNNER_NETWORK_CHECK_URLS="https://github.com/ https://api.github.com/"
	RUNNER_NETWORK_CHECK_TIMEOUT="90"
	TART_VMS_DIR="${TART_HOME}/vms"
	export GITHUB_SCOPE GITHUB_REPO GITHUB_ORG VM_BASE_IMAGE VM_CLONE_PER_RUN
}

begin_test() {
	local name="$1"

	TEST_LOG_DIR="${TEST_ROOT}/${name}"
	mkdir -p "${TEST_LOG_DIR}"
	: >"${TEST_LOG_DIR}/gh.log"
	: >"${TEST_LOG_DIR}/tart.log"
	: >"${TEST_LOG_DIR}/sshpass.log"
	: >"${TEST_LOG_DIR}/sleep.log"
	export TEST_LOG_DIR

	unset FAKE_GH_RUN_IDS FAKE_GH_JOB_LINE FAKE_GH_JOB_LINE_QUEUE FAKE_GH_RUNNER_INFO FAKE_GH_RUNNER_INFO_QUEUE
	unset FAKE_GH_RUN_LIST_FAIL FAKE_GH_JOB_LIST_FAIL FAKE_GH_RUNNER_LIST_FAIL
	unset FAKE_GH_CANCEL_EXIT FAKE_GH_FORCE_CANCEL_EXIT FAKE_GH_FORCE_CANCEL_ERROR FAKE_GH_DELETE_EXIT
	unset FAKE_DATE_SEQUENCE FAKE_DATE_VALUE FAKE_SSH_RUNNER_BLOCK FAKE_SSH_RUNNER_EXIT
	unset FAKE_TART_IP FAKE_SSH_UTM_EXIT FAKE_SSH_UTM_EXIT_QUEUE FAKE_SSH_NETWORK_EXIT FAKE_SSH_NETWORK_EXIT_QUEUE FAKE_TART_RUNNING FAKE_ROUTER
	unset FAKE_DNS_OK FAKE_DNS_QUEUE FAKE_HTTP_CODE FAKE_RESOLVER
	rm -rf "${TART_HOME}"
	unset FAKE_BUNDLE_ID FAKE_TCC_AUTH_VALUE FAKE_OSASCRIPT_EXIT
	: >"${TEST_LOG_DIR}/guest.log"
	: >"${TEST_LOG_DIR}/sqlite.log"

	reset_script_state
}

set_date_sequence() {
	local sequence_file="${TEST_LOG_DIR}/date-sequence"

	: >"${sequence_file}"
	local value
	for value in "$@"; do
		printf '%s\n' "$value" >>"${sequence_file}"
	done
	export FAKE_DATE_SEQUENCE="${sequence_file}"
}

set_job_line_queue() {
	local queue_file="${TEST_LOG_DIR}/job-line-queue"

	: >"${queue_file}"
	local value
	for value in "$@"; do
		printf '%s\n' "$value" >>"${queue_file}"
	done
	export FAKE_GH_JOB_LINE_QUEUE="${queue_file}"
}

set_runner_info_queue() {
	local queue_file="${TEST_LOG_DIR}/runner-info-queue"

	: >"${queue_file}"
	local value
	for value in "$@"; do
		printf '%s\n' "$value" >>"${queue_file}"
	done
	export FAKE_GH_RUNNER_INFO_QUEUE="${queue_file}"
}

run_test() {
	local name="$1"

	printf 'running %s\n' "$name"
	"$name"
	printf 'ok - %s\n' "$name"
}

write_fake_commands
write_fake_guest_commands
PATH="${FAKE_BIN}:${ORIGINAL_PATH}"
export PATH
RUNNER_NAME_BASE="test-runner"
GITHUB_REPO="owner/repo"
VM_BASE_IMAGE="tahoe-runner"

# shellcheck source=.github/runners/create-macos-tart-runner.sh
source "${SCRIPT_UNDER_TEST}"

test_successful_cancel_signals_runner_and_stops_vm() {
	begin_test "successful-cancel"
	export FAKE_GH_RUN_IDS="9001"
	export FAKE_GH_JOB_LINE=$'9001\t501\tbuild\t"https://example.test/job"'
	export FAKE_GH_RUNNER_INFO=$'123\tfalse\tonline'

	cleanup_vm "vm-1" "runner-1" "10.0.0.2" "true" >"${TEST_LOG_DIR}/output" 2>&1

	assert_contains "${TEST_LOG_DIR}/output" "[vm-1] Canceling workflow run 9001"
	assert_contains "${TEST_LOG_DIR}/output" "[vm-1] Sending SIGINT to runner process inside VM"
	assert_contains "${TEST_LOG_DIR}/output" "Runner 'runner-1' is no longer busy"
	assert_contains "${TEST_LOG_DIR}/gh.log" "/repos/owner/repo/actions/runs/9001/cancel"
	assert_contains "${TEST_LOG_DIR}/sshpass.log" "signal-runner"
	assert_contains "${TEST_LOG_DIR}/tart.log" $'tart\tstop\tvm-1'
	assert_contains "${TEST_LOG_DIR}/tart.log" $'tart\tdelete\tvm-1'
}

test_runner_list_api_failure_remains_unknown() {
	begin_test "runner-list-api-failure"
	RUNNER_SHUTDOWN_GRACE_SECONDS="1"
	export FAKE_GH_RUNNER_LIST_FAIL="true"
	set_date_sequence 100 101

	if wait_for_runner_to_settle "vm-1" "runner-1" "" >"${TEST_LOG_DIR}/output" 2>&1; then
		fail "wait_for_runner_to_settle unexpectedly succeeded"
	fi

	assert_contains "${TEST_LOG_DIR}/output" "Runner 'runner-1' state is unknown"
	assert_contains "${TEST_LOG_DIR}/output" "could not confirm runner 'runner-1' state"
	assert_not_contains "${TEST_LOG_DIR}/output" "Runner 'runner-1' is no longer registered"
}

test_no_active_job_waits_until_busy_grace_deadline() {
	begin_test "no-active-job-busy-until-deadline"
	RUNNER_SHUTDOWN_GRACE_SECONDS="1"
	export FAKE_GH_RUN_IDS=""
	export FAKE_GH_RUNNER_INFO=$'123\ttrue\tonline'
	set_date_sequence 100 101

	cleanup_vm "vm-1" "runner-1" "10.0.0.2" "true" >"${TEST_LOG_DIR}/output" 2>&1 || true

	assert_contains "${TEST_LOG_DIR}/output" "No in-progress GitHub Actions job found for runner 'runner-1'"
	assert_contains "${TEST_LOG_DIR}/output" "runner 'runner-1' is still busy after 1s"
	assert_contains "${TEST_LOG_DIR}/output" "runner 'runner-1' is still busy; skipping GitHub runner deletion"
	assert_contains "${TEST_LOG_DIR}/output" "leaving VM running because GitHub still reports runner 'runner-1' as busy or unknown"
	assert_not_contains "${TEST_LOG_DIR}/gh.log" "/cancel"
	assert_not_contains "${TEST_LOG_DIR}/tart.log" $'tart\tstop\tvm-1'
	assert_not_contains "${TEST_LOG_DIR}/tart.log" $'tart\tdelete\tvm-1'
}

test_zero_shutdown_grace_waits_until_runner_releases() {
	begin_test "zero-shutdown-grace-waits"
	RUNNER_SHUTDOWN_GRACE_SECONDS="0"
	set_runner_info_queue $'123\ttrue\tonline' $'123\tfalse\tonline'
	set_date_sequence 100 100 100

	wait_for_runner_to_settle "vm-1" "runner-1" "9001" >"${TEST_LOG_DIR}/output" 2>&1

	assert_contains "${TEST_LOG_DIR}/output" "Runner 'runner-1' is no longer busy"
	assert_not_contains "${TEST_LOG_DIR}/output" "runner 'runner-1' is still busy after 0s"
}

test_zero_shutdown_poll_seconds_normalizes_before_polling() {
	begin_test "zero-shutdown-poll-normalized"
	RUNNER_SHUTDOWN_GRACE_SECONDS="0"
	RUNNER_SHUTDOWN_POLL_SECONDS="0"
	set_runner_info_queue $'123\ttrue\tonline' $'123\tfalse\tonline'
	set_date_sequence 100 100 100

	wait_for_runner_to_settle "vm-1" "runner-1" "9001" >"${TEST_LOG_DIR}/output" 2>&1

	assert_contains "${TEST_LOG_DIR}/output" "Runner 'runner-1' is no longer busy"
	assert_contains "${TEST_LOG_DIR}/sleep.log" $'sleep\t5'
	assert_not_contains "${TEST_LOG_DIR}/sleep.log" $'sleep\t0'
}

test_busy_runner_retries_job_lookup_and_cancels_race() {
	begin_test "busy-runner-retry-cancel"
	RUNNER_SHUTDOWN_GRACE_SECONDS="1"
	export FAKE_GH_RUN_IDS="9001"
	set_job_line_queue "__EMPTY__" $'9001\t501\tbuild\t"https://example.test/job"'
	set_runner_info_queue $'123\ttrue\tonline' $'123\tfalse\tonline' $'123\tfalse\tonline'
	set_date_sequence 100 100 100

	cleanup_vm "vm-1" "runner-1" "10.0.0.2" "true" >"${TEST_LOG_DIR}/output" 2>&1

	assert_contains "${TEST_LOG_DIR}/output" "Runner 'runner-1' is busy; retrying active job lookup before teardown"
	assert_contains "${TEST_LOG_DIR}/output" "Canceling workflow run 9001"
	assert_contains "${TEST_LOG_DIR}/output" "Runner 'runner-1' is no longer busy"
	assert_contains "${TEST_LOG_DIR}/gh.log" "/repos/owner/repo/actions/runs/9001/cancel"
	assert_contains "${TEST_LOG_DIR}/tart.log" $'tart\tstop\tvm-1'
	assert_contains "${TEST_LOG_DIR}/tart.log" $'tart\tdelete\tvm-1'
}

test_force_cancel_requires_opt_in() {
	begin_test "force-cancel-disabled"
	GITHUB_FORCE_CANCEL_RUN_ON_SHUTDOWN="false"
	RUNNER_SHUTDOWN_GRACE_SECONDS="1"
	RUNNER_FORCE_CANCEL_AFTER_SECONDS="1"
	export FAKE_GH_RUNNER_INFO=$'123\ttrue\tonline'
	set_date_sequence 100 101

	if wait_for_runner_to_settle "vm-1" "runner-1" "9001" >"${TEST_LOG_DIR}/output" 2>&1; then
		fail "wait_for_runner_to_settle unexpectedly succeeded with busy runner"
	fi
	assert_not_contains "${TEST_LOG_DIR}/gh.log" "/force-cancel"

	begin_test "force-cancel-enabled"
	GITHUB_FORCE_CANCEL_RUN_ON_SHUTDOWN="true"
	RUNNER_SHUTDOWN_GRACE_SECONDS="1"
	RUNNER_FORCE_CANCEL_AFTER_SECONDS="1"
	export FAKE_GH_RUNNER_INFO=$'123\ttrue\tonline'
	set_date_sequence 100 101

	if wait_for_runner_to_settle "vm-1" "runner-1" "9001" >"${TEST_LOG_DIR}/output" 2>&1; then
		fail "wait_for_runner_to_settle unexpectedly succeeded with busy runner"
	fi
	assert_contains "${TEST_LOG_DIR}/output" "requesting force-cancel"
	assert_contains "${TEST_LOG_DIR}/gh.log" "/repos/owner/repo/actions/runs/9001/force-cancel"
}

test_force_cancel_failure_is_reported() {
	begin_test "force-cancel-failure"
	GITHUB_FORCE_CANCEL_RUN_ON_SHUTDOWN="true"
	RUNNER_SHUTDOWN_GRACE_SECONDS="1"
	RUNNER_FORCE_CANCEL_AFTER_SECONDS="1"
	export FAKE_GH_RUNNER_INFO=$'123\ttrue\tonline'
	export FAKE_GH_FORCE_CANCEL_EXIT="22"
	export FAKE_GH_FORCE_CANCEL_ERROR="force cancel denied"
	set_date_sequence 100 101

	if wait_for_runner_to_settle "vm-1" "runner-1" "9001" >"${TEST_LOG_DIR}/output" 2>&1; then
		fail "wait_for_runner_to_settle unexpectedly succeeded with busy runner"
	fi

	assert_contains "${TEST_LOG_DIR}/output" "requesting force-cancel"
	assert_contains "${TEST_LOG_DIR}/output" "GitHub API force-cancel for workflow run 9001 failed (exit 22): force cancel denied"
	assert_contains "${TEST_LOG_DIR}/output" "force-cancel for workflow run 9001 was not accepted; manual cancellation may be required"
	assert_contains "${TEST_LOG_DIR}/gh.log" "/repos/owner/repo/actions/runs/9001/force-cancel"
}

test_zero_force_cancel_delay_disables_force_cancel() {
	begin_test "zero-force-cancel-delay"
	GITHUB_FORCE_CANCEL_RUN_ON_SHUTDOWN="true"
	RUNNER_SHUTDOWN_GRACE_SECONDS="1"
	RUNNER_FORCE_CANCEL_AFTER_SECONDS="0"
	export FAKE_GH_RUNNER_INFO=$'123\ttrue\tonline'
	set_date_sequence 100 101

	if wait_for_runner_to_settle "vm-1" "runner-1" "9001" >"${TEST_LOG_DIR}/output" 2>&1; then
		fail "wait_for_runner_to_settle unexpectedly succeeded with busy runner"
	fi

	assert_not_contains "${TEST_LOG_DIR}/gh.log" "/force-cancel"
	assert_contains "${TEST_LOG_DIR}/output" "runner 'runner-1' is still busy after 1s"
}

test_org_scope_does_not_call_repo_workflow_run_endpoints() {
	begin_test "org-scope"
	GITHUB_SCOPE="org"
	GITHUB_ORG="octo-org"
	GITHUB_REPO="owner/repo"
	export FAKE_GH_RUNNER_INFO=$'123\tfalse\tonline'

	cleanup_vm "vm-1" "runner-1" "10.0.0.2" "true" >"${TEST_LOG_DIR}/output" 2>&1

	assert_contains "${TEST_LOG_DIR}/output" "automatic workflow cancellation currently requires GITHUB_SCOPE=repo"
	assert_not_contains "${TEST_LOG_DIR}/gh.log" "/repos/owner/repo/actions/runs"
	assert_contains "${TEST_LOG_DIR}/gh.log" "/orgs/octo-org/actions/runners"
	assert_contains "${TEST_LOG_DIR}/gh.log" "/orgs/octo-org/actions/runners/123"
}

test_parent_term_reaches_worker_and_cleanup_is_idempotent() {
	begin_test "parent-term"
	export FAKE_GH_RUN_IDS="9001"
	export FAKE_GH_JOB_LINE=$'9001\t501\tbuild\t"https://example.test/job"'
	export FAKE_GH_RUNNER_INFO=$'123\tfalse\tonline'
	export FAKE_DATE_VALUE="1700000000"
	export FAKE_SSH_RUNNER_BLOCK="true"

	PATH="${FAKE_BIN}:${ORIGINAL_PATH}" \
	TEST_LOG_DIR="${TEST_LOG_DIR}" \
	FAKE_GH_RUN_IDS="${FAKE_GH_RUN_IDS}" \
	FAKE_GH_JOB_LINE="${FAKE_GH_JOB_LINE}" \
	FAKE_GH_RUNNER_INFO="${FAKE_GH_RUNNER_INFO}" \
	FAKE_DATE_VALUE="${FAKE_DATE_VALUE}" \
	FAKE_SSH_RUNNER_BLOCK="${FAKE_SSH_RUNNER_BLOCK}" \
	GITHUB_SCOPE="repo" \
	GITHUB_REPO="owner/repo" \
	RUNNER_NAME_BASE="test-runner" \
	RUNNER_POOL_SIZE="1" \
	MAX_RUNS="0" \
	VM_BASE_IMAGE="tahoe-runner" \
	VM_CLONE_PER_RUN="true" \
	GITHUB_CANCEL_RUN_ON_SHUTDOWN="true" \
	GITHUB_FORCE_CANCEL_RUN_ON_SHUTDOWN="false" \
	RUNNER_SHUTDOWN_GRACE_SECONDS="5" \
	RUNNER_FORCE_CANCEL_AFTER_SECONDS="30" \
	RUNNER_SHUTDOWN_POLL_SECONDS="0" \
	bash "${SCRIPT_UNDER_TEST}" >"${TEST_LOG_DIR}/output" 2>&1 &
	CURRENT_PARENT_PID=$!

	wait_for_file "${TEST_LOG_DIR}/remote_started"
	kill -TERM "${CURRENT_PARENT_PID}"

	local status=0
	if wait "${CURRENT_PARENT_PID}"; then
		status=0
	else
		status=$?
	fi
	CURRENT_PARENT_PID=""

	if [[ "$status" != "143" ]]; then
		sed -n '1,260p' "${TEST_LOG_DIR}/output" >&2 || true
		fail "expected parent to exit 143 after TERM, got ${status}"
	fi

	assert_contains "${TEST_LOG_DIR}/output" "Shutting down all workers"
	assert_contains "${TEST_LOG_DIR}/sshpass.log" "signal-runner"
	assert_contains "${TEST_LOG_DIR}/gh.log" "/repos/owner/repo/actions/runs/9001/cancel"
	assert_count "${TEST_LOG_DIR}/tart.log" $'tart\tstop\trunner-w1-1700000000' "1"
	assert_count "${TEST_LOG_DIR}/tart.log" $'tart\tdelete\trunner-w1-1700000000' "1"
	assert_count "${TEST_LOG_DIR}/output" "Shutting down all workers" "1"
}

test_worker_prepares_utm_before_runner_registration() {
	begin_test "prepare-utm-stdin"

	prepare_utm_automation "worker-1" "10.0.0.2" >"${TEST_LOG_DIR}/output" 2>&1

	assert_contains "${TEST_LOG_DIR}/output" "[worker-1] Preparing UTM for unattended AppleScript..."
	assert_contains "${TEST_LOG_DIR}/sshpass.log" "prepare-utm-automation"
	assert_contains "${TEST_LOG_DIR}/sshpass.log" $'admin@10.0.0.2\t/bin/bash\t-s'
	cmp -s "${TEST_LOG_DIR}/prepare-utm-automation-stdin" "${UTM_AUTOMATION_UNDER_TEST}" ||
		fail "the guest did not receive prepare-utm-automation.sh on stdin"
}

test_utm_preparation_failure_only_warns() {
	begin_test "prepare-utm-failure"
	export FAKE_SSH_UTM_EXIT="1"

	prepare_utm_automation "worker-1" "10.0.0.2" >"${TEST_LOG_DIR}/output" 2>&1 ||
		fail "a failed UTM preparation must not abort the runner cycle"

	assert_contains "${TEST_LOG_DIR}/output" "WARNING: UTM preparation failed"
	assert_count "${TEST_LOG_DIR}/sshpass.log" "prepare-utm-automation" "3"
}

test_utm_preparation_retries_transient_ssh_failure() {
	begin_test "prepare-utm-retry"
	printf '255\n0\n' >"${TEST_LOG_DIR}/utm-exit-queue"
	export FAKE_SSH_UTM_EXIT_QUEUE="${TEST_LOG_DIR}/utm-exit-queue"

	prepare_utm_automation "worker-1" "10.0.0.2" >"${TEST_LOG_DIR}/output" 2>&1

	assert_count "${TEST_LOG_DIR}/sshpass.log" "prepare-utm-automation" "2"
	assert_not_contains "${TEST_LOG_DIR}/output" "WARNING"
	assert_contains "${TEST_LOG_DIR}/sleep.log" $'sleep\t5'
}

test_utm_preparation_can_be_disabled() {
	begin_test "prepare-utm-disabled"
	PREPARE_UTM_AUTOMATION="false"

	prepare_utm_automation "worker-1" "10.0.0.2" >"${TEST_LOG_DIR}/output" 2>&1

	assert_not_contains "${TEST_LOG_DIR}/sshpass.log" "prepare-utm-automation"
}

test_worker_cycle_prepares_utm_before_config() {
	begin_test "prepare-utm-cycle"
	export FAKE_DATE_VALUE="1700000000"
	MAX_RUNS="1"

	(run_worker 1) >"${TEST_LOG_DIR}/output" 2>&1 || true

	assert_contains "${TEST_LOG_DIR}/output" "[worker-1] Preparing UTM for unattended AppleScript..."
	local prepare_line runner_line
	prepare_line="$(grep -n 'prepare-utm-automation' "${TEST_LOG_DIR}/sshpass.log" | head -1 | cut -d: -f1)"
	runner_line="$(grep -n $'\tbash$' "${TEST_LOG_DIR}/sshpass.log" | head -1 | cut -d: -f1)"
	[[ -n "$prepare_line" && -n "$runner_line" && "$prepare_line" -lt "$runner_line" ]] ||
		fail "UTM must be prepared before the runner session starts (prepare=${prepare_line:-none}, runner=${runner_line:-none})"
}

run_guest_script() {
	local app="${TEST_LOG_DIR}/UTM.app"
	mkdir -p "${app}/Contents"
	PATH="${FAKE_GUEST_BIN}:${FAKE_BIN}:${ORIGINAL_PATH}" \
	UTM_APP="${UTM_APP_OVERRIDE:-${app}}" \
	TCC_DB="${TEST_LOG_DIR}/TCC.db" \
	UTM_VERIFY_TIMEOUT="5" \
	bash "${UTM_AUTOMATION_UNDER_TEST}" "$@" >"${TEST_LOG_DIR}/output" 2>&1
}

hex_of() {
	printf '%s\n' "$1" | od -An -tx1 -v | tr -d ' \n'
}

test_guest_clears_quarantine_and_grants_ssh_automation() {
	begin_test "guest-grant"
	touch "${TEST_LOG_DIR}/quarantined"

	run_guest_script || fail "guest preparation failed: $(cat "${TEST_LOG_DIR}/output")"

	[[ ! -e "${TEST_LOG_DIR}/quarantined" ]] || fail "quarantine was not cleared"
	assert_contains "${TEST_LOG_DIR}/guest.log" "xattr -dr com.apple.quarantine ${TEST_LOG_DIR}/UTM.app"
	assert_contains "${TEST_LOG_DIR}/sqlite.log" "VALUES ('kTCCServiceAppleEvents', '/usr/libexec/sshd-keygen-wrapper', 1, 2, 3, 1, X'$(hex_of 'identifier "com.apple.sshd-keygen-wrapper" and anchor apple')', NULL, 0, 'com.utmapp.UTM', X'$(hex_of 'anchor apple generic and identifier "com.utmapp.UTM"')', 0);"
	assert_contains "${TEST_LOG_DIR}/output" "UTM: Automation granted for /usr/libexec/sshd-keygen-wrapper -> com.utmapp.UTM."
	assert_not_contains "${TEST_LOG_DIR}/guest.log" "osascript"
}

test_guest_preparation_is_idempotent() {
	begin_test "guest-idempotent"

	run_guest_script || fail "first run failed"
	run_guest_script || fail "second run failed: $(cat "${TEST_LOG_DIR}/output")"

	assert_count "${TEST_LOG_DIR}/sqlite.log" "INSERT OR REPLACE INTO access" "2"
}

test_guest_verify_launches_and_quits_utm() {
	begin_test "guest-verify"

	run_guest_script --verify || fail "verify failed: $(cat "${TEST_LOG_DIR}/output")"

	assert_contains "${TEST_LOG_DIR}/guest.log" 'osascript -e tell application id "com.utmapp.UTM" to get name of every virtual machine'
	assert_contains "${TEST_LOG_DIR}/guest.log" 'osascript -e tell application id "com.utmapp.UTM" to quit'
	assert_contains "${TEST_LOG_DIR}/output" "UTM: Apple Events answered."
}

test_guest_verify_failure_is_fatal() {
	begin_test "guest-verify-failure"
	export FAKE_OSASCRIPT_EXIT="142"

	if run_guest_script --verify; then
		fail "an unanswered Apple Event must fail the golden image build"
	fi
	assert_contains "${TEST_LOG_DIR}/output" "UTM did not answer Apple Events within 5s"
}

test_guest_unstored_grant_is_fatal() {
	begin_test "guest-unstored-grant"
	export FAKE_TCC_AUTH_VALUE="0"

	if run_guest_script; then
		fail "a grant that is not readable afterwards must fail"
	fi
	assert_contains "${TEST_LOG_DIR}/output" "was not stored"
}

test_guest_rejects_unsafe_bundle_identifier() {
	begin_test "guest-unsafe-bundle-id"
	export FAKE_BUNDLE_ID="com.utmapp.UTM'; DELETE FROM access; --"

	if run_guest_script; then
		fail "an unexpected bundle identifier must be rejected"
	fi
	assert_contains "${TEST_LOG_DIR}/output" "unexpected UTM bundle identifier"
	assert_count "${TEST_LOG_DIR}/sqlite.log" "access" "0"
}

test_guest_without_utm_is_a_noop_unless_verifying() {
	begin_test "guest-no-utm"

	UTM_APP_OVERRIDE="${TEST_LOG_DIR}/missing.app" run_guest_script || fail "missing UTM must be a no-op"
	assert_contains "${TEST_LOG_DIR}/output" "nothing to prepare"
	if UTM_APP_OVERRIDE="${TEST_LOG_DIR}/missing.app" run_guest_script --verify; then
		fail "missing UTM must fail with --verify"
	fi
}

test_nat_is_default_and_bridged_is_opt_in() {
	begin_test "network-mode"

	[[ -z "$(tart_network_args)" && -z "$(tart_ip_args)" ]] || fail "NAT must not pass bridged or ARP flags"
	RUNNER_NETWORK="bridged:Wi-Fi"
	[[ "$(tart_network_args)" == "--net-bridged=Wi-Fi" ]] || fail "bridged flag missing"
	[[ "$(tart_ip_args)" == "--resolver=arp" ]] || fail "bridged VMs need the ARP resolver"
	validate_runner_network 2>/dev/null || fail "bridged:Wi-Fi must be valid"
	for invalid in "bridged" "bridged:" "host" ""; do
		RUNNER_NETWORK="$invalid"
		if validate_runner_network 2>/dev/null; then
			fail "RUNNER_NETWORK='${invalid}' must be rejected"
		fi
	done
	RUNNER_NETWORK="nat"
	RUNNER_NETWORK_CHECK_TIMEOUT="soon"
	if validate_runner_network 2>/dev/null; then
		fail "a non-numeric check timeout must be rejected"
	fi
}

test_guest_dns_defaults_to_host_router_with_nat() {
	begin_test "guest-dns-resolution"

	[[ "$(resolve_guest_dns)" == "192.168.1.1" ]] || fail "NAT must default to the host's router"
	RUNNER_DNS_SERVERS="10.0.0.53, 10.0.0.54"
	[[ "$(resolve_guest_dns)" == "10.0.0.53  10.0.0.54" || "$(resolve_guest_dns)" == "10.0.0.53 10.0.0.54" ]] ||
		fail "comma-separated servers must become a list, got '$(resolve_guest_dns)'"
	RUNNER_DNS_SERVERS="dhcp"
	[[ "$(resolve_guest_dns)" == "dhcp" ]] || fail "dhcp must pass through"
	RUNNER_DNS_SERVERS=""
	RUNNER_NETWORK="bridged:Wi-Fi"
	[[ "$(resolve_guest_dns)" == "keep" ]] || fail "bridged must keep the LAN DHCP DNS by default"
	RUNNER_NETWORK="nat"
	export FAKE_ROUTER=""
	if resolve_guest_dns >/dev/null 2>"${TEST_LOG_DIR}/output"; then
		fail "a missing default router must fail"
	fi
	assert_contains "${TEST_LOG_DIR}/output" "could not determine the host's default IPv4 router"
}

test_worker_mac_is_stable_unicast_and_per_slot() {
	begin_test "worker-mac"

	local first second again
	first="$(worker_mac_address 1)"
	again="$(worker_mac_address 1)"
	second="$(worker_mac_address 2)"
	[[ "$first" == "$again" ]] || fail "slot MAC must be stable"
	[[ "$first" != "$second" ]] || fail "slots must not share a MAC"
	[[ "$first" =~ ^02(:[0-9a-f]{2}){4}:01$ ]] || fail "unexpected derived MAC ${first}"
	RUNNER_MAC_ADDRESSES="0a:00:00:00:00:0A, 0a:00:00:00:00:0b"
	[[ "$(worker_mac_address 2)" == "0a:00:00:00:00:0b" ]] || fail "configured MAC not used"
	[[ "$(worker_mac_address 1)" == "0a:00:00:00:00:0a" ]] || fail "configured MAC not normalized"
	if worker_mac_address 3 >/dev/null 2>&1; then
		fail "a slot without a configured MAC must fail"
	fi
	RUNNER_MAC_ADDRESSES="01:00:5e:00:00:01"
	if worker_mac_address 1 >/dev/null 2>&1; then
		fail "a multicast MAC must be rejected"
	fi
}

test_fixed_mac_is_written_to_clone_config() {
	begin_test "apply-mac"
	tart clone tahoe-runner runner-w1-1

	apply_worker_mac "worker-1" "runner-w1-1" 1 >"${TEST_LOG_DIR}/output" 2>&1 || fail "apply failed"

	assert_contains "${TART_VMS_DIR}/runner-w1-1/config.json" "\"macAddress\": \"$(worker_mac_address 1)\""
	assert_contains "${TART_VMS_DIR}/runner-w1-1/config.json" '"os": "darwin"'
	assert_contains "${TEST_LOG_DIR}/output" "Using fixed MAC"
}

test_fixed_mac_taken_by_running_vm_keeps_random_mac() {
	begin_test "apply-mac-in-use"
	local mac
	mac="$(worker_mac_address 1)"
	tart clone tahoe-runner runner-w1-old
	printf '{"macAddress" : "%s"}\n' "$mac" >"${TART_VMS_DIR}/runner-w1-old/config.json"
	tart clone tahoe-runner runner-w1-new
	export FAKE_TART_RUNNING="runner-w1-old"

	apply_worker_mac "worker-1" "runner-w1-new" 1 >"${TEST_LOG_DIR}/output" 2>&1 || fail "fallback must not fail"

	assert_contains "${TEST_LOG_DIR}/output" "is in use by running VM 'runner-w1-old'"
	assert_contains "${TART_VMS_DIR}/runner-w1-new/config.json" "8e:00:00:00:00:01"

	export FAKE_TART_RUNNING=""
	apply_worker_mac "worker-1" "runner-w1-new" 1 >"${TEST_LOG_DIR}/output" 2>&1 || fail "apply failed"
	assert_contains "${TART_VMS_DIR}/runner-w1-new/config.json" "$mac"
}

test_guest_network_receives_router_dns_and_check_urls() {
	begin_test "guest-network-args"

	prepare_guest_network "worker-1" "10.0.0.2" >"${TEST_LOG_DIR}/output" 2>&1 || fail "network preparation failed"

	assert_contains "${TEST_LOG_DIR}/output" "Preparing guest network (DNS: 192.168.1.1)"
	assert_contains "${TEST_LOG_DIR}/sshpass.log" $'/bin/bash\t-s\t--\t--dns 192.168.1.1 --service Ethernet --timeout 90 --url https://github.com/ --url https://api.github.com/ '
	cmp -s "${TEST_LOG_DIR}/prepare-guest-network-stdin" "${GUEST_NETWORK_UNDER_TEST}" ||
		fail "the guest did not receive prepare-guest-network.sh on stdin"
}

test_guest_network_is_skipped_when_bridged_without_check() {
	begin_test "guest-network-skip"
	RUNNER_NETWORK="bridged:Wi-Fi"
	RUNNER_NETWORK_CHECK="false"

	prepare_guest_network "worker-1" "10.0.0.2" >"${TEST_LOG_DIR}/output" 2>&1 || fail "skip must succeed"

	assert_not_contains "${TEST_LOG_DIR}/sshpass.log" "prepare-guest-network"
}

test_worker_cycle_uses_nat_slot_mac_and_checks_network_first() {
	begin_test "worker-cycle-network"
	export FAKE_DATE_VALUE="1700000000"
	local queue="${TEST_LOG_DIR}/network-exit-queue"
	printf '1\n0\n' >"$queue"
	export FAKE_SSH_NETWORK_EXIT_QUEUE="$queue"
	MAX_RUNS="2"

	(run_worker 1) >"${TEST_LOG_DIR}/output" 2>&1 || true

	assert_contains "${TEST_LOG_DIR}/output" "[worker-1] Using fixed MAC $(worker_mac_address 1) for this slot."
	assert_contains "${TEST_LOG_DIR}/output" "[worker-1] ERROR: Guest network is not ready. Cleaning up and retrying in 30s..."
	assert_contains "${TEST_LOG_DIR}/tart.log" $'tart\trun\t--no-graphics\trunner-w1-1700000000'
	assert_not_contains "${TEST_LOG_DIR}/tart.log" "--net-bridged"
	assert_contains "${TEST_LOG_DIR}/tart.log" $'tart\tip\trunner-w1-1700000000'
	assert_not_contains "${TEST_LOG_DIR}/tart.log" "--resolver=arp"
	assert_count "${TEST_LOG_DIR}/sshpass.log" "prepare-guest-network" "2"
	# The failed cycle must not register a runner; the second one does.
	assert_count "${TEST_LOG_DIR}/sshpass.log" "prepare-utm-automation" "1"
	local network_line runner_line
	network_line="$(grep -n 'prepare-guest-network' "${TEST_LOG_DIR}/sshpass.log" | tail -1 | cut -d: -f1)"
	runner_line="$(grep -n $'\tbash$' "${TEST_LOG_DIR}/sshpass.log" | head -1 | cut -d: -f1)"
	[[ -n "$network_line" && -n "$runner_line" && "$network_line" -lt "$runner_line" ]] ||
		fail "the network check must pass before the runner session starts"
	assert_contains "${TEST_LOG_DIR}/sleep.log" $'sleep\t30'
}

test_worker_cycle_bridged_opt_in() {
	begin_test "worker-cycle-bridged"
	export FAKE_DATE_VALUE="1700000000"
	RUNNER_NETWORK="bridged:Wi-Fi"
	MAX_RUNS="1"

	(run_worker 1) >"${TEST_LOG_DIR}/output" 2>&1 || true

	assert_contains "${TEST_LOG_DIR}/tart.log" $'tart\trun\t--no-graphics\t--net-bridged=Wi-Fi\trunner-w1-1700000000'
	assert_contains "${TEST_LOG_DIR}/tart.log" $'tart\tip\t--resolver=arp\trunner-w1-1700000000'
	assert_contains "${TEST_LOG_DIR}/sshpass.log" $'--dns keep --service Ethernet'
}

run_network_script() {
	PATH="${FAKE_GUEST_BIN}:${FAKE_BIN}:${ORIGINAL_PATH}" \
	bash "${GUEST_NETWORK_UNDER_TEST}" "$@" >"${TEST_LOG_DIR}/output" 2>&1
}

test_guest_network_sets_dns_and_reports_ready() {
	begin_test "guest-net-ready"

	run_network_script --dns "192.168.1.1 192.168.1.2" --url https://github.com/ --url https://api.github.com:443/x ||
		fail "network script failed: $(cat "${TEST_LOG_DIR}/output")"

	assert_contains "${TEST_LOG_DIR}/guest.log" "networksetup -setdnsservers Ethernet 192.168.1.1 192.168.1.2"
	assert_contains "${TEST_LOG_DIR}/guest.log" "killall -HUP mDNSResponder"
	assert_contains "${TEST_LOG_DIR}/guest.log" "dscacheutil -q host -a name api.github.com"
	assert_contains "${TEST_LOG_DIR}/output" "Guest network ready after 1 attempt(s) (resolvers: 192.168.1.1)."
}

test_guest_network_dhcp_and_keep() {
	begin_test "guest-net-dhcp-keep"

	run_network_script --dns dhcp --service "Ethernet 2" || fail "dhcp failed"
	assert_contains "${TEST_LOG_DIR}/guest.log" "networksetup -setdnsservers Ethernet 2 Empty"
	: >"${TEST_LOG_DIR}/guest.log"
	run_network_script --url https://github.com/ || fail "keep failed"
	assert_not_contains "${TEST_LOG_DIR}/guest.log" "networksetup"
}

test_guest_network_rejects_bad_input() {
	begin_test "guest-net-bad-input"

	if run_network_script --dns "192.168.1.1; reboot"; then
		fail "a non-IP DNS server must be rejected"
	fi
	assert_contains "${TEST_LOG_DIR}/output" "not an IP address"
	assert_not_contains "${TEST_LOG_DIR}/guest.log" "networksetup"
	if run_network_script --timeout soon; then
		fail "a non-numeric timeout must be rejected"
	fi
}

test_guest_network_dns_failure_names_stage_and_resolver() {
	begin_test "guest-net-dns-failure"
	export FAKE_DNS_OK="false" FAKE_RESOLVER="192.168.64.1"

	if run_network_script --url https://github.com/ --timeout 0; then
		fail "a broken DNS path must fail"
	fi
	assert_contains "${TEST_LOG_DIR}/output" "guest network not ready after 0s (1 attempt(s)): system DNS cannot resolve github.com; resolvers: 192.168.64.1."
	assert_not_contains "${TEST_LOG_DIR}/guest.log" "curl"
}

test_guest_network_https_failure_is_reported() {
	begin_test "guest-net-https-failure"
	export FAKE_HTTP_CODE="000"

	if run_network_script --url https://github.com/ --timeout 0; then
		fail "an unreachable HTTPS endpoint must fail"
	fi
	assert_contains "${TEST_LOG_DIR}/output" "HTTPS to https://github.com/ failed"
}

test_guest_network_retries_until_ready() {
	begin_test "guest-net-retry"
	local queue="${TEST_LOG_DIR}/dns-queue"
	printf 'false\nfalse\ntrue\n' >"$queue"
	export FAKE_DNS_QUEUE="$queue"

	run_network_script --url https://github.com/ --timeout 30 --interval 0 ||
		fail "retry failed: $(cat "${TEST_LOG_DIR}/output")"
	assert_contains "${TEST_LOG_DIR}/output" "Guest network ready after 3 attempt(s)"
}

run_test test_successful_cancel_signals_runner_and_stops_vm
run_test test_runner_list_api_failure_remains_unknown
run_test test_no_active_job_waits_until_busy_grace_deadline
run_test test_zero_shutdown_grace_waits_until_runner_releases
run_test test_zero_shutdown_poll_seconds_normalizes_before_polling
run_test test_busy_runner_retries_job_lookup_and_cancels_race
run_test test_force_cancel_requires_opt_in
run_test test_force_cancel_failure_is_reported
run_test test_zero_force_cancel_delay_disables_force_cancel
run_test test_org_scope_does_not_call_repo_workflow_run_endpoints
run_test test_parent_term_reaches_worker_and_cleanup_is_idempotent
run_test test_worker_prepares_utm_before_runner_registration
run_test test_utm_preparation_failure_only_warns
run_test test_utm_preparation_retries_transient_ssh_failure
run_test test_utm_preparation_can_be_disabled
run_test test_worker_cycle_prepares_utm_before_config
run_test test_guest_clears_quarantine_and_grants_ssh_automation
run_test test_guest_preparation_is_idempotent
run_test test_guest_verify_launches_and_quits_utm
run_test test_guest_verify_failure_is_fatal
run_test test_guest_unstored_grant_is_fatal
run_test test_guest_rejects_unsafe_bundle_identifier
run_test test_guest_without_utm_is_a_noop_unless_verifying
run_test test_nat_is_default_and_bridged_is_opt_in
run_test test_guest_dns_defaults_to_host_router_with_nat
run_test test_worker_mac_is_stable_unicast_and_per_slot
run_test test_fixed_mac_is_written_to_clone_config
run_test test_fixed_mac_taken_by_running_vm_keeps_random_mac
run_test test_guest_network_receives_router_dns_and_check_urls
run_test test_guest_network_is_skipped_when_bridged_without_check
run_test test_worker_cycle_uses_nat_slot_mac_and_checks_network_first
run_test test_worker_cycle_bridged_opt_in
run_test test_guest_network_sets_dns_and_reports_ready
run_test test_guest_network_dhcp_and_keep
run_test test_guest_network_rejects_bad_input
run_test test_guest_network_dns_failure_names_stage_and_resolver
run_test test_guest_network_https_failure_is_reported
run_test test_guest_network_retries_until_ready

printf 'All macOS Tart runner tests passed.\n'
