#!/usr/bin/env bash
# Links Codex and OpenCode credentials from host-backed mounts into their
# container-owned agent homes and seeds the Orca Claude configuration.
#
#   Codex     ~/.codex-host/auth.json          (host ~/.codex)
#   OpenCode  ~/.opencode-host/auth.json       (devcontainer OpenCode volume, Orca only)
#
# Orca keeps its Claude login on a per-checkout volume. Claude Code replaces
# the credentials file on refresh, so a symlink would leave the source stale.
# Only onboarding state and settings come from the read-only Claude seed mount.
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

seed_copy() (
    local src="$1" dst="$2" tmp
    [ -f "$src" ] && [ ! -e "$dst" ] && [ ! -L "$dst" ] || return 0
    mkdir -p "$(dirname "$dst")" || return 1
    tmp="$(mktemp "${dst}.XXXXXX")" || return 1
    trap 'rm -f "$tmp"' EXIT
    cp -p "$src" "$tmp" || return 1
    # Publish the complete file once, even when multiple Orca workspaces start.
    if ln "$tmp" "$dst" 2>/dev/null; then
        echo ">>> agent auth: seeded $dst"
    elif [ ! -e "$dst" ] && [ ! -L "$dst" ]; then
        echo ">>> agent auth: could not seed $dst" >&2
        return 1
    fi
)

link_credential "${home}/.codex-host/auth.json" "${home}/.codex/auth.json"

claude_dir="${CLAUDE_CONFIG_DIR:-${home}/.claude}"
legacy_claude_link="${claude_dir}/.credentials.json"
if [ -L "$legacy_claude_link" ] && [ "$(readlink "$legacy_claude_link")" = "${home}/.claude-host/.credentials.json" ]; then
    rm -f "$legacy_claude_link"
    echo ">>> agent auth: removed the shared Claude login link; log in to Claude in Orca"
fi
# Account/onboarding state and settings: copied once, then owned by the container.
seed_copy "${home}/.claude-host/.claude.json" "${claude_dir}/.claude.json"
seed_copy "${home}/.claude-host/settings.json" "${claude_dir}/settings.json"

link_credential "${home}/.opencode-host/auth.json" "${home}/.local/share/opencode/auth.json"
