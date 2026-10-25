#!/usr/bin/env bash
# Gives each clone's VS Code window its own color, so parallel clones
# (sailwright-a, sailwright-b, ...) are easy to tell apart.
#
# Runs inside the devcontainer on every start (postStartCommand). The clone
# name is the workspace folder name; the part after the last "-" picks the
# color. Names without a suffix (e.g. "sailwright") are left uncolored.
#
# The colors go into the container's remote "Machine" settings instead of
# .vscode/settings.json, so the tracked workspace settings stay untouched.
# Only workbench.colorCustomizations and window.title are managed here.
#
# Usage: clone-color.sh [workspace-folder]   (default: the repo root)
# Env:   VSCODE_MACHINE_SETTINGS overrides the settings file path.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOLDER="${1:-$(cd "$SCRIPT_DIR/.." && pwd)}"
SETTINGS="${VSCODE_MACHINE_SETTINGS:-$HOME/.vscode-server/data/Machine/settings.json}"

NAME="$(basename "$FOLDER")"
SUFFIX="${NAME##*-}"
if [ "$SUFFIX" = "$NAME" ] || [ -z "$SUFFIX" ]; then
  echo "clone-color: '$NAME' has no -suffix, leaving colors alone"
  exit 0
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "clone-color: jq not found, skipping" >&2
  exit 0
fi

# Distinct, dark enough for white text. a, b, c, ... map in order; any other
# suffix is hashed onto the same palette so it still gets a stable color.
PALETTE=(
  "#1f5fa8" # a blue
  "#2e7d32" # b green
  "#b45309" # c orange
  "#7b2d8e" # d purple
  "#b3261e" # e red
  "#00796b" # f teal
  "#a1306b" # g pink
  "#5d4037" # h brown
)
lower="$(printf '%s' "$SUFFIX" | tr '[:upper:]' '[:lower:]')"
if [[ "$lower" =~ ^[a-z]$ ]]; then
  index=$(($(printf '%d' "'$lower") - 97))
else
  index=$(printf '%s' "$lower" | cksum | cut -d' ' -f1)
fi
BASE="${PALETTE[$((index % ${#PALETTE[@]}))]}"

# Inactive title bar: the same color at ~65% brightness.
dim() {
  local hex="${1#\#}" r g b
  r=$((16#${hex:0:2} * 65 / 100))
  g=$((16#${hex:2:2} * 65 / 100))
  b=$((16#${hex:4:2} * 65 / 100))
  printf '#%02x%02x%02x' "$r" "$g" "$b"
}
DIM="$(dim "$BASE")"

mkdir -p "$(dirname "$SETTINGS")"
[ -s "$SETTINGS" ] || echo '{}' >"$SETTINGS"

TMP="$(mktemp "$SETTINGS.XXXXXX")"
if ! jq --arg base "$BASE" --arg dim "$DIM" --arg suffix "$SUFFIX" '
  .["workbench.colorCustomizations"] = ((.["workbench.colorCustomizations"] // {}) + {
    "titleBar.activeBackground": $base,
    "titleBar.activeForeground": "#ffffff",
    "titleBar.inactiveBackground": $dim,
    "titleBar.inactiveForeground": "#d0d0d0",
    "activityBar.background": $base,
    "activityBar.foreground": "#ffffff",
    "activityBar.inactiveForeground": "#ffffffaa",
    "statusBar.background": $base,
    "statusBar.foreground": "#ffffff",
    "statusBar.noFolderBackground": $base
  })
  | .["window.title"] = ("${rootName} [" + $suffix + "]${separator}${activeEditorShort}")
' "$SETTINGS" >"$TMP" 2>/dev/null; then
  rm -f "$TMP"
  echo "clone-color: $SETTINGS is not plain JSON (comments?), left unchanged" >&2
  exit 0
fi
cat "$TMP" >"$SETTINGS"
rm -f "$TMP"
echo "clone-color: '$NAME' -> $BASE"
