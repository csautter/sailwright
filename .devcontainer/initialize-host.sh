#!/usr/bin/env bash
set -euo pipefail

# Resolve user home directory across Unix and Windows environments
USER_HOME="${HOME:-${USERPROFILE:-}}"

if [ -z "$USER_HOME" ]; then
  echo "Error: Neither HOME nor USERPROFILE is set." >&2
  exit 1
fi

# Ensure required host directories exist before bind-mounting
mkdir -p "${USER_HOME}/.sailwright-gateway"
mkdir -p "${USER_HOME}/.gemini/config"

# Ensure ~/.gemini/config/skills is a real directory mountpoint, not a dangling symlink
SKILLS_DIR="${USER_HOME}/.gemini/config/skills"
if [ -L "$SKILLS_DIR" ]; then
  rm "$SKILLS_DIR"
fi
mkdir -p "$SKILLS_DIR"

