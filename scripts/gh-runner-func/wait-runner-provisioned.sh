#!/usr/bin/env bash
# Wait until the runner VM created by request_runner has been provisioned.
#
# Polls the runner_status endpoint of the gh-runner Function App and exits as
# soon as Azure reports a terminal state, so CI fails fast on a provisioning
# failure, Spot eviction or deleted VM instead of waiting for a runner that
# never registers. Requires an `az login` session that can get a token for the
# Function App's audience.
#
# Exit codes: 0 = provisioning succeeded, 1 = failed / not found / deleting /
# timed out / misconfigured.
#
# Required environment:
#   FUNCTION_APP_NAME  Function App name (<name>.azurewebsites.net)
#   AZURE_CLIENT_ID    Client ID of the Function App's app registration (token audience)
#   RESOURCE_GROUP     Resource group from the request_runner response
#   VM_NAME            VM name ("vm") from the request_runner response
# Optional environment:
#   PROVISION_TIMEOUT_SECONDS  Give up after this many seconds (default 900)
#   PROVISION_POLL_SECONDS     Delay between calls (default 10, the endpoint's Retry-After)
#   PROVISION_REQUEST_TIMEOUT  Per-request timeout in seconds (default 60)
#   PROVISION_MAX_ERRORS       Consecutive failed status requests tolerated (default 5)
set -euo pipefail

: "${FUNCTION_APP_NAME:?FUNCTION_APP_NAME is required}"
: "${AZURE_CLIENT_ID:?AZURE_CLIENT_ID is required}"
: "${RESOURCE_GROUP:?RESOURCE_GROUP is required}"
: "${VM_NAME:?VM_NAME is required}"
PROVISION_TIMEOUT_SECONDS="${PROVISION_TIMEOUT_SECONDS:-900}"
PROVISION_POLL_SECONDS="${PROVISION_POLL_SECONDS:-10}"
PROVISION_REQUEST_TIMEOUT="${PROVISION_REQUEST_TIMEOUT:-60}"
PROVISION_MAX_ERRORS="${PROVISION_MAX_ERRORS:-5}"

resource="api://${AZURE_CLIENT_ID}"
url="https://${FUNCTION_APP_NAME}.azurewebsites.net/api/runner_status"
body="$(python3 -c 'import json, sys; print(json.dumps({"resource-group": sys.argv[1], "vm": sys.argv[2]}))' "${RESOURCE_GROUP}" "${VM_NAME}")"

# Print the "status" field of a JSON document embedded in the given text, or
# "unknown". az rest prints the response body of failed requests to stderr.
json_status() {
  python3 -c '
import json, re, sys
text = sys.stdin.read()
for candidate in [text] + re.findall(r"\{.*\}", text, re.S):
    try:
        print(json.loads(candidate).get("status") or "unknown")
        break
    except (ValueError, AttributeError):
        continue
else:
    print("unknown")
'
}

deadline=$(( $(date +%s) + PROVISION_TIMEOUT_SECONDS ))
errors=0
err_file="$(mktemp)"
trap 'rm -f "${err_file}"' EXIT

while true; do
  status="unknown"
  response=""
  if response="$(timeout "${PROVISION_REQUEST_TIMEOUT}" az rest \
      --only-show-errors \
      --method post \
      --uri "${url}" \
      --resource "${resource}" \
      --body "${body}" 2>"${err_file}")"; then
    status="$(json_status <<< "${response}")"
    errors=0
  else
    status="$(json_status < "${err_file}")"
    if [[ "${status}" == "unknown" ]]; then
      error_text="$(cat "${err_file}")"
      if [[ "${error_text}" =~ (^|[^0-9])404([^0-9]|$)|Not[[:space:]]?Found ]]; then
        echo "::error::runner_status route unavailable (404 without a JSON status). Deploy the updated Function App before polling." >&2
        exit 1
      fi
      errors=$(( errors + 1 ))
      echo "Status request failed (${errors}/${PROVISION_MAX_ERRORS}): $(head -c 500 "${err_file}")"
    fi
  fi
  echo "Runner VM ${RESOURCE_GROUP}/${VM_NAME}: status=${status}"

  case "${status}" in
    succeeded)
      echo "Runner VM provisioning succeeded."
      exit 0
      ;;
    failed)
      echo "::error::Runner VM ${VM_NAME} failed to provision or stopped: ${response:-$(cat "${err_file}")}" >&2
      exit 1
      ;;
    not-found|deleting)
      echo "::error::Runner VM ${VM_NAME} in ${RESOURCE_GROUP} is ${status}." >&2
      exit 1
      ;;
  esac

  if (( errors >= PROVISION_MAX_ERRORS )); then
    echo "::error::runner_status failed ${errors} times in a row." >&2
    exit 1
  fi
  if (( $(date +%s) + PROVISION_POLL_SECONDS > deadline )); then
    echo "::error::Runner VM ${VM_NAME} was not provisioned within ${PROVISION_TIMEOUT_SECONDS}s." >&2
    exit 1
  fi
  sleep "${PROVISION_POLL_SECONDS}"
done
