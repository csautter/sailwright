#!/usr/bin/env bash
# Offline reconnect dispatch, using real functions and host-owned helper bytes.
# shellcheck disable=SC2016
set -euo pipefail
for test_env_name in ${!ORCA_DOCKER_@}; do unset "$test_env_name"; done
unset XDG_CONFIG_HOME CONFIG_HOME GITHUB_AUTH_DIR GITHUB_AUTH_STATE GH_TOKEN GITHUB_TOKEN GH_CONFIG_DIR BASH_ENV
SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
for scenario in reattach resume stopped-resume generic-resume resume-markers resume-error; do
    case_dir="$(mktemp -d)"
    export CASE_DIR="$case_dir" HOME="$case_dir/home with 'quote $" SCENARIO="$scenario"
    export XDG_CONFIG_HOME="$HOME/.config"
    export USERPROFILE="" GIT_CONFIG_GLOBAL="$HOME/.gitconfig"
    mkdir -p "$HOME" "$case_dir/repo/.devcontainer" "$case_dir/repo/scripts/orca-vm" "$case_dir/bin" "$case_dir/old-checkout/.devcontainer"
    cat >"$case_dir/repo/.devcontainer/devcontainer.json" <<'JSON'
{"remoteUser":"root", "initializeCommand":"exit 99", "postStartCommand":"echo service-started >>\"$CASE_DIR/events\"; case $SCENARIO in resume-markers|resume-error) bash .devcontainer/github-auth.sh container ;; esac"}
JSON
    printf '#!/bin/sh\nexit 0\n' >"$case_dir/bin/docker"
    chmod +x "$case_dir/bin/docker"
    export PATH="$case_dir/bin:$PATH"
    cp "$SRC/docker-lib.sh" "$SRC/docker-resume.sh" "$case_dir/repo/scripts/orca-vm/"
    # A stale checkout hook must never be invoked on auth-only reconnect.
    printf '#!/bin/sh\necho stale >>"$CASE_DIR/events"\nexit 91\n' >"$case_dir/old-checkout/.devcontainer/github-auth.sh"
    if [[ "$scenario" = resume-* ]]; then
        mkdir -p "$HOME/.config/provider" "$HOME/.config/orca-docker"
        printf '%064d' 1 >"$HOME/.config/provider/github-token.sha256"
        printf '%064d' 2 >"$HOME/.config/orca-docker/github-token.sha256"
        cat >"$case_dir/old-checkout/.devcontainer/github-auth.sh" <<'LEGACY'
#!/bin/bash
mv "$HOME/.config/provider/github-token.sha256" "$CASE_DIR/deleted-provider"
mv "$HOME/.config/orca-docker/github-token.sha256" "$CASE_DIR/deleted-generic"
echo legacy-auth >>"$CASE_DIR/events"
[ "$SCENARIO" != resume-error ]
LEGACY
    fi
    if [ "$scenario" != generic-resume ]; then
        cat >"$case_dir/repo/.devcontainer/github-auth.sh" <<'HELPER'
#!/bin/bash
case "$1" in
 host) echo host-auth >>"$CASE_DIR/events" ;;
 container)
    if [[ "$SCENARIO" = resume-* ]]; then
        cmp "$CASE_DIR/deleted-provider" "$HOME/.config/provider/github-token.sha256" || exit 1
        cmp "$CASE_DIR/deleted-generic" "$HOME/.config/orca-docker/github-token.sha256" || exit 1
    fi
    echo current-auth >>"$CASE_DIR/events" ;;
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
cexec() {
    if [ "$1" = -i ]; then shift; fi
    if [ "$1" = -w ]; then shift 2; fi
    shift
    # Model the container login environment without sourcing the host profile.
    if [ "$1" = bash ] && [ "${2:-}" = -lc ]; then shift 2; set -- bash -c "$@"; fi
    (cd "$CASE_DIR/old-checkout" && "$@")
}
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
            expected=$'start\nup\nservice-started\ngeneric-auth'
        elif [[ "$scenario" = resume-* ]]; then
            expected=$'start\nup\nservice-started\nlegacy-auth\nhost-auth\ncurrent-auth\ngeneric-auth'
        else
            expected=$'start\nup\nservice-started\nhost-auth\ncurrent-auth\ngeneric-auth'
        fi
    fi
    [ "$(cat "$case_dir/events")" = "$expected" ]
done
printf 'github auth lifecycle: 6 cases passed\n'
