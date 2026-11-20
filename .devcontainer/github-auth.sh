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
# Token source on the host:
#   1. <config>/sailwright/devcontainer/github-token: a token you created for
#      the containers, preferably a fine-grained one limited to the repositories
#      the agents work on (see .devcontainer/README.md). An empty file shares
#      nothing.
#   2. Only with SAILWRIGHT_GH_SHARE_HOST_LOGIN=1: `gh auth token` of the host's
#      own gh login, with a warning, because every process in the containers
#      then holds every scope and repository of that login. When no gh is
#      found at all (e.g. WSL's bash without the Windows PATH), a token shared
#      earlier is kept rather than removed.
#   Otherwise no token is shared, and a token shared earlier is removed.
# <config> is $XDG_CONFIG_HOME or ~/.config (the Windows profile's .config under
# WSL). Classic tokens (ghp_, gho_) are checked for risky scopes through the
# X-OAuth-Scopes header of the GitHub API; the token itself is never printed
# or put on a command line.
#
# Without a shared token, a login made earlier from a shared token is logged
# out again and the remote rewrite removed; a login of your own made inside the
# container is left alone. Test overrides: GH_CMD (host; "none" means no gh),
# GITHUB_SCOPES_CMD (host; prints the scopes of the token on stdin, "none"
# skips the check), GITHUB_AUTH_DIR and GITHUB_AUTH_STATE (container).
set -uo pipefail

TOKEN_RE='^[A-Za-z0-9_]+$'
HOST=github.com

log() { echo ">>> github auth: $*"; }

# --- host ------------------------------------------------------------------------

# Prints the host's gh, or fails when there is none. VS Code may run
# initializeCommand with WSL's bash.exe, whose PATH often lacks the Windows
# gh.exe: ask Windows where it is.
host_gh() {
    local bin
    case "${GH_CMD:-}" in none) return 1 ;; ?*) printf "%s" "$GH_CMD"; return 0 ;; esac
    for bin in gh gh.exe; do
        command -v "$bin" >/dev/null 2>&1 && { printf '%s' "$bin"; return 0; }
    done
    if command -v wslpath >/dev/null 2>&1 && command -v cmd.exe >/dev/null 2>&1; then
        bin="$(cmd.exe /d /c where gh.exe 2>/dev/null | head -n 1 | tr -d '\r')"
        [ -n "$bin" ] && bin="$(wslpath -u "$bin" 2>/dev/null)" && [ -x "$bin" ] \
            && { printf '%s' "$bin"; return 0; }
    fi
    return 1
}

# Prints the X-OAuth-Scopes header GitHub returns for the token on stdin (empty
# for fine-grained tokens), or fails when GitHub cannot be asked. The token is
# passed to curl through a config on stdin, never as an argument.
token_scopes() {
    local token headers
    IFS= read -r token || return 1
    command -v curl >/dev/null 2>&1 || return 1
    headers="$(printf 'header = "Authorization: token %s"\n' "$token" \
        | curl -q -fsS -I --max-time 5 --config - "https://api.${HOST}/" 2>/dev/null)" || return 1
    printf '%s\n' "$headers" | tr -d '\r' | sed -n 's/^[Xx]-[Oo][Aa]uth-[Ss]copes:[[:space:]]*//p'
}

# Warns about scopes of a classic token that let a stolen token do much more
# than push branches and open pull requests. Never fails.
warn_risky_scopes() {
    local token="$1" scopes scope risky=""
    case "$token" in ghp_*|gho_*|ghu_*) ;; *) return 0 ;; esac
    case "${GITHUB_SCOPES_CMD:-}" in
        none) return 0 ;;
        ?*) scopes="$(printf '%s\n' "$token" | "$GITHUB_SCOPES_CMD")" || return 0 ;;
        *) scopes="$(printf '%s\n' "$token" | token_scopes)" || return 0 ;;
    esac
    for scope in $(printf '%s' "$scopes" | tr ',' ' '); do
        case "$scope" in
            workflow|delete_repo|write:packages|admin:*) risky="${risky:+${risky}, }${scope}" ;;
        esac
    done
    [ -z "$risky" ] || log "WARNING: the shared token has the scopes ${risky}; anything running in the containers can use them (prefer a fine-grained token, see .devcontainer/README.md)" >&2
}

host() {
    local config_home="${CONFIG_HOME:?CONFIG_HOME is not set}"
    local override="${config_home}/sailwright/devcontainer/github-token"
    local share_dir="${config_home}/sailwright/devcontainer/github"
    local token="" source tmp gh
    (umask 077 && mkdir -p "$share_dir")
    if [ -f "$override" ]; then
        token="$(tr -d '\r\n' <"$override")"
        source="$override"
    elif [ "${SAILWRIGHT_GH_SHARE_HOST_LOGIN:-}" != 1 ]; then
        rm -f "${share_dir}/token"
        log "no token shared. Create a fine-grained token for the repositories the agents work on" \
            "(https://github.com/settings/personal-access-tokens/new, see .devcontainer/README.md)" \
            "and save it to ${override} (chmod 600), or set SAILWRIGHT_GH_SHARE_HOST_LOGIN=1" \
            "to share the host's gh login with all of its rights"
        return 0
    elif gh="$(host_gh)"; then
        token="$("$gh" auth token --hostname "$HOST" 2>/dev/null | tr -d '\r\n')" || token=""
        source="the host's gh login"
    else
        # The login state is unknown here, not gone: keep what an earlier run
        # shared, so running containers and new Orca workspaces keep working.
        if [ -s "${share_dir}/token" ]; then
            log "gh not found on this host; keeping the token shared earlier"
        else
            log "no token shared (gh not found; create ${override} or install gh and run \`gh auth login\`)"
        fi
        return 0
    fi
    if [ -n "$token" ] && ! [[ "$token" =~ $TOKEN_RE ]]; then
        log "ignoring the token from ${source} (unexpected characters)" >&2
        token=""
    fi
    if [ -z "$token" ]; then
        rm -f "${share_dir}/token"
        log "no token shared (nothing usable in ${source})"
        return 0
    fi
    tmp="$(umask 077 && mktemp "${share_dir}/token.XXXXXX")" || return 1
    printf '%s\n' "$token" >"$tmp" && mv -f "$tmp" "${share_dir}/token"
    if [ "$source" = "$override" ]; then
        log "sharing the token from ${source}"
    else
        log "WARNING: sharing the host's gh login (SAILWRIGHT_GH_SHARE_HOST_LOGIN=1): every process in the containers, coding agents and package install scripts included, can use it with all of its scopes and repositories. Prefer a fine-grained token in ${override}" >&2
    fi
    warn_risky_scopes "$token"
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
    local legacy="${HOME}/.config/orca-docker/github-token.sha256"
    local token="" current shared_hash="" marker
    if ! command -v gh >/dev/null 2>&1; then
        log "gh is not installed, skipped"
        return 0
    fi
    current="$(gh auth token --hostname "$HOST" 2>/dev/null)" || current=""
    [ -r "$token_file" ] && token="$(tr -d '\r\n' <"$token_file")"
    for marker in "$state" "$legacy"; do
        [ -f "$marker" ] || continue
        shared_hash="$(cat "$marker")"
        if [ -n "$current" ] && [ "$(token_hash "$current")" = "$shared_hash" ] && [ "$current" != "$token" ]; then
            if ! gh auth logout --hostname "$HOST" >/dev/null 2>&1; then
                log "WARNING: logout failed; keeping the shared-login marker for retry"
                return 0
            fi
            local after
            after="$(gh auth token --hostname "$HOST" 2>/dev/null)" || after=""
            if [ "$after" = "$current" ]; then
                log "WARNING: shared login remains; keeping its marker for retry"
                return 0
            fi
            current="$after"
            [ -n "$current" ] || remove_rewrite
            log "logged out the earlier shared login; revoke previously exposed tokens at GitHub Settings > Applications or personal access tokens"
        fi
        [ "$marker" != "$legacy" ] || rm -f "$legacy"
    done
    shared_hash=""
    [ ! -f "$state" ] || shared_hash="$(cat "$state")"
    if [ -z "$token" ] || ! [[ "$token" =~ $TOKEN_RE ]]; then
        if [ -n "$shared_hash" ]; then
            [ -n "$current" ] || remove_rewrite
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
