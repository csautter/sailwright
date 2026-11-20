#!/usr/bin/env bash
# Orca environment recipe "docker-devcontainer": resume. Restarts the
# container, refreshes authentication and prints a fresh recipe result
# (the published SSH port can change on restart; the host key does not).
set -euo pipefail

# shellcheck source=docker-lib.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/docker-lib.sh"

read_payload
assert_owned "$RESOURCE_ID"
ensure_ssh_key
docker start "$RESOURCE_ID" >/dev/null

bring_up "$RESOURCE_ID"
if [ -n "$PAYLOAD_PORT" ] && [ "$PAYLOAD_PORT" != "$BROUGHT_UP_PORT" ]; then
    forget_known_host "$PAYLOAD_PORT" "$BROUGHT_UP_HOST_KEY"
fi

# A repository auth helper is streamed from the host by share_credentials.
# Replaying an old checkout hook could discard migration retry markers.
if [ ! -f "$DEVCONTAINER_DIR/github-auth.sh" ]; then
    log "postStartCommand"
    run_lifecycle container postStartCommand "$RESOURCE_ID" || log "postStartCommand failed (continuing)"
fi
share_credentials "$RESOURCE_ID"

image="${PAYLOAD_IMAGE:-$(docker inspect -f '{{.Config.Image}}' "$RESOURCE_ID")}"
emit_result "$RESOURCE_ID" "$BROUGHT_UP_PORT" "$image" "$BROUGHT_UP_HOST_KEY"
