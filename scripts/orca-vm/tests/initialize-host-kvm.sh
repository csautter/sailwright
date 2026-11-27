#!/usr/bin/env bash
# Tests for conditional /dev/kvm passthrough in .devcontainer/initialize-host.sh.
# Uses a throwaway repository copy and temporary HOME/XDG_CONFIG_HOME so the
# real repository and host config are never touched.
set -uo pipefail

for test_env_name in ${!ORCA_DOCKER_@} ${!SAILWRIGHT_@}; do unset "$test_env_name"; done
unset XDG_CONFIG_HOME CONFIG_HOME DEVCONTAINER_KVM_DEVICE BASH_ENV

TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_SRC="$(cd "${TESTS_DIR}/../../.." && pwd)"
INIT_SRC="${REPO_SRC}/.devcontainer/initialize-host.sh"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/init-host-kvm-tests.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

failures=0
passes=0
pass() { passes=$((passes + 1)); }
fail() { failures=$((failures + 1)); echo "FAIL: $*" >&2; }
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
mkdir -p "${REPO}/.devcontainer" "${HOME_DIR}"
cp "$INIT_SRC" "${REPO}/.devcontainer/initialize-host.sh"
cat >"${REPO}/.devcontainer/github-auth.sh" <<'EOF'
#!/usr/bin/env bash
mkdir -p "${CONFIG_HOME}/sailwright/devcontainer/github"
exit 0
EOF
chmod +x "${REPO}/.devcontainer/initialize-host.sh" "${REPO}/.devcontainer/github-auth.sh"

run_init() { # kvm_device
    HOME="$HOME_DIR" XDG_CONFIG_HOME="${HOME_DIR}/.config" USERPROFILE="" \
        DEVCONTAINER_KVM_DEVICE="$1" \
        bash "${REPO}/.devcontainer/initialize-host.sh" 2>&1
}

OUT_YAML="${REPO}/.devcontainer/.generated/compose.local.yaml"

# Case 1: KVM device present -> devices entry emitted and logged as mounted.
FAKE_KVM="${TMP}/fake-kvm"
: >"$FAKE_KVM"
out_present="$(run_init "$FAKE_KVM")"
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
if command -v yq >/dev/null 2>&1; then
    assert_true "initialize-host KVM present: generated YAML parses cleanly" \
        yq -e '.services.dev.devices[0]' "$OUT_YAML" >/dev/null 2>&1
fi

# Case 2: KVM device absent -> no devices block, logged as skipped, exits 0.
MISSING_KVM="${TMP}/no-such-kvm"
out_absent="$(run_init "$MISSING_KVM")"
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

echo "initialize-host KVM tests: ${passes} passed, ${failures} failed"
[ "$failures" -eq 0 ]
