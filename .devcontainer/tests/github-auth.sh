#!/usr/bin/env bash
# Tests for .devcontainer/github-auth.sh with a stubbed gh. Needs bash, git and
# sha256sum; HOME, the git config and the token directories are temporary.
#
#   .devcontainer/tests/github-auth.sh
set -uo pipefail

bash "$(dirname "${BASH_SOURCE[0]}")/github-auth-migration.sh" || exit 1

SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/github-auth.sh"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/github-auth-tests.XXXXXX")"
# Temporary fixtures are retained for diagnosis.

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
OVERRIDE="${CONFIG}/sailwright/devcontainer/github-token"
# Stub scope check: prints SCOPES, records that it was asked.
cat >"${TMP}/bin/scopes" <<'EOF'
#!/usr/bin/env bash
read -r t
echo "$t" >>"${GH_STUB_DIR}/scope-calls"
printf '%s\n' "${SCOPES:-}"
EOF
chmod +x "${TMP}/bin/scopes"
host_out() {
    CONFIG_HOME="$CONFIG" GH_CMD="${GH_CMD_TEST:-${TMP}/bin/gh}" GITHUB_SCOPES_CMD="${TMP}/bin/scopes" \
        bash "$SCRIPT" host 2>&1
}
host() { host_out >/dev/null; }
optin() { SAILWRIGHT_GH_SHARE_HOST_LOGIN=1 "$@"; }

printf 'gho_host\r\n' >"${GH_STUB_DIR}/login"
out="$(host_out)"
assert_true "host: without opt-in the host gh login is not shared" test ! -e "${SHARE}/token"
assert_true "host: without opt-in gh is not asked" bash -c "! grep -q '^auth token' '${GH_STUB_DIR}/calls' 2>/dev/null"
assert_true "host: hint names the override file" grep -qF "$OVERRIDE" <<<"$out"
assert_true "host: hint names fine-grained tokens" grep -q 'fine-grained token' <<<"$out"
assert_true "host: hint names the opt-in" grep -q 'SAILWRIGHT_GH_SHARE_HOST_LOGIN=1' <<<"$out"
assert_true "host: share directory created (bind source)" test -d "$SHARE"
assert_true "host: no warning without a shared token" bash -c "! grep -q WARNING <<<\"\$1\"" _ "$out"

SCOPES="repo, workflow, read:org, admin:org"
export SCOPES
out="$(optin host_out)"
assert_eq "host: opt-in shares the host gh login (CR stripped)" "gho_host" "$(cat "${SHARE}/token")"
assert_true "host: opt-in warns about the host login" grep -q "WARNING: sharing the host's gh login" <<<"$out"
assert_true "host: risky scopes named" grep -q 'scopes workflow, admin:org;' <<<"$out"
assert_true "host: token not printed" bash -c "! grep -q gho_host <<<\"\$1\"" _ "$out"
assert_eq "host: scope check gets the token on stdin" "gho_host" "$(tail -n 1 "${GH_STUB_DIR}/scope-calls")"

SCOPES="repo, read:org"
out="$(optin host_out)"
assert_true "host: no scope warning for harmless scopes" bash -c "! grep -q 'has the scopes' <<<\"\$1\"" _ "$out"

printf 'gho_host\n' >"${GH_STUB_DIR}/login"
SCOPES="repo, workflow"
host
assert_true "host: opt-in dropped removes the shared host login" test ! -e "${SHARE}/token"

mkdir -p "${CONFIG}/sailwright/devcontainer"
echo github_pat_scoped >"$OVERRIDE"
: >"${GH_STUB_DIR}/scope-calls"
out="$(host_out)"
assert_eq "host: token file shared without opt-in" "github_pat_scoped" "$(cat "${SHARE}/token")"
assert_true "host: token file shares without a warning" bash -c "! grep -q WARNING <<<\"\$1\"" _ "$out"
assert_true "host: fine-grained token not scope-checked" test ! -s "${GH_STUB_DIR}/scope-calls"
optin host
assert_eq "host: token file wins over the opt-in" "github_pat_scoped" "$(cat "${SHARE}/token")"

echo ghp_classic >"$OVERRIDE"
out="$(host_out)"
assert_true "host: classic token file with risky scopes warns" grep -q 'scopes workflow;' <<<"$out"
assert_true "host: classic token file not printed" bash -c "! grep -q ghp_classic <<<\"\$1\"" _ "$out"

# The built-in check: curl gets the token through its config on stdin only.
mkdir -p "${TMP}/curlbin"
cat >"${TMP}/curlbin/curl" <<'EOF'
#!/usr/bin/env bash
echo "$*" >>"${GH_STUB_DIR}/curl-args"
grep -q '^header = "Authorization: token ghp_classic"$' || exit 22
printf 'HTTP/2 200\r\nx-oauth-scopes: repo, delete_repo\r\n\r\n'
EOF
chmod +x "${TMP}/curlbin/curl"
out="$(CONFIG_HOME="$CONFIG" GH_CMD=none PATH="${TMP}/curlbin:${PATH}" bash "$SCRIPT" host 2>&1)"
assert_true "host: scopes read from the API response header" grep -q 'scopes delete_repo;' <<<"$out"
assert_true "host: token not on the curl command line" bash -c "! grep -q ghp_classic '${GH_STUB_DIR}/curl-args'"

echo 'bad token; rm -rf /' >"$OVERRIDE"
host
assert_true "host: malformed token not shared" test ! -e "${SHARE}/token"

: >"$OVERRIDE"
optin host
assert_true "host: empty token file shares nothing, even with opt-in" test ! -e "${SHARE}/token"

rm -f "$OVERRIDE" "${GH_STUB_DIR}/login"
optin host
assert_true "host: opt-in without a gh login shares nothing" test ! -e "${SHARE}/token"
assert_true "host: no temp files left" test -z "$(ls -A "$SHARE")"

printf 'gho_host\n' >"${GH_STUB_DIR}/login"
optin host
nogh() { GH_CMD_TEST=none host_out; }
assert_true "host: opt-in, no gh keeps the token shared earlier" grep -q 'keeping the token' <<<"$(optin nogh)"
assert_eq "host: kept token unchanged" "gho_host" "$(cat "${SHARE}/token")"
nogh >/dev/null
assert_true "host: no gh and no opt-in removes the token shared earlier" test ! -e "${SHARE}/token"
assert_true "host: opt-in, no gh and no earlier token shares nothing" grep -q 'gh not found' <<<"$(optin nogh)"
assert_true "host: no gh creates no token" test ! -e "${SHARE}/token"
rm -f "${GH_STUB_DIR}/login"
unset SCOPES

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
