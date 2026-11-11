#!/usr/bin/env bash
# Orca environment recipe "docker-devcontainer": suspend (stops the container).
set -euo pipefail

# shellcheck source=docker-lib.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/docker-lib.sh"

read_payload
assert_owned "$RESOURCE_ID"
docker stop "$RESOURCE_ID" >/dev/null
log "stopped ${RESOURCE_ID}"
