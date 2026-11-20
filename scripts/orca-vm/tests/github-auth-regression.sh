#!/usr/bin/env bash
# Offline security regression cases; each case retains its own temporary tree.
# shellcheck disable=SC2016
set -euo pipefail
SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checks=0
for scenario in defaults-host defaults-gh defaults-keys defaults-none defaults-local replace revoke-failure revoke-empty unreadable; do
    case_dir="$(mktemp -d)"
    mkdir -p "$case_dir/repo/.devcontainer" "$case_dir/repo/scripts/orca-vm" "$case_dir/home" "$case_dir/bin"
    printf '{"remoteUser":"root"}\n' >"$case_dir/repo/.devcontainer/devcontainer.json"
    cp "$SRC/docker-lib.sh" "$case_dir/repo/scripts/orca-vm/"
    printf '#!/bin/sh\nexit 0\n' >"$case_dir/bin/docker"
    cat >"$case_dir/bin/gh" <<'EOF'
#!/bin/sh
case "$1 $2" in
 "auth token") [ -s "$HOME/login" ] && cat "$HOME/login" || exit 1 ;;
 "auth logout") [ "${FAIL_LOGOUT:-}" != 1 ] || exit 1; mv "$HOME/login" "$HOME/logged-out" ;;
 "auth login") [ ! -s "$HOME/login" ] || cp "$HOME/login" "$HOME/inactive-token"; cat >"$HOME/login" ;;
 "auth setup-git") : ;;
esac
EOF
    chmod +x "$case_dir/bin/"*
    export HOME="$case_dir/home" PATH="$case_dir/bin:$PATH"
    export GIT_CONFIG_GLOBAL="$HOME/.gitconfig"
    unset ORCA_DOCKER_GITHUB_AUTH ORCA_DOCKER_STATE_DIR ORCA_DOCKER_SSH_KEY ORCA_DOCKER_SKIP_LOCAL_ENV BASH_ENV GIT_SSH_COMMAND
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
            replace|revoke-failure|revoke-empty)
                mkdir -p "$HOME/.config/orca-docker"
                state="$HOME/.config/orca-docker/github-token.sha256"
                printf old >"$HOME/login"
                printf old | sha256 >"$state"
                # sha256 output includes a filename separator; store just the hash.
                digest="$(cut -d ' ' -f1 "$state")"; printf '%s\n' "$digest" >"$state"
                case "$scenario" in
                    replace) printf 'new\n' | sh -c "$_GH_CONTAINER_SCRIPT" _ github.com; [ ! -e "$HOME/inactive-token" ]; [ "$(cat "$HOME/logged-out")" = old ]; [ "$(cat "$HOME/login")" = new ] ;;
                    revoke-failure) rc=0; printf '\n' | FAIL_LOGOUT=1 sh -c "$_GH_CONTAINER_SCRIPT" _ github.com || rc=$?; [ "$rc" = 5 ]; [ -s "$state" ]; [ "$(cat "$HOME/login")" = old ] ;;
                    revoke-empty) git config --global --add url.https://github.com/.insteadOf git@github.com:; mv "$HOME/login" "$HOME/previous-login"; printf '\n' | sh -c "$_GH_CONTAINER_SCRIPT" _ github.com; [ ! -e "$state" ]; rc=0; printf '\n' | sh -c "$_GH_CONTAINER_SCRIPT" _ github.com || rc=$?; [ "$rc" = 4 ]; if git config --global --get-all url.https://github.com/.insteadOf; then exit 1; fi ;;
                esac ;;
            unreadable)
                # Inject an I/O failure independent of root's permission bypass.
                mkdir -p "$STATE_DIR"
                printf token >"$GITHUB_TOKEN_FILE"
                # Stubbed functions are invoked indirectly by share_github_auth.
                # shellcheck disable=SC2317
                tr() { return 1; }
                # shellcheck disable=SC2317
                cexec() { cat >/dev/null; return 4; }
                share_github_auth c1 ;;
        esac
    )
    checks=$((checks + 1))
done
printf 'github auth regression: %s cases passed\n' "$checks"
