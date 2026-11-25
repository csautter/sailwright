#!/usr/bin/env bash
# Offline security regression cases; each case retains its own temporary tree.
# shellcheck disable=SC2016
set -euo pipefail

# Never inherit personal credential paths or launcher configuration into fixtures.
for test_env_name in ${!ORCA_DOCKER_@}; do unset "$test_env_name"; done
unset XDG_CONFIG_HOME CONFIG_HOME GITHUB_AUTH_DIR GITHUB_AUTH_STATE GH_CMD GITHUB_SCOPES_CMD GH_TOKEN GITHUB_TOKEN GH_CONFIG_DIR BASH_ENV
HOME="$(mktemp -d)/home"
export HOME
export XDG_CONFIG_HOME="$HOME/.config" USERPROFILE="" GIT_CONFIG_GLOBAL="$HOME/.gitconfig"
mkdir -p "$HOME"
SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checks=0
for scenario in defaults-host defaults-gh defaults-keys defaults-none defaults-local replace revoke-failure revoke-empty inactive revoke-noop revoke-backend old-gh old-noop old-own old-backend old-empty old-read-empty old-upgrade unreadable; do
    case_dir="$(mktemp -d)"
    mkdir -p "$case_dir/repo/.devcontainer" "$case_dir/repo/scripts/orca-vm" "$case_dir/home" "$case_dir/bin"
    printf '{"remoteUser":"root"}\n' >"$case_dir/repo/.devcontainer/devcontainer.json"
    cp "$SRC/docker-lib.sh" "$case_dir/repo/scripts/orca-vm/"
    printf '#!/bin/sh\nexit 0\n' >"$case_dir/bin/docker"
    cat >"$case_dir/bin/gh" <<'EOF'
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
EOF
    chmod +x "$case_dir/bin/"*
    export HOME="$case_dir/home" PATH="$case_dir/bin:$PATH"
    export GIT_CONFIG_GLOBAL="$HOME/.gitconfig"
    unset XDG_CONFIG_HOME ORCA_DOCKER_GITHUB_TOKEN_FILE ORCA_DOCKER_GITHUB_AUTH ORCA_DOCKER_STATE_DIR ORCA_DOCKER_SSH_KEY ORCA_DOCKER_SKIP_LOCAL_ENV BASH_ENV GIT_SSH_COMMAND
    defaults="$case_dir/repo/scripts/orca-vm/defaults.env"
    case "$scenario" in
        defaults-host|defaults-gh) printf 'ORCA_DOCKER_GITHUB_AUTH=%s\n' "${scenario#defaults-}" >"$defaults" ;;
        defaults-keys) printf 'BASH_ENV=bad\nGIT_SSH_COMMAND=bad\nPATH=bad\nORCA_DOCKER_STATE_DIR=bad\nx}"; touch PWNED; #=1\na b=1\n' >"$defaults" ;;
        defaults-none) printf 'ORCA_DOCKER_GITHUB_AUTH=none' >"$defaults" ;;
        defaults-local) printf 'ORCA_DOCKER_STATE_DIR=%s/state\nORCA_DOCKER_SSH_KEY=%s/key\nORCA_DOCKER_GITHUB_AUTH=host\n' "$case_dir" "$case_dir" >"${defaults%/*}/local.env" ;;
    esac
    (
        # shellcheck disable=SC1091
        . "$case_dir/repo/scripts/orca-vm/docker-lib.sh"
        case "$scenario" in
            defaults-host|defaults-gh) [ "${ORCA_DOCKER_GITHUB_AUTH-unset}" = unset ] ;;
            defaults-keys) [ "${BASH_ENV-unset}" = unset ]; [ "${GIT_SSH_COMMAND-unset}" = unset ]; [ "${ORCA_DOCKER_STATE_DIR-unset}" = unset ]; [ ! -e PWNED ] ;;
            defaults-none) [ "$ORCA_DOCKER_GITHUB_AUTH" = none ] ;;
            defaults-local) [ "$STATE_DIR" = "$case_dir/state" ]; [ "$SSH_KEY" = "$case_dir/key" ]; [ "$GITHUB_TOKEN_FILE" = "$case_dir/state/github-token" ]; [ "$ORCA_DOCKER_GITHUB_AUTH" = host ] ;;
            replace|revoke-failure|revoke-empty|inactive|revoke-noop|revoke-backend|old-*)
                mkdir -p "$HOME/.config/orca-docker"
                state="$HOME/.config/orca-docker/github-token.sha256"
                mkdir -p "$HOME/accounts"
                printf old >"$HOME/accounts/user-shared"
                printf shared >"$HOME/accounts/active"
                printf old | sha256 >"$state"
                # sha256 output includes a filename separator; store just the hash.
                digest="$(cut -d ' ' -f1 "$state")"; printf '%s\n' "$digest" >"$state"
                case "$scenario" in
                    old-*)
                        export OLD_GH=1
                        case "$scenario" in
                            old-noop) export NOOP_LOGOUT=1 ;;
                            old-own|old-upgrade) printf own >"$HOME/accounts/active"; printf private >"$HOME/accounts/user-own" ;;
                            old-backend) export STATUS_ERROR=1 ;;
                            old-read-empty) export EMPTY_READ=1 ;;
                            old-empty) mv "$HOME/accounts/user-shared" "$HOME/accounts/removed-shared" ;;
                        esac
                        rc=0
                        printf '\n' | sh -c "$_GH_CONTAINER_SCRIPT" _ github.com 2>"$case_dir/fallback.log" || rc=$?
                        grep -q 'gh >= 2.81' "$case_dir/fallback.log"
                        case "$scenario" in
                            old-gh|old-empty) [ "$rc" = 0 ]; [ ! -e "$state" ]; [ ! -e "$HOME/accounts/user-shared" ] ;;
                            *) [ "$rc" = 5 ]; [ -s "$state" ] ;;
                        esac
                        if [ "$scenario" = old-upgrade ]; then
                            unset OLD_GH
                            printf '\n' | sh -c "$_GH_CONTAINER_SCRIPT" _ github.com
                            [ ! -e "$state" ]; [ ! -e "$HOME/accounts/user-shared" ]; [ "$(cat "$HOME/accounts/user-own")" = private ]
                        fi ;;
                    replace) printf 'new\n' | sh -c "$_GH_CONTAINER_SCRIPT" _ github.com; [ "$(cat "$HOME/accounts/removed-shared")" = old ]; [ "$(cat "$HOME/accounts/user-shared")" = new ]; [ "$(cat "$state")" = "$(printf new | sha256 | cut -d " " -f1)" ] ;;
                    inactive) printf own >"$HOME/accounts/active"; printf private >"$HOME/accounts/user-own"; printf old >"$HOME/accounts/user-shared2"; printf '\n' | sh -c "$_GH_CONTAINER_SCRIPT" _ github.com; [ ! -e "$state" ]; [ ! -e "$HOME/accounts/user-shared" ]; [ ! -e "$HOME/accounts/user-shared2" ]; [ "$(cat "$HOME/accounts/user-own")" = private ] ;;
                    revoke-backend) rc=0; printf '\n' | NOOP_LOGOUT=1 READ_AFTER_LOGOUT=1 sh -c "$_GH_CONTAINER_SCRIPT" _ github.com || rc=$?; [ "$rc" = 5 ]; [ -s "$state" ]; [ "$(cat "$HOME/accounts/user-shared")" = old ] ;;
                    revoke-noop) for attempt in 1 2; do [ "$attempt" -gt 0 ]; rc=0; printf '\n' | NOOP_LOGOUT=1 sh -c "$_GH_CONTAINER_SCRIPT" _ github.com || rc=$?; [ "$rc" = 5 ]; [ -s "$state" ]; done ;;
                    revoke-failure) rc=0; printf '\n' | FAIL_LOGOUT=1 sh -c "$_GH_CONTAINER_SCRIPT" _ github.com || rc=$?; [ "$rc" = 5 ]; [ -s "$state" ]; [ "$(cat "$HOME/accounts/user-shared")" = old ] ;;
                    revoke-empty) git config --global --add url.https://github.com/.insteadOf git@github.com:; mv "$HOME/accounts/user-shared" "$HOME/accounts/previous-login"; printf '\n' | sh -c "$_GH_CONTAINER_SCRIPT" _ github.com; [ ! -e "$state" ]; rc=0; printf '\n' | sh -c "$_GH_CONTAINER_SCRIPT" _ github.com || rc=$?; [ "$rc" = 4 ]; if git config --global --get-all url.https://github.com/.insteadOf; then exit 1; fi ;;
                esac ;;
            unreadable)
                # Inject an I/O failure independent of root's permission bypass.
                mkdir -p "$STATE_DIR"
                printf token >"$GITHUB_TOKEN_FILE"
                # Stubbed functions are invoked indirectly by share_github_auth.
                # shellcheck disable=SC2317,SC2329
                tr() { return 1; }
                # shellcheck disable=SC2317,SC2329
                cexec() { cat >/dev/null; return 4; }
                share_github_auth c1 ;;
        esac
    )
    checks=$((checks + 1))
done
printf 'github auth regression: %s cases passed\n' "$checks"
