#!/usr/bin/env bash
# Start both public suite entrypoints with hostile inherited fixture paths.
set -euo pipefail
[ "${AUTH_ISOLATION_CHILD:-}" != 1 ] || exit 0
SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
for suite in run.sh github-auth-regression.sh; do
    case_dir="$(mktemp -d)"
    printf 'outside-sentinel\n' >"$case_dir/token"
    AUTH_ISOLATION_CHILD=1 ORCA_DOCKER_GITHUB_TOKEN_FILE="$case_dir/token" \
        ORCA_DOCKER_STATE_DIR="$case_dir/personal-state" XDG_CONFIG_HOME="$case_dir/personal-config" \
        SAILWRIGHT_GH_SHARE_HOST_LOGIN=1 bash "$SRC/$suite" >"$case_dir/suite.log" 2>&1
    [ "$(cat "$case_dir/token")" = outside-sentinel ]
    [ ! -e "$case_dir/personal-state" ]
    [ ! -e "$case_dir/personal-config" ]
done
printf 'github auth isolation: 2 entrypoints passed\n'
