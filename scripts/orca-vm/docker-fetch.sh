#!/usr/bin/env bash
# Fetches the branches of Orca workspace containers into this (host) checkout
# as refs/remotes/orca/<container>/*, so work can be reviewed and pushed from
# the host. The containers hold no credentials for origin.
#
#   scripts/orca-vm/docker-fetch.sh             all running containers of this repo
#   scripts/orca-vm/docker-fetch.sh <container> one container (must be running)
set -euo pipefail

# shellcheck source=docker-lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/docker-lib.sh"

if [ $# -gt 0 ]; then
    names="$*"
else
    names="$(docker ps --filter "label=${LABEL_REPO}=${REPO_ROOT}" --format '{{.Names}}')"
fi
[ -n "$names" ] || die "no running Orca containers for ${REPO_ROOT}"

ensure_ssh_key
opts=()
while IFS= read -r o; do opts+=("$(printf '%q' "$o")"); done < <(ssh_opts)
export GIT_SSH_COMMAND="ssh ${opts[*]}"

for name in $names; do
    assert_owned "$name"
    trust_container "$name"
    log "fetching ${name}"
    git -C "$REPO_ROOT" fetch --prune \
        "ssh://${CONTAINER_USER}@${LOOPBACK}:${BROUGHT_UP_PORT}${PROJECT_ROOT_IN_CONTAINER}" \
        "+refs/heads/*:refs/remotes/orca/${name}/*"
done
