#!/usr/bin/env bash
# Offline migration tests using the real resume and running-workspace paths.
# shellcheck disable=SC2016
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
for scenario in resume reattach legacy replace logout-failure; do
    case_dir="$(mktemp -d)"
    export CASE_DIR="$case_dir" AUTH_SCRIPT="$ROOT/.devcontainer/github-auth.sh"
    export HOME="$case_dir/home" CONFIG_HOME="$case_dir/config" GH_STUB_DIR="$case_dir/gh"
    export GIT_CONFIG_GLOBAL="$HOME/.gitconfig"
    export GITHUB_AUTH_DIR="$CONFIG_HOME/sailwright/devcontainer/github" GITHUB_SCOPES_CMD=none GH_CMD=none
    unset SAILWRIGHT_GH_SHARE_HOST_LOGIN
    mkdir -p "$HOME" "$GITHUB_AUTH_DIR" "$GH_STUB_DIR" "$case_dir/bin" "$case_dir/scripts"
    cat >"$case_dir/bin/gh" <<'STUB'
#!/bin/sh
case "$1 $2" in
 'auth token') [ -s "$GH_STUB_DIR/login" ] && cat "$GH_STUB_DIR/login" || exit 1 ;;
 'auth logout') [ "${FAIL_LOGOUT:-}" != 1 ] || exit 1; mv "$GH_STUB_DIR/login" "$GH_STUB_DIR/logged-out" ;;
 'auth login') [ ! -s "$GH_STUB_DIR/login" ] || cp "$GH_STUB_DIR/login" "$GH_STUB_DIR/inactive"; cat >"$GH_STUB_DIR/login" ;;
 'auth setup-git') : ;;
esac
STUB
    chmod +x "$case_dir/bin/gh"
    export PATH="$case_dir/bin:$PATH"
    git config --global --add url.https://github.com/.insteadOf git@github.com:
    printf old >"$GH_STUB_DIR/login"
    mkdir -p "$HOME/.config/orca-docker"
    if command -v sha256sum >/dev/null; then digest="$(printf old | sha256sum)"; else digest="$(printf old | shasum -a 256)"; fi
    printf '%s\n' "${digest%% *}" >"$HOME/.config/orca-docker/github-token.sha256"
    if [ "$scenario" = resume ] || [ "$scenario" = reattach ]; then
        printf old >"$GITHUB_AUTH_DIR/token"
        cat >"$case_dir/scripts/docker-lib.sh" <<'LIB'
log() { :; }
read_payload() { RESOURCE_ID=c1; PAYLOAD_PORT=''; PAYLOAD_IMAGE=fixture; }
assert_owned() { :; }
ensure_ssh_key() { :; }
docker() { :; }
bring_up() { BROUGHT_UP_PORT=2222; BROUGHT_UP_HOST_KEY=fixture; }
emit_result() { :; }
container_exists() { return 0; }
container_running() { return 0; }
share_credentials() { :; }
run_lifecycle() {
    case "$1 $2" in
        'host initializeCommand') bash "$AUTH_SCRIPT" host ;;
        'container postStartCommand') bash "$AUTH_SCRIPT" container ;;
        *) return 1 ;;
    esac
}
LIB
        if [ "$scenario" = resume ]; then
            cp "$ROOT/scripts/orca-vm/docker-resume.sh" "$case_dir/scripts/"
            bash "$case_dir/scripts/docker-resume.sh"
        else
            sed -n '/^ensure_container() {/,/^}/p' "$ROOT/scripts/orca-vm/docker-workspace.sh" >"$case_dir/ensure.sh"
            bash -c '. "$CASE_DIR/scripts/docker-lib.sh"; . "$CASE_DIR/ensure.sh"; ensure_container c1 fixture'
        fi
        [ ! -e "$GITHUB_AUTH_DIR/token" ]
    else
        case "$scenario" in
            replace) printf new >"$GITHUB_AUTH_DIR/token" ;;
            logout-failure) export FAIL_LOGOUT=1 ;;
        esac
        bash "$AUTH_SCRIPT" container
    fi
    if [ "$scenario" = logout-failure ]; then
        [ -s "$HOME/.config/orca-docker/github-token.sha256" ]
        [ "$(cat "$GH_STUB_DIR/login")" = old ]
        unset FAIL_LOGOUT
    else
        [ ! -e "$HOME/.config/orca-docker/github-token.sha256" ]
        [ ! -e "$GH_STUB_DIR/inactive" ]
        if [ "$scenario" = replace ]; then
            [ "$(cat "$GH_STUB_DIR/login")" = new ]
        else
            [ ! -e "$GH_STUB_DIR/login" ]
            if git config --global --get-all url.https://github.com/.insteadOf; then exit 1; fi
        fi
    fi
done
# Pin repository integration and migration guidance, as well as the shell logic.
[ "$(sed -n '/^ORCA_DOCKER_GITHUB_AUTH=/p' "$ROOT/scripts/orca-vm/defaults.env")" = ORCA_DOCKER_GITHUB_AUTH=none ]
grep -q 'revoke.*GitHub' "$ROOT/.devcontainer/README.md"
grep -q '^test-devcontainer-scripts:' "$ROOT/Makefile"
grep -q 'make test-devcontainer-scripts' "$ROOT/.github/workflows/shellcheck.yml"
printf 'github auth migration: 5 cases and integration checks passed\n'
