#!/usr/bin/env bash
# Offline reconnect dispatch, using real functions and host-owned helper bytes.
# shellcheck disable=SC2016
set -euo pipefail
for test_env_name in ${!ORCA_DOCKER_@} ${!SAILWRIGHT_@}; do unset "$test_env_name"; done
unset XDG_CONFIG_HOME CONFIG_HOME GITHUB_AUTH_DIR GITHUB_AUTH_STATE GH_TOKEN GITHUB_TOKEN GH_CONFIG_DIR BASH_ENV
SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
for scenario in reattach resume stopped-resume generic-resume; do
    case_dir="$(mktemp -d)"
    export CASE_DIR="$case_dir" HOME="$case_dir/home" XDG_CONFIG_HOME="$case_dir/home/.config"
    export USERPROFILE="" GIT_CONFIG_GLOBAL="$HOME/.gitconfig"
    mkdir -p "$HOME" "$case_dir/repo/.devcontainer" "$case_dir/repo/scripts/orca-vm" "$case_dir/bin" "$case_dir/old-checkout/.devcontainer"
    printf '{"remoteUser":"root"}\n' >"$case_dir/repo/.devcontainer/devcontainer.json"
    printf '#!/bin/sh\nexit 0\n' >"$case_dir/bin/docker"
    chmod +x "$case_dir/bin/docker"
    export PATH="$case_dir/bin:$PATH"
    cp "$SRC/docker-lib.sh" "$SRC/docker-resume.sh" "$case_dir/repo/scripts/orca-vm/"
    # A stale checkout hook must never be invoked on auth-only reconnect.
    printf '#!/bin/sh\necho stale >>"$CASE_DIR/events"\nexit 91\n' >"$case_dir/old-checkout/.devcontainer/github-auth.sh"
    if [ "$scenario" != generic-resume ]; then
        cat >"$case_dir/repo/.devcontainer/github-auth.sh" <<'HELPER'
#!/bin/bash
case "$1" in
 host) echo host-auth >>"$CASE_DIR/events" ;;
 container) echo current-auth >>"$CASE_DIR/events" ;;
esac
HELPER
    fi
    cat >"$case_dir/overrides.sh" <<'STUB'
log() { :; }
read_payload() { RESOURCE_ID=c1; PAYLOAD_PORT=''; PAYLOAD_IMAGE=fixture; }
assert_owned() { :; }
ensure_ssh_key() { :; }
docker() { echo start >>"$CASE_DIR/events"; }
bring_up() { echo up >>"$CASE_DIR/events"; BROUGHT_UP_PORT=2222; BROUGHT_UP_HOST_KEY=fixture; }
emit_result() { printf '{"connection":{"target":{"port":2222}},"userData":{"hostKey":"fixture"}}\n'; }
container_exists() { return 0; }
container_running() { [ "${STOPPED:-}" != 1 ]; }
share_github_auth() { echo generic-auth >>"$CASE_DIR/events"; }
share_opencode_auth() { :; }
cexec() { [ "$1" = -i ]; shift 2; (cd "$CASE_DIR/old-checkout" && "$@"); }
run_lifecycle() { echo "$1 $2" >>"$CASE_DIR/events"; }
STUB
    printf '\n. "$CASE_DIR/overrides.sh"\n' >>"$case_dir/repo/scripts/orca-vm/docker-lib.sh"
    if [ "$scenario" = reattach ]; then
        sed -n '/^ensure_container() {/,/^}/p' "$SRC/docker-workspace.sh" >"$case_dir/ensure.sh"
        bash -c '. "$CASE_DIR/repo/scripts/orca-vm/docker-lib.sh"; . "$CASE_DIR/ensure.sh"; ensure_container c1 shared; ensure_container c1 shared'
        expected=$'up\nhost-auth\ncurrent-auth\ngeneric-auth\nup\nhost-auth\ncurrent-auth\ngeneric-auth'
    else
        if [ "$scenario" = stopped-resume ]; then
            sed -n '/^ensure_container() {/,/^}/p' "$SRC/docker-workspace.sh" >"$case_dir/ensure.sh"
            STOPPED=1 bash -c '. "$CASE_DIR/repo/scripts/orca-vm/docker-lib.sh"; . "$CASE_DIR/ensure.sh"; ensure_container c1 shared'
        else
            bash "$case_dir/repo/scripts/orca-vm/docker-resume.sh"
        fi
        if [ "$scenario" = generic-resume ]; then
            expected=$'start\nup\ncontainer postStartCommand\ngeneric-auth'
        else
            expected=$'start\nup\nhost-auth\ncurrent-auth\ngeneric-auth'
        fi
    fi
    [ "$(cat "$case_dir/events")" = "$expected" ]
done
printf 'github auth lifecycle: 4 cases passed\n'
