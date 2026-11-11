#!/usr/bin/env bash
# Links agent credentials from host-backed mounts into the container-owned
# agent homes. Only the credential files are shared; caches, sqlite state,
# sessions and hook approvals stay per container.
#
#   Codex     ~/.codex-host/auth.json          (host ~/.codex)
#   Claude    ~/.claude-host/.credentials.json (host ~/.claude-devcontainer, Orca only)
#   OpenCode  ~/.opencode-host/auth.json       (devcontainer OpenCode volume, Orca only)
#
# A credential is linked only when the shared file exists. Without one, the
# container keeps its own login and nothing is created on the host side; a
# link left from an earlier shared login is removed so a new login cannot
# write through it. Token refreshes of a linked login do update the shared
# file, so rotated refresh tokens stay valid everywhere.
#
# A mount that is absent is skipped, so the same script works in the
# devcontainer (Claude/OpenCode homes are mounted directly) and in Orca
# containers (scripts/orca-vm). AGENT_AUTH_HOME (default $HOME) exists for tests.
set -uo pipefail

home="${AGENT_AUTH_HOME:-$HOME}"

link_credential() {
    local src="$1" dst="$2"
    if [ ! -f "$src" ]; then
        if [ -L "$dst" ] && [ "$(readlink "$dst")" = "$src" ]; then
            rm -f "$dst"
            echo ">>> agent auth: removed link ${dst} (no shared login at ${src})"
        fi
        return 0
    fi
    mkdir -p "$(dirname "$dst")"
    if [ -f "$dst" ] && [ ! -L "$dst" ]; then
        echo ">>> agent auth: replacing the container-local login ${dst} with the shared one"
    fi
    ln -sfn "$src" "$dst"
    echo ">>> agent auth: $dst -> $src"
}

seed_copy() {
    local src="$1" dst="$2"
    [ -f "$src" ] && [ ! -e "$dst" ] || return 0
    mkdir -p "$(dirname "$dst")"
    cp -p "$src" "$dst"
    echo ">>> agent auth: seeded $dst"
}

link_credential "${home}/.codex-host/auth.json" "${home}/.codex/auth.json"

claude_dir="${CLAUDE_CONFIG_DIR:-${home}/.claude}"
link_credential "${home}/.claude-host/.credentials.json" "${claude_dir}/.credentials.json"
# Account/onboarding state and settings: copied once, then owned by the container.
seed_copy "${home}/.claude-host/.claude.json" "${claude_dir}/.claude.json"
seed_copy "${home}/.claude-host/settings.json" "${claude_dir}/settings.json"

link_credential "${home}/.opencode-host/auth.json" "${home}/.local/share/opencode/auth.json"
