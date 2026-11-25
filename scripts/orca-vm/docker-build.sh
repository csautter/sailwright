#!/usr/bin/env bash
# Prebuilds the Orca image (Dockerfile target "orca"), so the first workspace
# does not wait for the full toolchain build. Prints the image tag.
set -euo pipefail

# shellcheck source=docker-lib.sh disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/docker-lib.sh"

# initializeCommand may build or pull the base image the Orca image layers on.
log "initializeCommand (host)"
run_lifecycle host initializeCommand >&2
ensure_image
echo "$IMAGE_TAG"
