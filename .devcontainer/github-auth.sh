#!/usr/bin/env bash
# Shares a GitHub token from the host with the devcontainer and the Orca
# workspace containers (scripts/orca-vm), so gh and git over HTTPS work there.
#
#   github-auth.sh host       initializeCommand (host): writes the token to
#                             <config>/sailwright/devcontainer/github/token,
#                             which initialize-host.sh bind-mounts read-only
#                             at /run/sailwright-github.
#   github-auth.sh container  postStartCommand: logs gh in with that token,
#                             makes gh the git credential helper for
#                             github.com and rewrites GitHub SSH remotes to
#                             HTTPS.
#
# Token source on the host, first match wins:
#   1. <config>/sailwright/devcontainer/github-token: a token you created for
#      the containers, preferably a fine-grained one limited to the repositories
#      the agents need. An empty file shares nothing.
#   2. `gh auth token` of the host's own gh login.
# <config> is $XDG_CONFIG_HOME or ~/.config (the Windows profile's .config under
# WSL). Everything in the containers can use the token, and with the host's own
# login that is every repository it can reach.
#
# Without a shared token, a login made earlier from a shared token is logged
# out again and the remote rewrite removed; a login of your own made inside the
# container is left alone. Test overrides: GH_CMD (host), GITHUB_AUTH_DIR and
# GITHUB_AUTH_STATE (container).
set -uo pipefail

TOKEN_RE='^[A-Za-z0-9_]+$'
HOST=github.com

log() { echo ">>> github auth: $*"; }

# --- host ------------------------------------------------------------------------

host_gh_token() {
    local gh="${GH_CMD:-}"
    if [ -z "$gh" ]; then
        if command -v gh >/dev/null 2>&1; then gh=gh
        elif command -v gh.exe >/dev/null 2>&1; then gh=gh.exe
        else return 1
        fi
    fi
    "$gh" auth token --hostname "$HOST" 2>/dev/null
}

host() {
    local config_home="${CONFIG_HOME:?CONFIG_HOME is not set}"
    local override="${config_home}/sailwright/devcontainer/github-token"
    local share_dir="${config_home}/sailwright/devcontainer/github"
    local token="" source tmp
    if [ -f "$override" ]; then
        token="$(tr -d '\r\n' <"$override")"
        source="$override"
    else
        token="$(host_gh_token | tr -d '\r\n')" || token=""
        source="the host's gh login"
    fi
    (umask 077 && mkdir -p "$share_dir")
    if [ -n "$token" ] && ! [[ "$token" =~ $TOKEN_RE ]]; then
        log "ignoring the token from ${source} (unexpected characters)" >&2
        token=""
    fi
    if [ -z "$token" ]; then
        rm -f "${share_dir}/token"
        log "no token shared (create ${override} or run \`gh auth login\` on the host)"
        return 0
    fi
    tmp="$(umask 077 && mktemp "${share_dir}/token.XXXXXX")" || return 1
    printf '%s\n' "$token" >"$tmp" && mv -f "$tmp" "${share_dir}/token"
    log "sharing the token from ${source}"
}

# --- container -------------------------------------------------------------------

token_hash() { printf '%s' "$1" | sha256sum | cut -d' ' -f1; }

rewrite_prefixes=("git@${HOST}:" "ssh://git@${HOST}/")
rewrite_key="url.https://${HOST}/.insteadOf"

add_rewrite() {
    local prefix
    for prefix in "${rewrite_prefixes[@]}"; do
        git config --global --get-all "$rewrite_key" 2>/dev/null | grep -qxF "$prefix" \
            || git config --global --add "$rewrite_key" "$prefix"
    done
}

remove_rewrite() {
    local prefix
    for prefix in "${rewrite_prefixes[@]}"; do
        git config --global --fixed-value --unset-all "$rewrite_key" "$prefix" 2>/dev/null || true
    done
}

container() {
    local token_file="${GITHUB_AUTH_DIR:-/run/sailwright-github}/token"
    local state="${GITHUB_AUTH_STATE:-${HOME}/.config/sailwright/github-token.sha256}"
    local token="" current shared_hash=""
    if ! command -v gh >/dev/null 2>&1; then
        log "gh is not installed, skipped"
        return 0
    fi
    [ -f "$state" ] && shared_hash="$(cat "$state")"
    current="$(gh auth token --hostname "$HOST" 2>/dev/null)" || current=""
    [ -r "$token_file" ] && token="$(tr -d '\r\n' <"$token_file")"

    if [ -z "$token" ] || ! [[ "$token" =~ $TOKEN_RE ]]; then
        if [ -n "$shared_hash" ]; then
            if [ -n "$current" ] && [ "$(token_hash "$current")" = "$shared_hash" ]; then
                gh auth logout --hostname "$HOST" >/dev/null 2>&1 || true
                log "logged out the earlier shared login (no shared token any more)"
            fi
            remove_rewrite
            rm -f "$state"
        fi
        log "no shared token; gh and git use the container's own login, if any"
        return 0
    fi

    if [ "$current" != "$token" ]; then
        if ! printf '%s\n' "$token" | gh auth login --hostname "$HOST" --git-protocol https --with-token; then
            log "gh auth login with the shared token failed (offline, expired or missing scopes?)" >&2
            return 0
        fi
    fi
    mkdir -p "$(dirname "$state")"
    (umask 077 && token_hash "$token" >"$state")
    gh auth setup-git --hostname "$HOST"
    add_rewrite
    log "gh logged in with the shared token; git uses HTTPS for ${HOST}"
}

case "${1:-}" in
    host) host ;;
    container) container ;;
    *) echo "usage: $0 host|container" >&2; exit 2 ;;
esac
