#!/usr/bin/env bash
# Personal overrides must never become fixture outputs.
set -euo pipefail
[ "${AUTH_DEV_ISOLATION_CHILD:-}" != 1 ] || exit 0
SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
for suite in github-auth.sh github-auth-migration.sh; do
    case_dir="$(mktemp -d)"
    printf 'outside-sentinel\n' >"$case_dir/token"
    AUTH_DEV_ISOLATION_CHILD=1 ORCA_DOCKER_GITHUB_TOKEN_FILE="$case_dir/token" \
        ORCA_DOCKER_STATE_DIR="$case_dir/state" GITHUB_AUTH_STATE="$case_dir/token" \
        GITHUB_AUTH_DIR="$case_dir/share" CONFIG_HOME="$case_dir/config" XDG_CONFIG_HOME="$case_dir/config" \
        SAILWRIGHT_GH_SHARE_HOST_LOGIN=1 bash "$SRC/$suite" >"$case_dir/suite.log" 2>&1
    [ "$(cat "$case_dir/token")" = outside-sentinel ]
    [ ! -e "$case_dir/state" ]
    [ ! -e "$case_dir/share" ]
    [ ! -e "$case_dir/config" ]
done
printf 'github auth devcontainer isolation: 2 entrypoints passed\n'
