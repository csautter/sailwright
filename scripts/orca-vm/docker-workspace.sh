#!/usr/bin/env bash
# Scriptable Orca workspaces in local Docker containers, for agents that
# orchestrate many workspaces through the `orca` CLI.
#
# Orca's per-workspace recipes (orca.yaml) can only be started from the app UI.
# This script uses the same image and lifecycle scripts, but registers each
# container as an Orca SSH host. Orca then runs a managed Orca server in the
# container, and workspaces are created there with the regular CLI
# (`orca worktree create --environment <server>`).
#
#   docker-workspace.sh create <name> [--isolated|--shared] [-- <orca worktree create flags>]
#   docker-workspace.sh rm <name> [--force] [--no-fetch]
#   docker-workspace.sh list
#   docker-workspace.sh up                  start the shared container and its Orca server
#   docker-workspace.sh down [--force]      fetch branches, then remove the shared container
#   docker-workspace.sh doctor              check Docker, Orca and the RPC methods used here
#
# Modes:
#   shared (default)  all workspaces are worktrees in one container
#                     (orca-<repo>-shared), like a VS Code devcontainer.
#   --isolated        one container per workspace (orca-<repo>-ws-<name>);
#                     `rm` removes the container, its SSH host and its Orca server.
#
# `create` prints one JSON object on stdout (environmentId, worktree id and path,
# plus Orca's worktree-create result). Use its environmentId with --environment
# for follow-up commands, e.g. `orca terminal create --environment <id> ...`.
# Example:
#   docker-workspace.sh create fix-login -- --agent codex --prompt "Fix the login test"
#
# A container's branches are pushed only if its shared GitHub token allows it
# (.devcontainer/github-auth.sh), so `rm` and `down` first fetch the
# container's branches to refs/remotes/orca/<container>/* (docker-fetch.sh) and
# refuse to remove anything if that fails, unless --force (or --no-fetch for rm).
# Parallel calls are safe: work on one container is serialised by a lock.
#
# Requires Orca, Docker and jq on macOS or Windows (Git Bash). Orca's CLI has
# no commands for SSH hosts, so ssh.listEditableTargets, ssh.addTarget,
# ssh.updateTarget, ssh.connect and ssh.removeTarget are called through Orca's
# runtime RPC (orca-rpc.js, run by the Node.js built into Orca). This is not a
# documented interface: run `doctor` after Orca updates.
set -euo pipefail

# shellcheck source=docker-lib.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/docker-lib.sh"

SHARED_CONTAINER="$(sanitize_name "orca-${REPO_NAME}-shared")"

usage() {
    sed -n '/^#   docker-workspace.sh create/,/^#   docker-workspace.sh doctor/s/^# \{0,3\}//p' "${BASH_SOURCE[0]}" >&2
    exit "${1:-2}"
}

# --- Orca CLI and runtime RPC --------------------------------------------------

# Prints the Orca installation directory: Orca.app on macOS, the directory of
# Orca.exe on Windows (default %LOCALAPPDATA%\Programs\orca). ORCA_APP
# overrides it.
orca_app() {
    local cli app
    if [ -n "${ORCA_APP:-}" ]; then host_path "$ORCA_APP"; return 0; fi
    cli="$(command -v orca 2>/dev/null || true)"
    while [ -n "$cli" ] && [ -L "$cli" ]; do
        local link
        link="$(readlink "$cli")"
        case "$link" in /*) cli="$link" ;; *) cli="$(dirname "$cli")/${link}" ;; esac
    done
    if $IS_WINDOWS; then
        # <install>/resources/bin/orca(.exe|.cmd)
        case "$cli" in
            */resources/bin/*) host_path "${cli%/resources/bin/*}" ;;
            *) host_path "${LOCALAPPDATA:-${HOST_HOME}/AppData/Local}/Programs/orca" ;;
        esac
        return 0
    fi
    app="${cli%%.app/*}"
    if [ -n "$cli" ] && [ "$app" != "$cli" ]; then
        printf '%s.app' "$app"
    else
        printf '%s' /Applications/Orca.app
    fi
}

ORCA_APP_DIR="$(orca_app)"
if $IS_WINDOWS; then
    ORCA_BIN="${ORCA_APP_DIR}/resources/bin/orca.exe"
    ORCA_NODE="${ORCA_APP_DIR}/Orca.exe"
    ORCA_CLI_DIR="${ORCA_APP_DIR}/resources/app.asar.unpacked/out/cli"
    ORCA_PROFILES_DIR="$(host_path "${APPDATA:-${HOST_HOME}/AppData/Roaming}")/orca/profiles"
else
    ORCA_BIN="${ORCA_APP_DIR}/Contents/Resources/bin/orca"
    ORCA_NODE="${ORCA_APP_DIR}/Contents/MacOS/Orca"
    ORCA_CLI_DIR="${ORCA_APP_DIR}/Contents/Resources/app.asar.unpacked/out/cli"
    ORCA_PROFILES_DIR="${HOME}/Library/Application Support/orca/profiles"
fi
[ -x "$ORCA_BIN" ] || ORCA_BIN="$(command -v orca 2>/dev/null || true)"
[ -n "$ORCA_BIN" ] || die "orca CLI not found (set ORCA_APP to the Orca installation)"

orca_cli() { "$ORCA_BIN" "$@"; }

# Runs JavaScript with the Node.js built into Orca.
orca_node() { ELECTRON_RUN_AS_NODE=1 "$ORCA_NODE" "$@"; }

rpc() {
    orca_node "${ORCA_VM_DIR}/orca-rpc.js" "$ORCA_CLI_DIR" "$@"
}

# Runs an orca CLI command with --json and prints .result, or dies with the error.
orca_json() {
    local out
    if ! out="$(orca_cli "$@" --json)" || [ "$(printf '%s' "$out" | jq -r '.ok' 2>/dev/null)" != true ]; then
        echo "orca $*:" >&2
        printf '%s\n' "$out" >&2
        echo "orca-docker: orca $1 $2 failed" >&2
        return 1
    fi
    printf '%s' "$out" | jq -c '.result'
}

# --- containers ------------------------------------------------------------------

container_name_for() {
    local ws="$1" mode="$2"
    if [ "$mode" = isolated ]; then
        sanitize_name "orca-${REPO_NAME}-ws-${ws}"
    else
        printf '%s' "$SHARED_CONTAINER"
    fi
}

container_lock() { lock_acquire "container-$1" 7200; }

free_port() {
    orca_node -e 'const s = require("net").createServer().listen(0, "127.0.0.1", () => { console.log(s.address().port); s.close() })' \
        | tr -d '\r'
}

# Creates, restarts or reattaches the container; sets CONTAINER_PORT and CONTAINER_HOST_KEY.
# CONTAINER_CREATED is true when the container was created now (freshly seeded).
ensure_container() {
    local name="$1" mode="$2" result
    CONTAINER_CREATED=false
    if ! container_exists "$name"; then
        log "creating container ${name}"
        result="$(ORCA_DOCKER_CONTAINER_NAME="$name" ORCA_DOCKER_SSH_PORT="$(free_port)" \
            ORCA_RECIPE_ID="docker-workspace-${mode}" "${ORCA_VM_DIR}/docker-create.sh")"
        CONTAINER_CREATED=true
    elif ! container_running "$name"; then
        assert_owned "$name"
        log "starting container ${name}"
        result="$(jq -n --arg n "$name" '{recipeResult: {userData: {resourceId: $n}}}' \
            | "${ORCA_VM_DIR}/docker-resume.sh")"
    else
        assert_owned "$name"
        bring_up "$name"
        share_opencode_auth "$name"
        CONTAINER_PORT="$BROUGHT_UP_PORT"
        CONTAINER_HOST_KEY="$BROUGHT_UP_HOST_KEY"
        return 0
    fi
    CONTAINER_PORT="$(printf '%s' "$result" | jq -r '.connection.target.port')"
    # shellcheck disable=SC2034 # part of the result contract set by this function
    CONTAINER_HOST_KEY="$(printf '%s' "$result" | jq -r '.userData.hostKey')"
}

# Brings the container's repository up to date with this checkout: origin/*
# and tags are mirrored, local branches only fast-forwarded, so branches that
# agents created or advanced in the container are never overwritten.
sync_repo() {
    local name="$1" bundle
    bundle="$(host_mktemp orca-repo)"
    create_repo_bundle "$bundle"
    docker cp "$bundle" "${name}:/tmp/repo-sync.bundle" >/dev/null
    rm -f "$bundle"
    docker exec -u root "$name" chown "$CONTAINER_USER" /tmp/repo-sync.bundle
    log "syncing ${PROJECT_ROOT_IN_CONTAINER} with ${REPO_ROOT}"
    cexec -i -e "ROOT=${PROJECT_ROOT_IN_CONTAINER}" "$name" bash -s >&2 <<'EOF'
set -uo pipefail
cd "$ROOT"
b=/tmp/repo-sync.bundle
# The primary checkout is only Orca's repository root; detach it (when clean)
# so its branch can be fast-forwarded like the others.
if git symbolic-ref -q HEAD >/dev/null && [ -z "$(git status --porcelain)" ]; then
    git checkout -q --detach
fi
git fetch -q "$b" '+refs/remotes/origin/*:refs/remotes/origin/*' 'refs/tags/*:refs/tags/*' || true
checked_out="$(git worktree list --porcelain | sed -n 's/^branch //p')"
git bundle list-heads "$b" | awk '{print $2}' | grep '^refs/heads/' | while IFS= read -r ref; do
    if printf '%s\n' "$checked_out" | grep -qxF "$ref"; then continue; fi
    git fetch -q "$b" "${ref}:${ref}" 2>/dev/null || echo "sync: kept ${ref#refs/heads/} (diverged from host)"
done
rm -f "$b"
EOF
}

# --- Orca SSH host and server ----------------------------------------------------

# Prints the SSH target id for a label, or nothing.
target_id_for() {
    rpc ssh.listEditableTargets | jq -r --arg l "$1" '[.result | .. | objects | select(.label? == $l and .id?)][0].id // empty'
}

ensure_target() {
    local name="$1" port="$2" target id current
    target="$(jq -nc --arg l "$name" --arg h "$LOOPBACK" --argjson p "$port" --arg u "$CONTAINER_USER" \
        --arg k "$SSH_KEY" '{label: $l, host: $h, port: $p, username: $u, identityFile: $k, identitiesOnly: true}')"
    id="$(target_id_for "$name")"
    if [ -z "$id" ]; then
        log "adding Orca SSH host ${name}"
        id="$(rpc ssh.addTarget "$(jq -nc --argjson t "$target" '{target: $t}')" | jq -r '.result.target.id')"
    else
        current="$(rpc ssh.listEditableTargets | jq -c --arg i "$id" '[.result | .. | objects | select(.id? == $i)][0] | {port, identityFile}')"
        if [ "$current" != "$(printf '%s' "$target" | jq -c '{port, identityFile}')" ]; then
            log "updating Orca SSH host ${name} (port ${port}, key ${SSH_KEY})"
            rpc ssh.updateTarget "$(jq -nc --arg i "$id" --argjson t "$target" '{targetId: $i, target: $t}')" >/dev/null
        fi
    fi
    if [ -z "$id" ] || [ "$id" = null ]; then die "could not register SSH host ${name} in Orca"; fi
    log "connecting Orca to ${name} (first connect installs the Orca server)"
    local state
    state="$(rpc ssh.connect "$(jq -nc --arg i "$id" '{targetId: $i}')" | jq -c '.result.state')"
    [ "$(printf '%s' "$state" | jq -r '.status')" = connected ] \
        || die "Orca could not connect to ${name}: $(printf '%s' "$state" | jq -r '.error // .status')"
    TARGET_ID="$id"
}

# Prints the id of the managed Orca server deployed over the SSH target, or nothing.
environment_for_target() {
    orca_json environment list | jq -r --arg t "$1" \
        '[.environments[] | select(.orcadDeployment.sshTargetId? == $t)][0].id // empty'
}

ensure_environment() {
    local target="$1" env="" _i
    for _i in $(seq 1 30); do
        env="$(environment_for_target "$target")"
        [ -n "$env" ] && break
        sleep 2
    done
    [ -n "$env" ] || die "no Orca server appeared for SSH host ${target}"
    if ! orca_json repo list --environment "$env" \
        | jq -e --arg p "$PROJECT_ROOT_IN_CONTAINER" '.repos[] | select(.path == $p)' >/dev/null; then
        log "adding ${PROJECT_ROOT_IN_CONTAINER} to the Orca server"
        orca_json repo add --environment "$env" --path "$PROJECT_ROOT_IN_CONTAINER" >/dev/null
    fi
    ENVIRONMENT_ID="$env"
}

# Container -> running container, SSH host and Orca server with the repository.
# Callers hold the container lock.
ensure_ready() {
    local name="$1" mode="$2"
    ensure_ssh_key
    ensure_container "$name" "$mode"
    $CONTAINER_CREATED || sync_repo "$name"
    ensure_target "$name" "$CONTAINER_PORT"
    ensure_environment "$TARGET_ID"
}

# Removes the Orca server, the SSH host and the container. Callers fetch the
# container's branches first.
teardown() {
    local name="$1" target env host_key="" port=""
    target="$(target_id_for "$name")"
    if [ -n "$target" ]; then
        env="$(environment_for_target "$target")"
        if [ -n "$env" ]; then
            local wt
            for wt in $(orca_json worktree list --environment "$env" 2>/dev/null | jq -r '.worktrees[].id' || true); do
                orca_json terminal close --environment "$env" --worktree "id:${wt}" --all >/dev/null 2>&1 || true
            done
            log "stopping Orca server on ${name}"
            orca_json environment stop --environment "$env" --yes >/dev/null \
                || orca_json environment forget --environment "$env" --yes >/dev/null \
                || log "WARNING: could not stop or forget Orca server ${env}; remove it in Orca"
        fi
        log "removing Orca SSH host ${name}"
        rpc ssh.removeTarget "$(jq -nc --arg i "$target" '{targetId: $i}')" >/dev/null \
            || log "WARNING: could not remove Orca SSH host ${name}; remove it in Orca"
    fi
    if container_exists "$name"; then
        if container_running "$name"; then
            port="$(wait_for_port "$name")"
            host_key="$(read_host_key "$name")"
        fi
        jq -n --arg n "$name" --arg k "$host_key" --arg p "$port" \
            '{recipeResult: {connection: {target: {port: $p}}, userData: {resourceId: $n, hostKey: $k}}}' \
            | ORCA_DOCKER_SKIP_FETCH=1 "${ORCA_VM_DIR}/docker-destroy.sh"
    fi
}

worktree_id_for() {
    local env="$1" ws="$2"
    orca_json worktree list --environment "$env" | jq -r --arg n "$ws" \
        '[.worktrees[] | select(.isMainWorktree | not) | select(.displayName == $n or (.path | split("/") | last) == $n)][0].id // empty'
}

# --- commands --------------------------------------------------------------------

# Exit hook of an isolated `create`: removes the half-created container.
ROLLBACK_CONTAINER=""
rollback_create() {
    [ "$EXIT_RC" -ne 0 ] && [ -n "$ROLLBACK_CONTAINER" ] || return 0
    log "create failed, removing ${ROLLBACK_CONTAINER}"
    teardown "$ROLLBACK_CONTAINER"
}

cmd_create() {
    local ws="" mode=shared name extra=() has_parent=false a
    while [ $# -gt 0 ]; do
        case "$1" in
            --isolated) mode=isolated ;;
            --shared) mode=shared ;;
            -h|--help) usage 0 ;;
            --) shift; extra=("$@"); break ;;
            -*) die "unknown option $1 (pass orca worktree create flags after --)" ;;
            *) [ -z "$ws" ] || die "only one workspace name"; ws="$1" ;;
        esac
        shift
    done
    [ -n "$ws" ] || usage
    for a in ${extra[@]+"${extra[@]}"}; do
        case "$a" in --parent-worktree|--no-parent) has_parent=true ;; --name|--repo|--environment|--project|--host) die "$a is set by this script" ;; esac
    done
    $has_parent || extra+=(--no-parent)

    name="$(container_name_for "$ws" "$mode")"
    container_lock "$name"
    if [ "$mode" = isolated ]; then
        container_exists "$name" && die "workspace ${ws} already exists (container ${name})"
        ROLLBACK_CONTAINER="$name"
        on_exit rollback_create
    fi
    ensure_ready "$name" "$mode"
    lock_release "container-${name}"

    log "creating workspace ${ws}"
    local created
    created="$(orca_json worktree create --environment "$ENVIRONMENT_ID" \
        --repo "path:${PROJECT_ROOT_IN_CONTAINER}" --name "$ws" ${extra[@]+"${extra[@]}"})"
    ROLLBACK_CONTAINER=""
    printf '%s' "$created" | jq --arg mode "$mode" --arg c "$name" --arg t "$TARGET_ID" --arg e "$ENVIRONMENT_ID" '{
        mode: $mode, container: $c, sshTargetId: $t, environmentId: $e,
        worktreeId: .worktree.id, worktreePath: .worktree.path,
        branch: (.worktree.branch | sub("^refs/heads/"; "")),
        worktreeCreate: .
    }'
}

cmd_rm() {
    local ws="" force=false fetch=true mode name target env wt
    while [ $# -gt 0 ]; do
        case "$1" in
            --force) force=true ;;
            --no-fetch) fetch=false ;;
            -h|--help) usage 0 ;;
            -*) die "unknown option $1" ;;
            *) ws="$1" ;;
        esac
        shift
    done
    [ -n "$ws" ] || usage
    mode=shared
    container_exists "$(container_name_for "$ws" isolated)" && mode=isolated
    name="$(container_name_for "$ws" "$mode")"
    container_exists "$name" || die "no container for workspace ${ws}"
    assert_owned "$name"
    container_lock "$name"
    container_running "$name" || ensure_ready "$name" "$mode"

    if $fetch; then
        "${ORCA_VM_DIR}/docker-fetch.sh" "$name" \
            || die "could not fetch the branches of ${name}; nothing removed (pass --no-fetch to discard them)"
    fi

    target="$(target_id_for "$name")"
    env="${target:+$(environment_for_target "$target")}"
    wt=""
    [ -z "$env" ] || wt="$(worktree_id_for "$env" "$ws")"
    if [ -n "$wt" ]; then
        log "removing workspace ${ws}"
        local rm_args=(worktree rm --environment "$env" --worktree "id:${wt}")
        if $force; then rm_args+=(--force); fi
        orca_json "${rm_args[@]}" >/dev/null
    elif [ "$mode" = shared ]; then
        die "workspace ${ws} not found in ${name}"
    fi
    if [ "$mode" = isolated ]; then teardown "$name"; fi
    log "removed workspace ${ws}"
}

cmd_list() {
    local name state recipe target env
    docker ps -a --filter "label=${LABEL_REPO}=${REPO_ROOT}" --format '{{.Names}}\t{{.State}}\t{{.Label "org.orca.recipe"}}' \
        | while IFS=$'\t' read -r name state recipe; do
            target="$(target_id_for "$name")"
            env="${target:+$(environment_for_target "$target")}"
            printf '%s\t%s\t%s\tenvironment=%s\n' "$name" "$state" "${recipe:-}" "${env:--}"
            if [ -n "$env" ] && [ "$state" = running ]; then
                orca_json worktree list --environment "$env" 2>/dev/null \
                    | jq -r '.worktrees[] | select(.isMainWorktree | not) | "  \(.displayName)\t\(.path)\t\(.branch | sub("^refs/heads/"; ""))"' || true
            fi
        done
}

cmd_up() {
    container_lock "$SHARED_CONTAINER"
    ensure_ready "$SHARED_CONTAINER" shared
    jq -n --arg c "$SHARED_CONTAINER" --arg t "$TARGET_ID" --arg e "$ENVIRONMENT_ID" \
        '{mode: "shared", container: $c, sshTargetId: $t, environmentId: $e}'
}

# Removes the shared container. A stopped container is started first, so its
# branches are always fetched; without --force, open workspaces or a failed
# start or fetch stop the removal. Works without Orca running.
cmd_down() {
    local force=false count
    case "${1:-}" in
        --force) force=true ;;
        -h|--help) usage 0 ;;
        '') ;;
        *) die "unknown option $1" ;;
    esac
    container_exists "$SHARED_CONTAINER" || { log "${SHARED_CONTAINER} does not exist"; return 0; }
    assert_owned "$SHARED_CONTAINER"
    container_lock "$SHARED_CONTAINER"
    ensure_ssh_key
    if ! (start_container "$SHARED_CONTAINER"); then
        $force || die "could not start ${SHARED_CONTAINER} to fetch its branches; nothing removed (pass --force to discard them)"
    else
        count="$(container_worktree_count "$SHARED_CONTAINER")"
        [ "$count" = 0 ] || $force \
            || die "${count} workspace(s) still in ${SHARED_CONTAINER}; remove them or pass --force (their uncommitted changes are lost)"
        "${ORCA_VM_DIR}/docker-fetch.sh" "$SHARED_CONTAINER" || $force \
            || die "could not fetch the branches of ${SHARED_CONTAINER}; nothing removed (pass --force to discard them)"
    fi
    teardown "$SHARED_CONTAINER"
}

# Prints the Cloud VM setting of the Orca app (true/false), or nothing. Reads
# the settings database read-only, with sqlite3 or Orca's own Node.js.
orca_cloud_vm_setting() {
    local db query="select payload from profile_state_documents where domain = 'settings'"
    for db in "${ORCA_PROFILES_DIR}"/*/profile-state.db; do
        [ -f "$db" ] || continue
        if command -v sqlite3 >/dev/null 2>&1; then
            sqlite3 -readonly "$db" "$query" 2>/dev/null
        else
            orca_node -e 'const { DatabaseSync } = require("node:sqlite")
                const db = new DatabaseSync(process.argv[1], { readOnly: true })
                const row = db.prepare(process.argv[2]).get()
                if (row) process.stdout.write(row.payload)' "$db" "$query" 2>/dev/null
        fi | jq -r '.experimentalEphemeralVms // false' 2>/dev/null || true
        return 0
    done
}

cmd_doctor() {
    local ok=true
    if docker info >/dev/null 2>&1; then
        log "docker: ok"
    else
        log "docker: daemon not reachable"; ok=false
    fi
    if orca_cli status --json 2>/dev/null | jq -e '.result.runtime.reachable == true' >/dev/null; then
        log "orca runtime: ok (${ORCA_APP_DIR}, $(orca_cli --version 2>/dev/null || echo 'version unknown'))"
    else
        log "orca runtime: not reachable (start Orca)"; ok=false
    fi
    if rpc ssh.listEditableTargets >/dev/null 2>&1; then
        log "orca rpc ssh.*: ok"
    else
        log "orca rpc ssh.*: failed (Orca update may have changed the internal RPC)"; ok=false
    fi
    if [ "$(free_port 2>/dev/null)" -gt 0 ] 2>/dev/null; then
        log "orca node (${ORCA_NODE}): ok"
    else
        log "orca node (${ORCA_NODE}): failed"; ok=false
    fi
    if $IS_WINDOWS; then
        if [ -f "${ORCA_VM_DIR}/recipe.cmd" ]; then
            log "recipe.cmd: ok"
        else
            log "recipe.cmd: missing (Orca runs the recipe through cmd.exe on Windows)"; ok=false
        fi
    fi
    if [ -f "$SSH_KEY" ]; then
        log "ssh key: ${SSH_KEY}"
    elif [ -f "$LEGACY_SSH_KEY" ]; then
        log "ssh key: ${LEGACY_SSH_KEY} (moved to ${SSH_KEY} on the next create)"
    else
        log "ssh key: none yet (generated on the next create)"
    fi
    case "$(orca_cloud_vm_setting)" in
        true) log "orca Cloud VM: on (recipe \"Docker (devcontainer image)\" is offered under Run on)" ;;
        false) log "orca Cloud VM: off. To use the recipe from the Orca app, turn on Settings > Experimental > Cloud VM (not needed for this script)" ;;
        *) log "orca Cloud VM: unknown. The recipe needs Settings > Experimental > Cloud VM (not needed for this script)" ;;
    esac
    $ok
}

cmd="${1:-}"
[ $# -eq 0 ] || shift
case "$cmd" in
    create) cmd_create "$@" ;;
    rm) cmd_rm "$@" ;;
    list) cmd_list ;;
    up) cmd_up ;;
    down) cmd_down "$@" ;;
    doctor) cmd_doctor ;;
    -h|--help|help) usage 0 ;;
    *) usage ;;
esac
