#!/usr/bin/env bash
# Seeds Orca Claude/Codex configuration from read-only devcontainer mounts,
# removes legacy shared credential links, and links OpenCode credentials.
#
#   OpenCode  ~/.opencode-host/auth.json       (devcontainer OpenCode volume, Orca only)
#
# Containers sign in to Claude Code and Codex separately from the host so
# container-specific accounts can be used. Orca keeps its Claude and Codex
# logins on per-checkout volumes (orca-<repo>-claude, orca-<repo>-codex); only
# non-credential config files (.claude.json, settings.json, config.toml) are
# seeded once from the read-only *-host mounts.
#
# For OpenCode, a credential is linked only when the shared file exists.
# Without one, the container keeps its own login and nothing is created on the
# host side; a link left from an earlier shared login is removed.
#
# A mount that is absent is skipped, so the same script works in the
# devcontainer (Claude/Codex/OpenCode homes are mounted directly) and in Orca
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

codex_dir="${CODEX_HOME:-${home}/.codex}"
legacy_codex_link="${codex_dir}/auth.json"
case "$(readlink "$legacy_codex_link" 2>/dev/null || true)" in
    "${home}/.codex-host/auth.json"|"${home}/.codex-auth.json")
        rm -f "$legacy_codex_link"
        echo ">>> agent auth: removed the shared Codex login link; log in to Codex in the container"
        ;;
esac
seed_copy "${home}/.codex-host/config.toml" "${codex_dir}/config.toml"

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
