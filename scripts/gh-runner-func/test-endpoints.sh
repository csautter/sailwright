#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Test Azure runner broker endpoints with Entra auth.

Optional:
  --function-app <name>    Azure Function App name (without .azurewebsites.net)
  --api-client-id <id>     App registration client ID used as API audience
  --repo <owner/repo>      GitHub repo (default: csautter/sailwright)
  --resource-group <name>  Resource group name
  --runner-name <name>     Runner name
  --flavor <hyperv|virtualbox>  Virtualization flavor (default: hyperv)
  --admin-azure-profile-dir <path> Azure CLI config dir for admin lookups (default: ~/.azure)
  --terragrunt-output-dir <path>  Terragrunt dir for output discovery
  --request-runner         Call request_runner endpoint
  --delete-resource-group  Call delete_resource_group endpoint
  -h, --help               Show this help

Examples:
  bash scripts/gh-runner-func/test-endpoints.sh \
    --request-runner

  bash scripts/gh-runner-func/test-endpoints.sh \
    --resource-group gh-runner-tmp-local-12345 \
    --runner-name local-12345 \
    --delete-resource-group
EOF
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

FUNCTION_APP_NAME="${FUNCTION_APP_NAME:-}"
API_CLIENT_ID="${API_CLIENT_ID:-}"
REPO="${REPO:-csautter/sailwright}"
RESOURCE_GROUP="${RESOURCE_GROUP:-}"
RUNNER_NAME="${RUNNER_NAME:-}"
VIRTUALIZATION_FLAVOR="${VIRTUALIZATION_FLAVOR:-hyperv}"
REQUEST_RUNNER=false
DELETE_RESOURCE_GROUP=false
AZURE_ADMIN_CONFIG_DIR="${AZURE_ADMIN_CONFIG_DIR:-$HOME/.azure}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
TERRAGRUNT_OUTPUT_DIR="${TERRAGRUNT_OUTPUT_DIR:-$REPO_ROOT/deployments/terraform/env/azure_dev/azure_gh_runner}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --function-app)
      FUNCTION_APP_NAME="$2"
      shift 2
      ;;
    --api-client-id)
      API_CLIENT_ID="$2"
      shift 2
      ;;
    --repo)
      REPO="$2"
      shift 2
      ;;
    --resource-group)
      RESOURCE_GROUP="$2"
      shift 2
      ;;
    --runner-name)
      RUNNER_NAME="$2"
      shift 2
      ;;
    --flavor)
      VIRTUALIZATION_FLAVOR="$2"
      shift 2
      ;;
    --admin-azure-profile-dir)
      AZURE_ADMIN_CONFIG_DIR="$2"
      shift 2
      ;;
    --terragrunt-output-dir)
      TERRAGRUNT_OUTPUT_DIR="$2"
      shift 2
      ;;
    --request-runner)
      REQUEST_RUNNER=true
      shift
      ;;
    --delete-resource-group)
      DELETE_RESOURCE_GROUP=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ "$REQUEST_RUNNER" == "false" && "$DELETE_RESOURCE_GROUP" == "false" ]]; then
  echo "You must provide exactly one operation flag: --request-runner or --delete-resource-group" >&2
  exit 1
fi

if [[ "$REQUEST_RUNNER" == "true" && "$DELETE_RESOURCE_GROUP" == "true" ]]; then
  echo "Use only one operation at a time: --request-runner OR --delete-resource-group" >&2
  exit 1
fi

case "$VIRTUALIZATION_FLAVOR" in
  hyperv|virtualbox) ;;
  *)
    echo "--flavor must be one of: hyperv, virtualbox" >&2
    exit 1
    ;;
esac

require_cmd az

resolve_from_terragrunt_output() {
  local key="$1"
  if ! command -v terragrunt >/dev/null 2>&1; then
    return 0
  fi
  if [[ ! -d "$TERRAGRUNT_OUTPUT_DIR" ]]; then
    return 0
  fi
  (
    cd "$TERRAGRUNT_OUTPUT_DIR" || exit 0
    terragrunt output -raw "$key" 2>/dev/null || true
  )
}

if [[ -z "$API_CLIENT_ID" ]]; then
  API_CLIENT_ID="$(resolve_from_terragrunt_output azure_ad_app_client_id)"
fi
if [[ -z "$FUNCTION_APP_NAME" ]]; then
  FUNCTION_APP_NAME="$(resolve_from_terragrunt_output function_app_name)"
fi

if [[ -z "$API_CLIENT_ID" && -d "$AZURE_ADMIN_CONFIG_DIR" ]]; then
  API_CLIENT_ID="$(AZURE_CONFIG_DIR="$AZURE_ADMIN_CONFIG_DIR" az ad app list --display-name gh-actions-runner-broker --query '[0].appId' -o tsv 2>/dev/null || true)"
fi
if [[ -z "$FUNCTION_APP_NAME" ]]; then
  echo "FUNCTION_APP_NAME is required. Pass --function-app or set FUNCTION_APP_NAME." >&2
  exit 1
fi
if [[ -z "$API_CLIENT_ID" ]]; then
  echo "API_CLIENT_ID is required. Pass --api-client-id or set API_CLIENT_ID." >&2
  exit 1
fi
BASE_URL="https://${FUNCTION_APP_NAME}.azurewebsites.net/api"
RESOURCE="api://${API_CLIENT_ID}"

if [[ "$REQUEST_RUNNER" == "true" ]]; then
  if [[ -z "$RESOURCE_GROUP" ]]; then
    RESOURCE_GROUP="gh-runner-tmp-local-$(date +%s)"
  fi
  if [[ -z "$RUNNER_NAME" ]]; then
    RUNNER_NAME="local-$(date +%s | tail -c 9)"
  fi

  request_body=$(
    printf '{"repo":"%s","resource-group":"%s","runner-name":"%s","virtualization-flavor":"%s"}' \
      "$REPO" "$RESOURCE_GROUP" "$RUNNER_NAME" "$VIRTUALIZATION_FLAVOR"
  )

  echo "Calling request_runner..."
  echo "  function app: ${FUNCTION_APP_NAME}"
  echo "  repo: ${REPO}"
  echo "  resource group: ${RESOURCE_GROUP}"
  echo "  runner name: ${RUNNER_NAME}"
  echo "  flavor: ${VIRTUALIZATION_FLAVOR}"

  az rest \
    --only-show-errors \
    --method post \
    --uri "${BASE_URL}/request_runner" \
    --resource "$RESOURCE" \
    --headers "Content-Type=application/json" \
    --body "$request_body" \
    --output jsonc

  echo "request_runner succeeded."
fi

if [[ "$DELETE_RESOURCE_GROUP" == "true" ]]; then
  if [[ -z "$RESOURCE_GROUP" ]]; then
    echo "--resource-group is required for --delete-resource-group" >&2
    exit 1
  fi

  delete_body=$(
    printf '{"resource-group":"%s","runner-name":"%s"}' \
      "$RESOURCE_GROUP" ""
  )

  echo "Calling delete_resource_group..."
  az rest \
    --only-show-errors \
    --method post \
    --uri "${BASE_URL}/delete_resource_group" \
    --resource "$RESOURCE" \
    --headers "Content-Type=application/json" \
    --body "$delete_body" \
    --output jsonc
  echo "delete_resource_group succeeded (status \"deleting\" means deletion is still in progress; call again to check)."
fi
