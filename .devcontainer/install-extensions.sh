#!/usr/bin/env bash
# Installs the VS Code extensions declared in devcontainer.json's
# customizations.vscode.extensions list.
#
# Some devcontainer launchers don't apply that list automatically on every
# start, especially once a persisted extensions volume already has content
# from a prior container. This script makes the install explicit and
# idempotent so a rebuild always ends up with the declared extensions
# present, regardless of which tool launched the container.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEVCONTAINER_JSON="$SCRIPT_DIR/devcontainer.json"
LOG_FILE="/tmp/install-extensions.log"

if ! command -v jq >/dev/null 2>&1; then
  echo "$(date -Iseconds) jq not found, skipping extension install" >>"$LOG_FILE"
  exit 0
fi

extensions=$(jq -r '.customizations.vscode.extensions[]?' "$DEVCONTAINER_JSON" 2>/dev/null)
if [ -z "$extensions" ]; then
  exit 0
fi

# The `code` remote CLI only works once an editor window has attached and
# created its IPC socket, which can happen after this postStartCommand runs.
# Poll for up to 5 minutes before giving up.
ready=false
for _ in $(seq 1 60); do
  if command -v code >/dev/null 2>&1 && code --list-extensions >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 5
done

if [ "$ready" != true ]; then
  echo "$(date -Iseconds) code CLI never became available, skipping extension install" >>"$LOG_FILE"
  exit 0
fi

installed=$(code --list-extensions 2>/dev/null | tr '[:upper:]' '[:lower:]')

while IFS= read -r ext; do
  [ -z "$ext" ] && continue
  ext_lower=$(echo "$ext" | tr '[:upper:]' '[:lower:]')
  if echo "$installed" | grep -qxF "$ext_lower"; then
    continue
  fi
  echo "$(date -Iseconds) installing $ext" >>"$LOG_FILE"
  code --install-extension "$ext" >>"$LOG_FILE" 2>&1
done <<<"$extensions"
