#!/usr/bin/env bash
# Orca environment recipe "docker-devcontainer": create.
# Starts one container per workspace from the devcontainer image plus an sshd
# layer (.devcontainer/orca/Dockerfile), seeds the repository, runs the devcontainer lifecycle commands and
# prints the SSH recipe result on stdout. Progress goes to stderr.
# Single-quoted snippets run in the container shell, where $HOME expands.
# shellcheck disable=SC2016

set -euo pipefail

# shellcheck source=docker-lib.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/docker-lib.sh"

recipe_id="${ORCA_RECIPE_ID:-docker-devcontainer}"
instance_id="${ORCA_VM_INSTANCE_ID:-$(date +%s)}"
# docker-workspace.sh sets a fixed name and SSH port so its Orca SSH target
# stays valid across container restarts; the recipe uses an ephemeral port.
name="$(sanitize_name "${ORCA_DOCKER_CONTAINER_NAME:-orca-${REPO_NAME}-${recipe_id}-${instance_id}}")"
publish="${LOOPBACK}:${ORCA_DOCKER_SSH_PORT:-}:22"
container_exists "$name" && die "container ${name} already exists"

ensure_ssh_key
ensure_image
image="$IMAGE_TAG"

log "initializeCommand (host)"
run_lifecycle host initializeCommand

collect_run_args

created=false
port=""
host_key=""
bundle=""
cleanup() {
    [ -z "$bundle" ] || rm -f "$bundle"
    if [ "$EXIT_RC" -ne 0 ] && $created; then
        log "create failed, removing ${name}"
        docker rm -f "$name" >/dev/null 2>&1 || true
        forget_known_host "$port" "$host_key"
    fi
}
on_exit cleanup

log "starting container ${name}"
# Runs as root (the image entrypoint starts sshd); sessions and lifecycle
# commands use remoteUser. IS_SANDBOX=1 lets Claude Code accept
# --dangerously-skip-permissions (Orca's agent view passes it) when remoteUser
# is root: the container is an isolated, per-workspace sandbox.
docker run -d --init --user root \
    --name "$name" --hostname "$name" \
    --label "${LABEL_RECIPE}=${recipe_id}" \
    --label "${LABEL_REPO}=${REPO_ROOT}" \
    -p "$publish" \
    -e "ORCA_SSH_PUBLIC_KEY=$(cat "${SSH_KEY}.pub")" \
    -e "ORCA_SSH_USER=${CONTAINER_USER}" \
    -e ORCA_DOCKER_WORKSPACE=1 \
    -e IS_SANDBOX=1 \
    ${DOCKER_RUN_ARGS[@]+"${DOCKER_RUN_ARGS[@]}"} \
    "$image" >/dev/null
created=true

bring_up "$name"
port="$BROUGHT_UP_PORT"
host_key="$BROUGHT_UP_HOST_KEY"

# --- Seed the repository: all local branches, tags and origin/* refs of the
# primary checkout, so Orca can create its linked worktree from any base ref.
# Uncommitted changes and gitignored files are not copied; the
# worktree.sharedDirectories of orca.yaml are bind-mounted instead.
log "seeding ${PROJECT_ROOT_IN_CONTAINER} from ${REPO_ROOT}"
bundle="$(host_mktemp orca-repo)"
create_repo_bundle "$bundle"
docker cp "$bundle" "${name}:/tmp/repo.bundle" >/dev/null
docker exec -u root "$name" sh -c \
    'mkdir -p "$1" && chown "$2" "$1" /tmp/repo.bundle' _ "$PROJECT_ROOT_IN_CONTAINER" "$CONTAINER_USER"

origin_url=""
raw_origin_url="$(git -C "$REPO_ROOT" remote get-url origin 2>/dev/null || true)"
if [ -n "$raw_origin_url" ]; then
    if origin_url="$(sanitize_remote_url "$raw_origin_url")"; then
        [ "$origin_url" = "$raw_origin_url" ] \
            || log "origin URL: credentials removed before copying it into the container"
    else
        origin_url=""
        log "origin URL not copied into the container (unsupported form); add the remote there by hand"
    fi
fi
raw_origin_url=""
origin_head="$(git -C "$REPO_ROOT" symbolic-ref -q refs/remotes/origin/HEAD 2>/dev/null || true)"
head_branch="$(git -C "$REPO_ROOT" symbolic-ref -q --short HEAD 2>/dev/null || true)"
head_commit="$(git -C "$REPO_ROOT" rev-parse HEAD)"
git_name="$(git -C "$REPO_ROOT" config user.name 2>/dev/null || true)"
git_email="$(git -C "$REPO_ROOT" config user.email 2>/dev/null || true)"

cexec -i \
    -e "ROOT=${PROJECT_ROOT_IN_CONTAINER}" -e "ORIGIN_URL=${origin_url}" \
    -e "ORIGIN_HEAD=${origin_head}" -e "HEAD_BRANCH=${head_branch}" \
    -e "HEAD_COMMIT=${head_commit}" -e "GIT_NAME=${git_name}" -e "GIT_EMAIL=${git_email}" \
    "$name" bash -s >&2 <<'EOF'
set -euo pipefail
[ ! -e "$ROOT/.git" ] || { echo "$ROOT already contains a repository" >&2; exit 1; }
git init -q -b "${HEAD_BRANCH:-main}" "$ROOT"
cd "$ROOT"
[ -z "$ORIGIN_URL" ] || git remote add origin "$ORIGIN_URL"
git fetch -q --update-head-ok /tmp/repo.bundle \
    '+refs/heads/*:refs/heads/*' '+refs/tags/*:refs/tags/*' \
    '+refs/remotes/origin/*:refs/remotes/origin/*'
[ -z "$ORIGIN_HEAD" ] || git symbolic-ref refs/remotes/origin/HEAD "$ORIGIN_HEAD"
if [ -n "$HEAD_BRANCH" ]; then
    git checkout -q -f "$HEAD_BRANCH"
else
    git fetch -q /tmp/repo.bundle HEAD
    git checkout -q -f --detach "$HEAD_COMMIT"
fi
git reset -q --hard
[ -z "$GIT_NAME" ] || git config --local user.name "$GIT_NAME"
[ -z "$GIT_EMAIL" ] || git config --local user.email "$GIT_EMAIL"
rm -f /tmp/repo.bundle
EOF

# Host key of the origin server, so git fetch/push over SSH from the container
# does not prompt. No SSH credentials are copied; GitHub remotes use HTTPS with
# the shared gh token instead (.devcontainer/github-auth.sh, postStartCommand).
origin_host="" origin_port=""
case "$origin_url" in
    ssh://*)
        hp="${origin_url#ssh://}"; hp="${hp#*@}"; hp="${hp%%/*}"
        origin_host="${hp%%:*}"
        [ "$hp" = "$origin_host" ] || origin_port="${hp#*:}"
        ;;
    *@*:*)
        hp="${origin_url#*@}"; origin_host="${hp%%:*}"
        ;;
esac
if [ -n "$origin_host" ]; then
    lookup="$origin_host"
    [ -z "$origin_port" ] || [ "$origin_port" = 22 ] || lookup="[${origin_host}]:${origin_port}"
    if entries="$(ssh-keygen -F "$lookup" -f "$USER_KNOWN_HOSTS" 2>/dev/null | grep -v '^#')" && [ -n "$entries" ]; then
        printf '%s\n' "$entries" | cexec -i "$name" sh -c \
            'umask 077; mkdir -p "$HOME/.ssh"; cat >>"$HOME/.ssh/known_hosts"'
    fi
fi

log "postCreateCommand"
run_lifecycle container postCreateCommand "$name"
log "postStartCommand"
run_lifecycle container postStartCommand "$name" || log "postStartCommand failed (continuing)"
share_opencode_auth "$name"

emit_result "$name" "$port" "$image" "$host_key"
