#!/usr/bin/env bash
# Offline migration tests using the real resume and running-workspace paths.
# shellcheck disable=SC2016
set -euo pipefail

# Never inherit personal credential paths or launcher configuration into fixtures.
for test_env_name in ${!ORCA_DOCKER_@} ${!SAILWRIGHT_@}; do unset "$test_env_name"; done
unset XDG_CONFIG_HOME CONFIG_HOME GITHUB_AUTH_DIR GITHUB_AUTH_STATE GH_CMD GITHUB_SCOPES_CMD GH_TOKEN GITHUB_TOKEN GH_CONFIG_DIR BASH_ENV
HOME="$(mktemp -d)/home"
export HOME
export XDG_CONFIG_HOME="$HOME/.config" USERPROFILE="" GIT_CONFIG_GLOBAL="$HOME/.gitconfig"
mkdir -p "$HOME"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
for scenario in legacy replace logout-failure inactive logout-noop logout-backend stale-retry stale-replace old-gh old-noop old-own old-backend old-empty old-read-empty old-upgrade lifecycle-retry lifecycle-success lifecycle-error; do
    case_dir="$(mktemp -d)"
    export CASE_DIR="$case_dir" AUTH_SCRIPT="$ROOT/.devcontainer/github-auth.sh"
    export HOME="$case_dir/home" CONFIG_HOME="$case_dir/config" GH_STUB_DIR="$case_dir/gh"
    export GIT_CONFIG_GLOBAL="$HOME/.gitconfig"
    export GITHUB_AUTH_DIR="$CONFIG_HOME/sailwright/devcontainer/github" GITHUB_SCOPES_CMD=none GH_CMD=none
    unset SAILWRIGHT_GH_SHARE_HOST_LOGIN
    mkdir -p "$HOME" "$GITHUB_AUTH_DIR" "$GH_STUB_DIR" "$case_dir/bin" "$case_dir/scripts"
    cat >"$case_dir/bin/gh" <<'STUB'
#!/bin/sh
op="$1 $2"
json=false
for arg in "$@"; do [ "$arg" != --json ] || json=true; done
store="${GH_STUB_DIR:-$HOME/accounts}"
mkdir -p "$store"
account="$(cat "$store/active" 2>/dev/null)"
while [ "$#" -gt 0 ]; do
    if [ "$1" = --user ]; then account=$2; break; fi
    shift
done
case "$op" in
 'auth status')
    if [ "${OLD_GH:-}" = 1 ]; then
        if $json; then echo 'unknown flag: --json' >&2; exit 2; fi
        if [ "${STATUS_ERROR:-}" = 1 ]; then echo 'credential backend unavailable' >&2; exit 1; fi
        if [ -s "$store/user-$account" ]; then exit 0; fi
        echo 'You are not logged into any GitHub hosts.' >&2; exit 1
    fi
    for f in "$store"/user-*; do [ ! -f "$f" ] || printf '%s\n' "${f##*/user-}"; done ;;
 'auth token')
    [ "${EMPTY_READ:-}" != 1 ] || exit 0
    if [ "${READ_AFTER_LOGOUT:-}" = 1 ] && [ -f "$store/logout-attempt" ]; then exit 1; fi
    [ "${READ_ERROR:-}" != 1 ] || exit 1; [ -n "$account" ] && [ -s "$store/user-$account" ] && cat "$store/user-$account" || exit 1 ;;
 'auth logout')
    touch "$store/logout-attempt"
    [ "${FAIL_LOGOUT:-}" != 1 ] || exit 1
    [ "${NOOP_LOGOUT:-}" != 1 ] || exit 0
    mv "$store/user-$account" "$store/removed-$account"
    if [ "$(cat "$store/active")" = "$account" ]; then printf own >"$store/active"; fi ;;
 'auth login') cat >"$store/user-shared"; printf shared >"$store/active" ;;
 'auth setup-git') : ;;
 *) exit 2 ;;
esac
STUB
    chmod +x "$case_dir/bin/gh"
    export PATH="$case_dir/bin:$PATH"
    git config --global --add url.https://github.com/.insteadOf git@github.com:
    printf old >"$GH_STUB_DIR/user-shared"; printf shared >"$GH_STUB_DIR/active"
    mkdir -p "$HOME/.config/orca-docker"
    if command -v sha256sum >/dev/null; then digest="$(printf old | sha256sum)"; else digest="$(printf old | shasum -a 256)"; fi
    printf '%s\n' "${digest%% *}" >"$HOME/.config/orca-docker/github-token.sha256"
    unset OLD_GH NOOP_LOGOUT STATUS_ERROR READ_ERROR EMPTY_READ READ_AFTER_LOGOUT
    case "$scenario" in
        old-*) export OLD_GH=1
            case "$scenario" in
                old-noop) export NOOP_LOGOUT=1 ;;
                old-own|old-upgrade) printf own >"$GH_STUB_DIR/active"; printf private >"$GH_STUB_DIR/user-own" ;;
                old-backend) export STATUS_ERROR=1 ;;
                old-read-empty) export EMPTY_READ=1 ;;
                old-empty) mv "$GH_STUB_DIR/user-shared" "$GH_STUB_DIR/removed-shared" ;;
            esac ;;
        replace|stale-replace) printf new >"$GITHUB_AUTH_DIR/token" ;;
        logout-failure) export FAIL_LOGOUT=1 ;;
        logout-noop) export NOOP_LOGOUT=1 ;;
        logout-backend) export NOOP_LOGOUT=1 READ_AFTER_LOGOUT=1 ;;
        inactive) printf own >"$GH_STUB_DIR/active"; printf private >"$GH_STUB_DIR/user-own"; printf old >"$GH_STUB_DIR/user-shared2" ;;
    esac
    if [[ "$scenario" = lifecycle-* ]]; then
        mkdir -p "$case_dir/repo/.devcontainer" "$case_dir/repo/scripts/orca-vm" "$case_dir/old-checkout/.devcontainer" "$HOME/.config/sailwright"
        cp "$ROOT/scripts/orca-vm/docker-lib.sh" "$ROOT/scripts/orca-vm/docker-resume.sh" "$case_dir/repo/scripts/orca-vm/"
        cp "$ROOT/.devcontainer/github-auth.sh" "$case_dir/repo/.devcontainer/"
        cp "$ROOT/.devcontainer/tests/fixtures/github-auth-legacy.sh" "$case_dir/old-checkout/.devcontainer/github-auth.sh"
        # Both historical marker paths must survive the old hook, including its failure.
        cp "$HOME/.config/orca-docker/github-token.sha256" "$HOME/.config/sailwright/github-token.sha256"
        if [ "$scenario" != lifecycle-retry ]; then
            mv "$HOME/.config/orca-docker/github-token.sha256" "$case_dir/original-legacy-marker"
        fi
        cat >"$case_dir/repo/.devcontainer/devcontainer.json" <<'JSON'
{"remoteUser":"root", "initializeCommand":"exit 99", "postStartCommand":"echo service >>\"$CASE_DIR/events\"; FAIL_LOGOUT=1 bash .devcontainer/github-auth.sh container; test \"${HOOK_ERROR:-0}\" = 0"}
JSON
        cat >"$case_dir/overrides.sh" <<'STUB'
read_payload() { RESOURCE_ID=c1; PAYLOAD_PORT=''; PAYLOAD_IMAGE=fixture; }
assert_owned() { :; }
ensure_ssh_key() { :; }
bring_up() { BROUGHT_UP_PORT=2222; BROUGHT_UP_HOST_KEY=fixture; }
emit_result() { :; }
share_github_auth() { :; }
share_opencode_auth() { :; }
cexec() {
    if [ "$1" = -i ]; then shift; fi
    if [ "$1" = -w ]; then shift 2; fi
    shift
    if [ "$1" = bash ] && [ "${2:-}" = -lc ]; then shift 2; set -- bash -c "$@"; fi
    (cd "$CASE_DIR/old-checkout" && "$@")
}
STUB
        printf '\n. "$CASE_DIR/overrides.sh"\n' >>"$case_dir/repo/scripts/orca-vm/docker-lib.sh"
        printf '#!/bin/sh\nexit 0\n' >"$case_dir/bin/docker"; chmod +x "$case_dir/bin/docker"
        if [ "$scenario" = lifecycle-retry ]; then export FAIL_LOGOUT=1; fi
        if [ "$scenario" = lifecycle-error ]; then export HOOK_ERROR=1; fi
        bash "$case_dir/repo/scripts/orca-vm/docker-resume.sh" >"$case_dir/resume.log" 2>&1
        [ "$(cat "$case_dir/events")" = service ]
        if [ "$scenario" = lifecycle-retry ]; then
            [ -s "$HOME/.config/sailwright/github-token.sha256" ]; [ -s "$HOME/.config/orca-docker/github-token.sha256" ]
            [ "$(cat "$GH_STUB_DIR/user-shared")" = old ]
            unset FAIL_LOGOUT
            bash "$case_dir/repo/scripts/orca-vm/docker-resume.sh" >>"$case_dir/resume.log" 2>&1
        fi
        [ ! -e "$HOME/.config/sailwright/github-token.sha256" ]; [ ! -e "$HOME/.config/orca-docker/github-token.sha256" ]
        [ ! -e "$GH_STUB_DIR/user-shared" ]
        unset HOOK_ERROR
        continue
    fi
    if [ "$scenario" = stale-retry ] || [ "$scenario" = stale-replace ]; then
        mkdir -p "$case_dir/old-checkout/.devcontainer"
        # Frozen deployed implementation: independent checkout, no git history
        # needed at test time. It discards the regular marker on logout failure.
        cp "$ROOT/.devcontainer/tests/fixtures/github-auth-legacy.sh" "$case_dir/old-checkout/.devcontainer/github-auth.sh"
        mkdir -p "$HOME/.config/sailwright"
        cp "$HOME/.config/orca-docker/github-token.sha256" "$HOME/.config/sailwright/github-token.sha256"
        sed -n '/^refresh_github_auth() {/,/^}/p' "$ROOT/scripts/orca-vm/docker-lib.sh" >"$case_dir/refresh.sh"
        # shellcheck disable=SC1091
        . "$case_dir/refresh.sh"
        export DEVCONTAINER_DIR="$ROOT/.devcontainer"
        # shellcheck disable=SC2317
        cexec() { [ "$1" = -i ]; shift 2; (cd "$CASE_DIR/old-checkout" && "$@"); }
        if [ "$scenario" = stale-retry ]; then
            FAIL_LOGOUT=1 refresh_github_auth c1
            [ -s "$HOME/.config/orca-docker/github-token.sha256" ]
            [ -s "$HOME/.config/sailwright/github-token.sha256" ]
            [ "$(cat "$GH_STUB_DIR/user-shared")" = old ]
        else
            printf new >"$CONFIG_HOME/sailwright/devcontainer/github-token"
        fi
        refresh_github_auth c1
        [ ! -e "$case_dir/stale-ran" ]
    else
        bash "$AUTH_SCRIPT" container >"$case_dir/auth.log" 2>&1
    fi
    case "$scenario" in
        old-*)
            grep -q 'gh >= 2.81' "$case_dir/auth.log"
            case "$scenario" in
                old-gh|old-empty) [ ! -e "$HOME/.config/orca-docker/github-token.sha256" ]; [ ! -e "$GH_STUB_DIR/user-shared" ] ;;
                *) [ -s "$HOME/.config/orca-docker/github-token.sha256" ] ;;
            esac
            if [ "$scenario" = old-upgrade ]; then
                unset OLD_GH
                bash "$AUTH_SCRIPT" container
                [ ! -e "$HOME/.config/orca-docker/github-token.sha256" ]; [ ! -e "$GH_STUB_DIR/user-shared" ]; [ "$(cat "$GH_STUB_DIR/user-own")" = private ]
            fi
            continue ;;
    esac
    if [ "$scenario" = logout-failure ] || [ "$scenario" = logout-noop ] || [ "$scenario" = logout-backend ]; then
        [ -s "$HOME/.config/orca-docker/github-token.sha256" ]
        [ "$(cat "$GH_STUB_DIR/user-shared")" = old ]
        unset FAIL_LOGOUT NOOP_LOGOUT READ_AFTER_LOGOUT
        bash "$AUTH_SCRIPT" container
        [ ! -e "$HOME/.config/orca-docker/github-token.sha256" ]
        [ ! -e "$GH_STUB_DIR/user-shared" ]
    else
        [ ! -e "$HOME/.config/orca-docker/github-token.sha256" ]
        if [ "$scenario" = inactive ]; then [ "$(cat "$GH_STUB_DIR/user-own")" = private ]; [ ! -e "$GH_STUB_DIR/user-shared2" ]; fi
        if [ "$scenario" = replace ] || [ "$scenario" = stale-replace ]; then
            [ "$(cat "$GH_STUB_DIR/user-shared")" = new ]
        else
            [ ! -e "$GH_STUB_DIR/user-shared" ]
            if [ "$scenario" != inactive ] && git config --global --get-all url.https://github.com/.insteadOf; then exit 1; fi
        fi
    fi
done
# Pin repository integration and migration guidance, as well as the shell logic.
[ "$(sed -n '/^ORCA_DOCKER_GITHUB_AUTH=/p' "$ROOT/scripts/orca-vm/defaults.env")" = ORCA_DOCKER_GITHUB_AUTH=none ]
grep -q 'revoke.*GitHub' "$ROOT/.devcontainer/README.md"
grep -q '^test-devcontainer-scripts:' "$ROOT/Makefile"
grep -q 'make test-devcontainer-scripts' "$ROOT/.github/workflows/shellcheck.yml"
printf 'github auth migration: 18 cases and integration checks passed\n'
