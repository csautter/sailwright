# shellcheck shell=bash
# Release test kit: helpers for deterministic, batched host checks on macOS and Linux.
#
# A phase script sources this file, calls rt_init once, then one rt_* call per check
# and rt_summary at the end. Each check gets an ID from the release test plan, a
# log under <phase>/logs/<ID>.log and one line in <phase>/results.tsv:
#
#   ID  STATUS  SECONDS  DESCRIPTION  DETAIL
#
# STATUS is PASS, FAIL, SKIP or MANUAL. MANUAL checks are instructions for the
# human operator; their result is recorded in the plan report, not here.
#
# Output is redacted before it is written: JSON "password" values and the text
# "Password:" line of `vm info --show-password` never reach a log.

RT_KIT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RT_REPO_ROOT="$(cd "$RT_KIT_DIR/../.." && pwd)"

rt__host() {
	local name
	name="$(hostname 2>/dev/null || uname -n)"
	printf '%s\n' "${name%%.*}"
}

# Match run-owned processes by path. Colima's filesystem probes mention these paths
# too, but are unrelated to builds and must never be selected for cleanup signals.
rt_processes_matching() {
	ps ax -o pid=,pgid=,stat=,command= | awk -v pattern="$1" '
		index($0, pattern) && $0 !~ /grep|ptyctl|unix\.sh/ &&
		$4 !~ /(^|\/)(awk|gawk|mawk|limactl)$/ { print }
	'
}

# Reap a sampler before the next check redirects stderr. Otherwise Bash may write
# a job-termination diagnostic (including the sampler's source) into that log.
rt_stop_sampler() {
	kill "$1" 2>/dev/null || true
	wait "$1" 2>/dev/null || true
}

# rt_wait_lock_holder LOCKS_DIR PID [TRIES]: succeed once a lock file in LOCKS_DIR
# holds the holder record of PID (sailwright writes it after taking the lock and
# truncates it on release). Fail when PID exits first or after TRIES 50 ms polls
# (default 1200). Records of other processes, such as a stale one left by a killed
# holder, do not count.
rt_wait_lock_holder() {
	local tries=0
	while [ "$tries" -lt "${3:-1200}" ] && kill -0 "$2" 2>/dev/null; do
		if grep -qsF "\"pid\":$2," "$1"/*; then return 0; fi
		sleep 0.05
		tries=$((tries + 1))
	done
	return 1
}

# rt_init PHASE: create the phase directory and reset its results.
rt_init() {
	RT_PHASE="$1"
	RT_RUN_DIR="${RT_RUN_DIR:-$RT_KIT_DIR/out/$(rt__host)-$(date +%Y%m%d)}"
	RT_PHASE_DIR="$RT_RUN_DIR/$RT_PHASE"
	RT_RESULTS="$RT_PHASE_DIR/results.tsv"
	mkdir -p "$RT_PHASE_DIR/logs"
	: >"$RT_RESULTS"
	RT_FAILS=0
	RT_PHASE_STARTED="$(date +%s)"
	printf '== phase %s  (results: %s)\n' "$RT_PHASE" "$RT_PHASE_DIR"
}

rt__record() { # ID STATUS SECONDS DESC DETAIL
	local detail="${5//$'\t'/ }"
	detail="${detail//$'\n'/ }"
	printf '%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" "${detail:0:300}" >>"$RT_RESULTS"
	printf '  %-8s %-6s %4ss  %s%s\n' "$1" "$2" "$3" "$4" "${detail:+  -- ${detail:0:160}}"
	if [ "$2" = FAIL ]; then RT_FAILS=$((RT_FAILS + 1)); fi
}

# rt_redact: filter that masks guest credentials.
rt_redact() {
	sed -E \
		-e 's/("password"[[:space:]]*:[[:space:]]*")[^"]*"/\1***"/g' \
		-e 's/^(Password: )[^*].*$/\1***REDACTED***/' \
		-e 's/(ansible_password[=:][[:space:]]*)[^[:space:]]+/\1***/g'
}

# rt__run_timeout SECONDS CMD...: run CMD with stdin from /dev/null; TERM after
# SECONDS, KILL 10 s later. Returns CMD's status, 124 on timeout.
rt__run_timeout() {
	local limit="$1" pid watchdog rc marker
	shift
	marker="$(mktemp "${TMPDIR:-/tmp}/rt-timeout.XXXXXX")"
	rm -f "$marker"
	"$@" </dev/null &
	pid=$!
	(
		sleep "$limit"
		: >"$marker"
		kill -TERM "$pid" 2>/dev/null
		sleep 10
		kill -KILL "$pid" 2>/dev/null
	) &
	watchdog=$!
	wait "$pid"
	rc=$?
	kill "$watchdog" 2>/dev/null
	wait "$watchdog" 2>/dev/null
	if [ -e "$marker" ]; then
		rm -f "$marker"
		return 124
	fi
	return "$rc"
}

# rt_check ID DESC [options] -- CMD...
#   --exit N        expected exit status (default 0); --exit-nonzero accepts any failure
#   --exit-any      do not check the exit status
#   --match RE      extended regex that must match a line of the output (repeatable)
#   --nomatch RE    extended regex that must not match any line (repeatable)
#   --json EXPR     Python expression over d = json.loads(stdout) that must be true
#   --timeout S     seconds before the command is stopped (default 900)
# Prints PASS/FAIL and returns 0/1, so phases can branch on a result.
rt_check() {
	local id="$1" desc="$2"
	shift 2
	local want_exit=0 any_exit=0 nonzero=0 timeout=900 json_expr=""
	local -a match=() nomatch=()
	while [ $# -gt 0 ]; do
		case "$1" in
		--exit) want_exit="$2"; shift 2 ;;
		--exit-any) any_exit=1; shift ;;
		--exit-nonzero) nonzero=1; shift ;;
		--match) match+=("$2"); shift 2 ;;
		--nomatch) nomatch+=("$2"); shift 2 ;;
		--json) json_expr="$2"; shift 2 ;;
		--timeout) timeout="$2"; shift 2 ;;
		--) shift; break ;;
		*) echo "rt_check $id: unknown option $1" >&2; return 2 ;;
		esac
	done
	local arg
	for arg in "$@"; do
		if [ "$arg" = "--show-password" ]; then
			rt__record "$id" FAIL 0 "$desc" "refusing to log a command with --show-password"
			return 1
		fi
	done
	local tmp log started rc problems="" re
	tmp="$(mktemp -d "${TMPDIR:-/tmp}/rt-check.XXXXXX")"
	log="$RT_PHASE_DIR/logs/$id.log"
	started="$(date +%s)"
	rt__run_timeout "$timeout" "$@" >"$tmp/stdout" 2>"$tmp/stderr"
	rc=$?
	{
		printf '$'
		printf ' %q' "$@"
		printf '\n# exit %s\n--- stdout\n' "$rc"
		rt_redact <"$tmp/stdout"
		printf -- '--- stderr\n'
		rt_redact <"$tmp/stderr"
	} >"$log"
	cat "$tmp/stdout" "$tmp/stderr" >"$tmp/all"
	if [ "$rc" = 124 ]; then
		problems="timeout (exit 124; outer limit ${timeout}s)"
	elif [ "$any_exit" = 0 ]; then
		if [ "$nonzero" = 1 ]; then
			[ "$rc" != 0 ] || problems="expected a non-zero exit"
		elif [ "$rc" != "$want_exit" ]; then
			problems="exit $rc, expected $want_exit"
		fi
	fi
	for re in "${match[@]+"${match[@]}"}"; do
		grep -Eq -- "$re" "$tmp/all" || problems="${problems:+$problems; }missing /$re/"
	done
	for re in "${nomatch[@]+"${nomatch[@]}"}"; do
		! grep -Eq -- "$re" "$tmp/all" || problems="${problems:+$problems; }unexpected /$re/"
	done
	if [ -n "$json_expr" ]; then
		python3 -I -c 'import json,sys
d=json.loads(open(sys.argv[1]).read())
sys.exit(0 if eval(sys.argv[2]) else 1)' "$tmp/stdout" "$json_expr" 2>>"$log" ||
			problems="${problems:+$problems; }json check failed: $json_expr"
	fi
	rm -rf "$tmp"
	if [ -z "$problems" ]; then
		rt__record "$id" PASS "$(($(date +%s) - started))" "$desc" ""
		return 0
	fi
	rt__record "$id" FAIL "$(($(date +%s) - started))" "$desc" "$problems (log: logs/$id.log)"
	return 1
}

# rt_assert ID DESC CMD...: a check without output expectations, for shell tests
# such as `test -e file`. The command's output still lands in the log.
rt_assert() {
	local id="$1" desc="$2"
	shift 2
	rt_check "$id" "$desc" -- "$@"
}

rt_pass() { rt__record "$1" PASS 0 "$2" "${3:-}"; }
rt_fail() { rt__record "$1" FAIL 0 "$2" "${3:-}"; }
rt_skip() { rt__record "$1" SKIP 0 "$2" "$3"; }

# rt_manual ID DESC INSTRUCTIONS: a step only a human can perform or judge.
rt_manual() {
	rt__record "$1" MANUAL 0 "$2" "$3"
	{
		printf '%s: %s\n%s\n\n' "$1" "$2" "$3"
	} >>"$RT_PHASE_DIR/manual.txt"
}

rt_note() { printf '  note: %s\n' "$*" | tee -a "$RT_PHASE_DIR/notes.txt"; }

# rt_e2e ID DESC GO_TEST_RUN [ENV=VALUE...]: run deterministic E2E scenarios and copy
# their reports into the phase directory.
rt_e2e() {
	local id="$1" desc="$2" run="$3"
	shift 3
	local out="$RT_PHASE_DIR/e2e-$id"
	mkdir -p "$out"
	rt_check "$id" "$desc" --timeout "${RT_E2E_TIMEOUT:-86400}" --match '^ok[[:space:]]' --match '--- PASS: Test' --nomatch '--- FAIL' -- \
		env SAILWRIGHT_E2E_OUT="$out" "$@" \
		go -C "$RT_REPO_ROOT" test -tags e2e ./test/e2e -run "$run" -count=1 -timeout "${RT_E2E_TIMEOUT:-86400}s" -v
}

# rt_require_disposable: stop unless the operator declared this machine disposable.
rt_require_disposable() {
	if [ "${RT_DISPOSABLE:-}" != 1 ]; then
		echo "This phase changes the machine beyond the test directories." >&2
		echo "Run it only on a disposable VM, with RT_DISPOSABLE=1 in release.env." >&2
		exit 3
	fi
}

# rt_summary: write summary.md and return non-zero if a check failed.
rt_summary() {
	local summary="$RT_PHASE_DIR/summary.md" pass fail skip manual
	pass="$(awk -F'\t' '$2=="PASS"' "$RT_RESULTS" | wc -l | tr -d ' ')"
	fail="$(awk -F'\t' '$2=="FAIL"' "$RT_RESULTS" | wc -l | tr -d ' ')"
	skip="$(awk -F'\t' '$2=="SKIP"' "$RT_RESULTS" | wc -l | tr -d ' ')"
	manual="$(awk -F'\t' '$2=="MANUAL"' "$RT_RESULTS" | wc -l | tr -d ' ')"
	{
		printf '# Phase %s on %s\n\n' "$RT_PHASE" "$(rt__host)"
		printf -- '- finished: %s, duration %ss\n' "$(date '+%Y-%m-%d %H:%M:%S %z')" "$(($(date +%s) - RT_PHASE_STARTED))"
		printf -- '- result: %s passed, %s failed, %s skipped, %s manual\n\n' "$pass" "$fail" "$skip" "$manual"
		printf '| ID | Status | s | Check | Detail |\n| --- | --- | --- | --- | --- |\n'
		awk -F'\t' '{ gsub(/\|/, "\\|"); printf "| %s | %s | %s | %s | %s |\n", $1, $2, $3, $4, $5 }' "$RT_RESULTS"
		if [ -s "$RT_PHASE_DIR/manual.txt" ]; then
			printf '\n## Manual steps\n\n```text\n'
			cat "$RT_PHASE_DIR/manual.txt"
			printf '```\n'
		fi
	} >"$summary"
	printf '== phase %s: %s passed, %s failed, %s skipped, %s manual  -> %s\n' "$RT_PHASE" "$pass" "$fail" "$skip" "$manual" "$summary"
	[ "$fail" = 0 ]
}
