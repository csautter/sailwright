#!/usr/bin/env bash
# Delete a broker-created runner resource group through the gh-runner Function App
# and wait until it is gone.
#
# The delete_resource_group endpoint starts the deletion and returns
# {"status": "deleting"}; it returns {"status": "deleted"} once the resource group
# no longer exists. This script calls it until it reports "deleted", so a request
# that fails (e.g. a gateway time-out) or a deletion already in progress is
# simply retried. Requires an `az login` session that can get a token for the
# Function App's audience.
#
# Required environment:
#   FUNCTION_APP_NAME  Function App name (<name>.azurewebsites.net)
#   AZURE_CLIENT_ID    Client ID of the Function App's app registration (token audience)
#   RESOURCE_GROUP     Resource group to delete
# Optional environment:
#   RUNNER_NAME                Runner name sent with the request
#   DELETE_TIMEOUT_SECONDS     Give up after this many seconds (default 1200)
#   DELETE_POLL_SECONDS        Delay between calls (default 20)
#   DELETE_REQUEST_TIMEOUT     Per-request timeout in seconds (default 120)
set -euo pipefail

: "${FUNCTION_APP_NAME:?FUNCTION_APP_NAME is required}"
: "${AZURE_CLIENT_ID:?AZURE_CLIENT_ID is required}"
: "${RESOURCE_GROUP:?RESOURCE_GROUP is required}"
RUNNER_NAME="${RUNNER_NAME:-}"
DELETE_TIMEOUT_SECONDS="${DELETE_TIMEOUT_SECONDS:-1200}"
DELETE_POLL_SECONDS="${DELETE_POLL_SECONDS:-20}"
DELETE_REQUEST_TIMEOUT="${DELETE_REQUEST_TIMEOUT:-120}"

resource="api://${AZURE_CLIENT_ID}"
url="https://${FUNCTION_APP_NAME}.azurewebsites.net/api/delete_resource_group"
body="$(python3 -c 'import json, sys; print(json.dumps({"resource-group": sys.argv[1], "runner-name": sys.argv[2]}))' "${RESOURCE_GROUP}" "${RUNNER_NAME}")"

deadline=$(( $(date +%s) + DELETE_TIMEOUT_SECONDS ))
attempt=0
while true; do
  attempt=$(( attempt + 1 ))
  echo "Delete request ${attempt} for ${RESOURCE_GROUP}"

  status=""
  if response="$(timeout "${DELETE_REQUEST_TIMEOUT}" az rest \
      --method post \
      --uri "${url}" \
      --resource "${resource}" \
      --body "${body}")"; then
    # Responses without a status field come from Function App versions that
    # only answered after the deletion had finished.
    status="$(python3 -c 'import json, sys; print(json.loads(sys.stdin.read() or "{}").get("status", "deleted"))' <<< "${response}" 2>/dev/null || echo "unknown")"
    echo "Function App reported status=${status}"
  else
    echo "Delete request failed or timed out; retrying."
  fi

  if [[ "${status}" == "deleted" ]]; then
    echo "Confirmed deletion of ${RESOURCE_GROUP}."
    exit 0
  fi

  if (( $(date +%s) + DELETE_POLL_SECONDS > deadline )); then
    echo "::error::${RESOURCE_GROUP} was not confirmed deleted within ${DELETE_TIMEOUT_SECONDS}s." >&2
    exit 1
  fi

  sleep "${DELETE_POLL_SECONDS}"
done
