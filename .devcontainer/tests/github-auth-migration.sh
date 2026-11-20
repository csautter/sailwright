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
for scenario in legacy replace logout-failure inactive logout-noop stale-retry stale-replace; do
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
store="${GH_STUB_DIR:-$HOME/accounts}"
mkdir -p "$store"
account="$(cat "$store/active" 2>/dev/null)"
while [ "$#" -gt 0 ]; do
    if [ "$1" = --user ]; then account=$2; break; fi
    shift
done
case "$op" in
 'auth status') for f in "$store"/user-*; do [ ! -f "$f" ] || printf '%s\n' "${f##*/user-}"; done ;;
 'auth token') [ -n "$account" ] && [ -s "$store/user-$account" ] && cat "$store/user-$account" || exit 1 ;;
 'auth logout')
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
    case "$scenario" in
        replace|stale-replace) printf new >"$GITHUB_AUTH_DIR/token" ;;
        logout-failure) export FAIL_LOGOUT=1 ;;
        logout-noop) export NOOP_LOGOUT=1 ;;
        inactive) printf own >"$GH_STUB_DIR/active"; printf private >"$GH_STUB_DIR/user-own"; printf old >"$GH_STUB_DIR/user-shared2" ;;
    esac
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
        bash "$AUTH_SCRIPT" container
    fi
    if [ "$scenario" = logout-failure ] || [ "$scenario" = logout-noop ]; then
        [ -s "$HOME/.config/orca-docker/github-token.sha256" ]
        [ "$(cat "$GH_STUB_DIR/user-shared")" = old ]
        unset FAIL_LOGOUT NOOP_LOGOUT
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
printf 'github auth migration: 7 cases and integration checks passed\n'
