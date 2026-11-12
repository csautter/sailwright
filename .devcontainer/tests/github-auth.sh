#!/usr/bin/env bash
# Tests for .devcontainer/github-auth.sh with a stubbed gh. Needs bash, git and
# sha256sum; HOME, the git config and the token directories are temporary.
#
#   .devcontainer/tests/github-auth.sh
set -uo pipefail

SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/github-auth.sh"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/github-auth-tests.XXXXXX")"
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

# Stub gh: the login is a file, every call is logged.
mkdir -p "${TMP}/bin" "${TMP}/home"
cat >"${TMP}/bin/gh" <<'EOF'
#!/usr/bin/env bash
echo "$*" >>"${GH_STUB_DIR}/calls"
login="${GH_STUB_DIR}/login"
case "$1 $2" in
    "auth token") [ -s "$login" ] && cat "$login" || exit 1 ;;
    "auth login") read -r t; [ "$t" != bad ] || exit 1; printf '%s\n' "$t" >"$login" ;;
    "auth logout") rm -f "$login" ;;
    "auth setup-git") ;;
    *) exit 3 ;;
esac
EOF
chmod +x "${TMP}/bin/gh"
export GH_STUB_DIR="${TMP}/gh"
mkdir -p "$GH_STUB_DIR"

# --- host --------------------------------------------------------------------------
CONFIG="${TMP}/config"
SHARE="${CONFIG}/sailwright/devcontainer/github"
host() { CONFIG_HOME="$CONFIG" GH_CMD="${TMP}/bin/gh" bash "$SCRIPT" host >/dev/null 2>&1; }

printf 'gho_host\r\n' >"${GH_STUB_DIR}/login"
host
assert_eq "host: token of the host gh login shared (CR stripped)" "gho_host" "$(cat "${SHARE}/token")"

mkdir -p "${CONFIG}/sailwright/devcontainer"
echo github_pat_scoped >"${CONFIG}/sailwright/devcontainer/github-token"
host
assert_eq "host: token file wins over the gh login" "github_pat_scoped" "$(cat "${SHARE}/token")"

echo 'bad token; rm -rf /' >"${CONFIG}/sailwright/devcontainer/github-token"
host
assert_true "host: malformed token not shared" test ! -e "${SHARE}/token"

: >"${CONFIG}/sailwright/devcontainer/github-token"
host
assert_true "host: empty token file shares nothing" test ! -e "${SHARE}/token"

rm -f "${CONFIG}/sailwright/devcontainer/github-token" "${GH_STUB_DIR}/login"
host
assert_true "host: no gh login shares nothing" test ! -e "${SHARE}/token"
assert_true "host: share directory still created (bind source)" test -d "$SHARE"
assert_true "host: no temp files left" test -z "$(ls -A "$SHARE")"

printf 'gho_host\n' >"${GH_STUB_DIR}/login"
host
nogh() { CONFIG_HOME="$CONFIG" GH_CMD=none bash "$SCRIPT" host 2>&1; }
assert_true "host: no gh keeps the token shared earlier" grep -q 'keeping the token' <<<"$(nogh)"
assert_eq "host: kept token unchanged" "gho_host" "$(cat "${SHARE}/token")"
rm -f "${SHARE}/token"
assert_true "host: no gh and no earlier token shares nothing" grep -q 'gh not found' <<<"$(nogh)"
assert_true "host: no gh creates no token" test ! -e "${SHARE}/token"
rm -f "${GH_STUB_DIR}/login"

# --- container ---------------------------------------------------------------------
TOKENS="${TMP}/run"
mkdir -p "$TOKENS"
export GIT_CONFIG_GLOBAL="${TMP}/home/.gitconfig"
container() {
    HOME="${TMP}/home" PATH="${TMP}/bin:${PATH}" GITHUB_AUTH_DIR="$TOKENS" \
        bash "$SCRIPT" container >/dev/null 2>&1
}
rewrites() { git config --global --get-all url.https://github.com/.insteadOf 2>/dev/null | sort | tr '\n' ' '; }
state="${TMP}/home/.config/sailwright/github-token.sha256"

container
assert_true "container: no token, no login" test ! -e "${GH_STUB_DIR}/login"
assert_eq "container: no token, no rewrite" "" "$(rewrites)"

echo gho_shared >"${TOKENS}/token"
: >"${GH_STUB_DIR}/calls"
container
assert_eq "container: logged in with the shared token" "gho_shared" "$(cat "${GH_STUB_DIR}/login")"
assert_true "container: gh set up as git credential helper" grep -q '^auth setup-git' "${GH_STUB_DIR}/calls"
assert_eq "container: SSH remotes rewritten to HTTPS" "git@github.com: ssh://git@github.com/ " "$(rewrites)"
assert_true "container: shared login recorded" test -s "$state"

: >"${GH_STUB_DIR}/calls"
container
assert_true "container: same token, no new login" bash -c "! grep -q '^auth login' '${GH_STUB_DIR}/calls'"
assert_eq "container: rewrite not duplicated" "git@github.com: ssh://git@github.com/ " "$(rewrites)"

echo gho_rotated >"${TOKENS}/token"
container
assert_eq "container: rotated token logged in" "gho_rotated" "$(cat "${GH_STUB_DIR}/login")"

rm -f "${TOKENS}/token"
container
assert_true "container: shared login logged out when the token is gone" test ! -e "${GH_STUB_DIR}/login"
assert_eq "container: rewrite removed with the shared login" "" "$(rewrites)"
assert_true "container: record removed" test ! -e "$state"

echo gho_own >"${GH_STUB_DIR}/login"
container
assert_eq "container: own login kept without a shared token" "gho_own" "$(cat "${GH_STUB_DIR}/login")"

echo gho_shared >"${TOKENS}/token"
container
echo gho_own >"${GH_STUB_DIR}/login"
rm -f "${TOKENS}/token"
container
assert_eq "container: login changed in the container is not logged out" "gho_own" "$(cat "${GH_STUB_DIR}/login")"

rm -f "${GH_STUB_DIR}/login"
echo bad >"${TOKENS}/token"
container
assert_true "container: failed login leaves no record" test ! -e "$state"

echo "github-auth tests: ${passes} passed, ${failures} failed"
[ "$failures" -eq 0 ]
