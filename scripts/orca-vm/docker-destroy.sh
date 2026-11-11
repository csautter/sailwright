#!/usr/bin/env bash
# Orca environment recipe "docker-devcontainer": destroy. Fetches the
# container's branches to the host (the container holds no credentials for
# origin), then removes the container and its known_hosts entries. If the fetch
# fails, the branches are saved under the state directory's rescue/ first; if
# that fails too, nothing is removed. Shared named volumes and host mounts
# (agent logins, skills, caches, shared directories) are kept.
# ORCA_DOCKER_SKIP_FETCH=1 skips the fetch and the rescue.
set -euo pipefail

# shellcheck source=docker-lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/docker-lib.sh"

read_payload
if container_exists "$RESOURCE_ID"; then
    assert_owned "$RESOURCE_ID"
    if [ "${ORCA_DOCKER_SKIP_FETCH:-}" != 1 ]; then
        log "fetching branches of ${RESOURCE_ID} before removing it"
        if ! fetch_container_branches "$RESOURCE_ID"; then
            rescue="$(rescue_container_branches "$RESOURCE_ID")" \
                || die "could not fetch or save the branches of ${RESOURCE_ID}; nothing removed (ORCA_DOCKER_SKIP_FETCH=1 discards them)"
            log "WARNING: could not fetch the branches of ${RESOURCE_ID}; saved them to ${rescue}"
            log "recover them with: git fetch '${rescue}' 'refs/heads/*:refs/remotes/orca/${RESOURCE_ID}/*'"
        fi
    fi
    if container_running "$RESOURCE_ID"; then
        # A restart for the fetch can publish a new port; forget that one too.
        current_port="$(wait_for_port "$RESOURCE_ID" 2>/dev/null || true)"
        current_key="$(read_host_key "$RESOURCE_ID" 2>/dev/null || true)"
    fi
    docker rm -f "$RESOURCE_ID" >/dev/null
    log "removed ${RESOURCE_ID}"
    forget_known_host "${current_port:-}" "${current_key:-}"
else
    log "${RESOURCE_ID} does not exist"
fi
forget_known_host "$PAYLOAD_PORT" "$PAYLOAD_HOST_KEY"
