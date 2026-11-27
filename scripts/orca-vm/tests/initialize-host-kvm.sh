#!/usr/bin/env bash
# Tests for conditional /dev/kvm passthrough in .devcontainer/initialize-host.sh.
# Uses a throwaway repository copy, temporary HOME/XDG_CONFIG_HOME, and isolated
# stub binaries (uname, colima, docker) so the real repository, host config,
# and any host colima/docker binaries are never touched.
set -uo pipefail

for test_env_name in ${!ORCA_DOCKER_@} ${!SAILWRIGHT_@}; do unset "$test_env_name"; done
unset XDG_CONFIG_HOME CONFIG_HOME DEVCONTAINER_KVM_DEVICE DEVCONTAINER_HOST_EXTRA_PATH \
    COLIMA_HOME COLIMA_PROFILE DOCKER_HOST DOCKER_CONTEXT WSL_DISTRO_NAME BASH_ENV

TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_SRC="$(cd "${TESTS_DIR}/../../.." && pwd)"
INIT_SRC="${REPO_SRC}/.devcontainer/initialize-host.sh"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/init-host-kvm-tests.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

failures=0
passes=0
pass() { passes=$((passes + 1)); }
fail() { failures=$((failures + 1)); echo "FAIL: $*" >&2; }
assert_eq() { # name expected actual
    if [ "$2" = "$3" ]; then pass; else fail "$1"$'\n'"  expected: $2"$'\n'"  actual:   $3"; fi
}
assert_true() { # name command...
    local name="$1"; shift
    if "$@"; then pass; else fail "$name"; fi
}
assert_false() { # name command...
    local name="$1"; shift
    if "$@"; then fail "$name"; else pass; fi
}

REPO="${TMP}/fixture-repo"
HOME_DIR="${TMP}/home"
STUB_BIN="${TMP}/stub-bin"
NO_COLIMA_BIN="${TMP}/no-colima-bin"
mkdir -p "${REPO}/.devcontainer" "${HOME_DIR}" "${STUB_BIN}" "${NO_COLIMA_BIN}"
cp "$INIT_SRC" "${REPO}/.devcontainer/initialize-host.sh"
cat >"${REPO}/.devcontainer/github-auth.sh" <<'EOF'
#!/usr/bin/env bash
mkdir -p "${CONFIG_HOME}/sailwright/devcontainer/github"
exit 0
EOF
chmod +x "${REPO}/.devcontainer/initialize-host.sh" "${REPO}/.devcontainer/github-auth.sh"

# Stub uname, colima, and docker so host binaries are completely isolated.
cat >"${STUB_BIN}/uname" <<'EOF'
#!/bin/sh
printf '%s\n' "${TEST_UNAME_S:-Linux}"
EOF
cat >"${STUB_BIN}/colima" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"${TEST_COLIMA_LOG}"
if [ "${TEST_DRAIN_STDIN:-0}" = 1 ]; then
    cat >/dev/null
fi
if [ "${TEST_COLIMA_FAIL:-0}" = "1" ]; then
    exit 1
fi
printf '%s\n' "${TEST_COLIMA_OUT:-absent}"
EOF
cat >"${STUB_BIN}/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"${TEST_DOCKER_LOG}"
case "$1 ${2:-}" in
    "context inspect")
        [ "${3:-}" = --format ] || exit 1
        if [ "${TEST_UNAME_S:-Linux}" = Linux ]; then
            printf '%s\n' "${TEST_DOCKER_ENDPOINT:-unix:///var/run/docker.sock}"
        else
            printf '%s\n' "${TEST_DOCKER_ENDPOINT:-unix:///Users/dev/.colima/default/docker.sock}"
        fi
        exit 0
        ;;
    "images -q")
        if [ -n "${TEST_DOCKER_IMAGE:-}" ]; then
            printf '%s\n' "${TEST_DOCKER_IMAGE}"
            exit 0
        fi
        exit 1
        ;;
    "run --rm")
        if [ "${TEST_DRAIN_STDIN:-0}" = 1 ]; then
            cat >/dev/null
        fi
        exit "${TEST_DOCKER_RUN_RC:-125}"
        ;;
esac
exit 1
EOF
chmod +x "${STUB_BIN}/uname" "${STUB_BIN}/colima" "${STUB_BIN}/docker"
cp "${STUB_BIN}/uname" "${STUB_BIN}/docker" "${NO_COLIMA_BIN}/"

COLIMA_LOG="${TMP}/colima.log"
DOCKER_LOG="${TMP}/docker.log"
OUT_YAML="${REPO}/.devcontainer/.generated/compose.local.yaml"
TEST_DEV="/dev/sailwright-test-kvm"

run_init() { # kvm_device [bin_dir]
    local kvm_dev="$1"
    local bin_dir="${2:-$STUB_BIN}"
    rm -f "$COLIMA_LOG" "$DOCKER_LOG"
    HOME="$HOME_DIR" XDG_CONFIG_HOME="${HOME_DIR}/.config" USERPROFILE="" \
        PATH="${bin_dir}:/usr/bin:/bin" \
        DEVCONTAINER_HOST_EXTRA_PATH=0 \
        DEVCONTAINER_KVM_DEVICE="$kvm_dev" COLIMA_HOME=/Users/dev/.colima \
        TEST_COLIMA_LOG="$COLIMA_LOG" \
        TEST_DOCKER_LOG="$DOCKER_LOG" \
        bash "${REPO}/.devcontainer/initialize-host.sh" 2>&1
}

# Case 1: Linux host, KVM device present -> devices entry emitted and logged as mounted.
FAKE_KVM="${TMP}/fake-kvm"
: >"$FAKE_KVM"
out_present="$(TEST_UNAME_S=Linux run_init "$FAKE_KVM")"
rc_present=$?
assert_true "initialize-host KVM present: exits 0" test "$rc_present" -eq 0
assert_true "initialize-host KVM present: compose.local.yaml written" test -f "$OUT_YAML"
assert_true "initialize-host KVM present: devices block emitted" grep -qxF '    devices:' "$OUT_YAML"
assert_true "initialize-host KVM present: maps host device to /dev/kvm" \
    grep -qxF "      - \"${FAKE_KVM}:/dev/kvm\"" "$OUT_YAML"
assert_true "initialize-host KVM present: summary logs mounted device" \
    grep -qF "mounted: device ${FAKE_KVM} -> /dev/kvm" <<<"$out_present"
assert_false "initialize-host KVM present: summary does not mark device skipped" \
    grep -qF "skipped: device " <<<"$out_present"
assert_false "initialize-host KVM present: does not invoke colima" test -s "$COLIMA_LOG"
if command -v yq >/dev/null 2>&1; then
    assert_true "initialize-host KVM present: generated YAML parses cleanly" \
        yq -e '.services.dev.devices[0]' "$OUT_YAML" >/dev/null 2>&1
fi

# Case 2: Linux host, KVM device absent -> no devices block, logged as skipped, no colima probe.
MISSING_KVM="${TMP}/no-such-kvm"
out_absent="$(TEST_UNAME_S=Linux run_init "$MISSING_KVM")"
rc_absent=$?
assert_true "initialize-host KVM absent: exits 0" test "$rc_absent" -eq 0
assert_true "initialize-host KVM absent: compose.local.yaml written" test -f "$OUT_YAML"
assert_false "initialize-host KVM absent: omits devices block" \
    grep -q '^    devices:' "$OUT_YAML"
assert_false "initialize-host KVM absent: omits /dev/kvm reference" \
    grep -q '/dev/kvm' "$OUT_YAML"
assert_true "initialize-host KVM absent: summary logs skipped device" \
    grep -qF "skipped: device ${MISSING_KVM} (not found)" <<<"$out_absent"
if command -v yq >/dev/null 2>&1; then
    assert_true "initialize-host KVM absent: generated YAML parses cleanly" \
        yq -e '.services.dev.volumes' "$OUT_YAML" >/dev/null 2>&1
fi

out_linux_dev="$(TEST_UNAME_S=Linux TEST_COLIMA_OUT=present run_init "$TEST_DEV")"
rc_linux_dev=$?
assert_eq "initialize-host Linux absent /dev/*: exits 0" "0" "$rc_linux_dev"
assert_false "initialize-host Linux absent /dev/*: omits devices block" grep -q '^    devices:' "$OUT_YAML"
assert_false "initialize-host Linux absent /dev/*: does not probe colima" test -s "$COLIMA_LOG"
assert_true "initialize-host Linux absent /dev/*: logs skipped" \
    grep -qF "skipped: device ${TEST_DEV} (not found)" <<<"$out_linux_dev"

# Case 3: Darwin + Colima VM has /dev/kvm (M3+/M5 --nested-virtualization) -> emitted, probed once.
out_darwin_present="$(TEST_UNAME_S=Darwin TEST_COLIMA_OUT=present run_init "$TEST_DEV")"
rc_darwin_present=$?
assert_eq "initialize-host Darwin+Colima present: exits 0" "0" "$rc_darwin_present"
assert_true "initialize-host Darwin+Colima present: devices block emitted" grep -qxF '    devices:' "$OUT_YAML"
assert_true "initialize-host Darwin+Colima present: maps device to /dev/kvm" \
    grep -qxF "      - \"${TEST_DEV}:/dev/kvm\"" "$OUT_YAML"
assert_true "initialize-host Darwin+Colima present: summary logs mounted device" \
    grep -qF "mounted: device ${TEST_DEV} -> /dev/kvm" <<<"$out_darwin_present"
assert_eq "initialize-host Darwin+Colima present: probes colima exactly once" "1" \
    "$(wc -l <"$COLIMA_LOG" | tr -d ' ')"
assert_true "initialize-host Darwin+Colima present: passes default profile and positional device path" \
    grep -qF -- "--profile default ssh -- sh -c if [ -e \"\$1\" ]; then echo present; else echo absent; fi _ ${TEST_DEV}" "$COLIMA_LOG"
if command -v yq >/dev/null 2>&1; then
    assert_true "initialize-host Darwin+Colima present: generated YAML parses cleanly" \
        yq -e '.services.dev.devices[0]' "$OUT_YAML" >/dev/null 2>&1
fi

# Case 4: Darwin + Colima VM lacks /dev/kvm (M2) -> omitted, logged skipped, no docker fallback.
out_darwin_absent="$(TEST_UNAME_S=Darwin TEST_COLIMA_OUT=absent TEST_DOCKER_IMAGE=img1 TEST_DOCKER_RUN_RC=0 run_init "$TEST_DEV")"
rc_darwin_absent=$?
assert_eq "initialize-host Darwin+Colima absent: exits 0" "0" "$rc_darwin_absent"
assert_false "initialize-host Darwin+Colima absent: omits devices block" grep -q '^    devices:' "$OUT_YAML"
assert_true "initialize-host Darwin+Colima absent: logs skipped device" \
    grep -qF "skipped: device ${TEST_DEV} (not found)" <<<"$out_darwin_absent"
assert_eq "initialize-host Darwin+Colima absent: probes colima exactly once" "1" \
    "$(wc -l <"$COLIMA_LOG" | tr -d ' ')"
assert_false "initialize-host Darwin+Colima absent: does not run docker fallback after definitive absent" \
    grep -q '^run ' "$DOCKER_LOG"

# Case 5: Select the actual Docker endpoint, even with a renamed context.
DOCKER_HOST="unix:///Users/dev/.colima/custom-kvm/docker.sock" TEST_UNAME_S=Darwin TEST_COLIMA_OUT=present run_init "$TEST_DEV" >/dev/null
assert_true "initialize-host Colima profile from DOCKER_HOST" \
    grep -q '^--profile custom-kvm ssh ' "$COLIMA_LOG"

DOCKER_CONTEXT=renamed DOCKER_HOST=tcp://wrong:2375 COLIMA_PROFILE=wrong \
    TEST_DOCKER_ENDPOINT=unix:///Users/dev/.colima/work/docker.sock \
    TEST_UNAME_S=Darwin TEST_COLIMA_OUT=present run_init "$TEST_DEV" >/dev/null
assert_true "initialize-host context socket overrides DOCKER_HOST and COLIMA_PROFILE" \
    grep -q '^--profile work ssh ' "$COLIMA_LOG"

TEST_DOCKER_ENDPOINT='unix:///Users/dev/.colima/-bad;id/docker.sock' \
    TEST_UNAME_S=Darwin TEST_COLIMA_OUT=present run_init "$TEST_DEV" >/dev/null
assert_false "initialize-host invalid socket profile: doesn't query Colima" test -s "$COLIMA_LOG"

# Colima's availability cannot affect a different active engine.
for docker_rc in 0 125; do
    colima_out=present
    if [ "$docker_rc" = 0 ]; then colima_out=absent; fi
    DOCKER_CONTEXT=desktop-linux DOCKER_HOST=unix:///Users/dev/.colima/default/docker.sock COLIMA_PROFILE=default \
        TEST_DOCKER_ENDPOINT=unix:///Users/dev/.docker/run/docker.sock \
        TEST_UNAME_S=Darwin TEST_COLIMA_OUT="$colima_out" TEST_DOCKER_IMAGE=localimg TEST_DOCKER_RUN_RC="$docker_rc" \
        run_init "$TEST_DEV" >/dev/null
    assert_false "initialize-host mixed engines: no unrelated Colima VM queried" test -s "$COLIMA_LOG"
    assert_true "initialize-host mixed engines: active Docker queried" grep -q '^run --rm' "$DOCKER_LOG"
    if [ "$docker_rc" = 0 ]; then
        assert_true "initialize-host mixed engines: active Docker device available" grep -qxF '    devices:' "$OUT_YAML"
    else
        assert_false "initialize-host mixed engines: active Docker device absent" grep -qxF '    devices:' "$OUT_YAML"
    fi
done

# Probes must not consume the initializer's inherited input either.
for endpoint in unix:///Users/dev/.colima/default/docker.sock unix:///Users/dev/.docker/run/docker.sock; do
    {
        DOCKER_HOST="$endpoint" TEST_UNAME_S=Darwin TEST_DRAIN_STDIN=1 TEST_COLIMA_OUT=present \
            TEST_DOCKER_IMAGE=localimg TEST_DOCKER_RUN_RC=0 run_init "$TEST_DEV" >/dev/null
        IFS= read -r remaining
    } <<<"operator input"
    assert_eq "initialize-host draining probe leaves stdin untouched" "operator input" "$remaining"
done

# Case 6: Darwin + unreachable/failing colima or missing colima -> skips cleanly without failing startup.
out_fail="$(TEST_UNAME_S=Darwin TEST_COLIMA_FAIL=1 run_init "$TEST_DEV")"
rc_fail=$?
assert_eq "initialize-host failing colima: exits 0" "0" "$rc_fail"
assert_false "initialize-host failing colima: omits devices block" grep -q '^    devices:' "$OUT_YAML"
assert_true "initialize-host failing colima: logs skipped device" \
    grep -qF "skipped: device ${TEST_DEV} (not found)" <<<"$out_fail"

out_nocolima="$(TEST_UNAME_S=Darwin run_init "$TEST_DEV" "$NO_COLIMA_BIN")"
rc_nocolima=$?
assert_eq "initialize-host missing colima binary: exits 0" "0" "$rc_nocolima"
assert_false "initialize-host missing colima binary: omits devices block" grep -q '^    devices:' "$OUT_YAML"
assert_true "initialize-host missing colima binary: logs skipped device" \
    grep -qF "skipped: device ${TEST_DEV} (not found)" <<<"$out_nocolima"

# Case 7: Darwin + non-Colima Docker VM fallback using local image (no pull).
out_docker_ok="$(TEST_UNAME_S=Darwin TEST_DOCKER_IMAGE=localimg123 TEST_DOCKER_RUN_RC=0 run_init "$TEST_DEV" "$NO_COLIMA_BIN")"
rc_docker_ok=$?
assert_eq "initialize-host Docker VM fallback present: exits 0" "0" "$rc_docker_ok"
assert_true "initialize-host Docker VM fallback present: emits devices block" grep -qxF '    devices:' "$OUT_YAML"
assert_true "initialize-host Docker VM fallback present: logs mounted device" \
    grep -qF "mounted: device ${TEST_DEV} -> /dev/kvm" <<<"$out_docker_ok"

out_docker_no="$(TEST_UNAME_S=Darwin TEST_DOCKER_IMAGE=localimg123 TEST_DOCKER_RUN_RC=125 run_init "$TEST_DEV" "$NO_COLIMA_BIN")"
rc_docker_no=$?
assert_eq "initialize-host Docker VM fallback absent: exits 0" "0" "$rc_docker_no"
assert_false "initialize-host Docker VM fallback absent: omits devices block" grep -q '^    devices:' "$OUT_YAML"
assert_true "initialize-host Docker VM fallback absent: logs skipped device" \
    grep -qF "skipped: device ${TEST_DEV} (not found)" <<<"$out_docker_no"

# Case 8: Default PATH augmentation (DEVCONTAINER_HOST_EXTRA_PATH unset) succeeds
# under set -euo pipefail even when /opt/homebrew/bin does not exist.
out_default_path="$(
    HOME="$HOME_DIR" XDG_CONFIG_HOME="${HOME_DIR}/.config" USERPROFILE="" \
        PATH="${STUB_BIN}:/usr/bin:/bin" \
        DEVCONTAINER_KVM_DEVICE="$FAKE_KVM" \
        TEST_UNAME_S=Linux \
        TEST_COLIMA_LOG="$COLIMA_LOG" \
        TEST_DOCKER_LOG="$DOCKER_LOG" \
        bash "${REPO}/.devcontainer/initialize-host.sh" 2>&1
)"
rc_default_path=$?
assert_eq "initialize-host default PATH augmentation: exits 0" "0" "$rc_default_path"
assert_true "initialize-host default PATH augmentation: mounts local KVM device" \
    grep -qF "mounted: device ${FAKE_KVM} -> /dev/kvm" <<<"$out_default_path"

echo "initialize-host KVM tests: ${passes} passed, ${failures} failed"
[ "$failures" -eq 0 ]
