#!/usr/bin/env bash
# Expects the following environment variables (set by the composite action):
#   CACHE_FILES       - JSON array of cache file descriptors
#   CACHE_BACKEND     - Remote cache backend: ftp or azure
#   SUBSCRIPTION_ID   - Azure subscription ID (azure backend only)
#   FTP_*             - FTP server, port, username, password, and base dir. Explicit TLS is enforced.
#   LOCAL_CACHE_DIR   - Path to the local runner shared-volume cache (may be empty)
set -euo pipefail

CACHE_BACKEND=$(printf '%s' "${CACHE_BACKEND:-ftp}" | tr '[:upper:]' '[:lower:]')
RESOURCE_GROUP="gh-runner-storage-rg"
FTP_READY=false
FTP_BASE_URL=""
FTP_BASE_DIR_NORMALIZED=""
FTP_CURL_COMMON_ARGS=()
FTP_CURL_TRANSFER_ARGS=()

fail() {
  echo "  ✗ $*" >&2
  exit 1
}

derive_azure_storage_account() {
  if [ -z "${SUBSCRIPTION_ID:-}" ]; then
    fail "subscription-id is required when storage-backend is 'azure'."
  fi

  echo "ghrunner${SUBSCRIPTION_ID}" |
    tr -cd '[:alnum:]' |
    tr '[:upper:]' '[:lower:]' |
    cut -c1-24
}

normalize_ftp_server() {
  local server="$1"
  server="${server#ftp://}"
  server="${server#ftps://}"
  server="${server%/}"

  if [[ "$server" == */* ]]; then
    fail "FTP server must be a hostname, optionally with a port. Put paths in ftp-base-dir."
  fi

  if [ -n "${FTP_PORT:-}" ] && [[ "$server" != *:* ]]; then
    server="${server}:${FTP_PORT}"
  fi

  printf 'ftp://%s' "$server"
}

normalize_ftp_base_dir() {
  local base_dir="${1:-/private}"
  base_dir="${base_dir//\\//}"
  base_dir="/${base_dir#/}"
  base_dir="${base_dir%/}"

  if [ -z "$base_dir" ]; then
    base_dir="/private"
  fi

  printf '%s' "$base_dir"
}

initialize_ftp() {
  if [ "$FTP_READY" = "true" ]; then
    return
  fi

  FTP_SERVER="${FTP_SERVER:-${BUILD_CACHE_FTP_SERVER:-}}"
  FTP_PORT="${FTP_PORT:-${BUILD_CACHE_FTP_PORT:-}}"
  FTP_USERNAME="${FTP_USERNAME:-${BUILD_CACHE_FTP_USERNAME:-}}"
  FTP_PASSWORD="${FTP_PASSWORD:-${BUILD_CACHE_FTP_PASSWORD:-}}"
  FTP_BASE_DIR="${FTP_BASE_DIR:-${BUILD_CACHE_FTP_BASE_DIR:-/private}}"

  local missing=()
  [ -n "$FTP_SERVER" ] || missing+=("FTP_SERVER")
  [ -n "$FTP_USERNAME" ] || missing+=("FTP_USERNAME")
  [ -n "$FTP_PASSWORD" ] || missing+=("FTP_PASSWORD")
  if [ "${#missing[@]}" -gt 0 ]; then
    fail "Missing FTP configuration: ${missing[*]}."
  fi

  command -v curl >/dev/null 2>&1 || fail "FTP backend requires curl."

  FTP_BASE_URL="$(normalize_ftp_server "$FTP_SERVER")"
  FTP_BASE_DIR_NORMALIZED="$(normalize_ftp_base_dir "$FTP_BASE_DIR")"
  FTP_CURL_COMMON_ARGS=(
    --fail
    --silent
    --show-error
    --ssl-reqd
    --connect-timeout 30
    --retry 3
    --retry-delay 5
    --user "${FTP_USERNAME}:${FTP_PASSWORD}"
  )
  FTP_CURL_TRANSFER_ARGS=(
    --fail
    --show-error
    --progress-bar
    --ssl-reqd
    --connect-timeout 30
    --retry 3
    --retry-delay 5
    --user "${FTP_USERNAME}:${FTP_PASSWORD}"
  )
  FTP_READY=true
}

ftp_key_for_blob() {
  local blob_name="${1#/}"
  blob_name="${blob_name//\\//}"
  printf '%s' "$blob_name"
}

ftp_remote_url() {
  local key="$1"
  printf '%s%s/%s' "$FTP_BASE_URL" "$FTP_BASE_DIR_NORMALIZED" "$key"
}

ftp_object_exists() {
  local url="$1"
  local status=0

  curl "${FTP_CURL_COMMON_ARGS[@]}" --head --output /dev/null --dump-header /dev/null "$url" || status=$?
  if [ "$status" -eq 0 ]; then
    return 0
  fi
  if [ "$status" -eq 78 ] || [ "$status" -eq 19 ]; then
    return 1
  fi

  fail "FTP object check failed with curl exit code $status."
}

save_to_local_cache() {
  local local_path="$1"
  local blob_name="$2"

  if [ -n "${LOCAL_CACHE_DIR:-}" ] && [ -d "$LOCAL_CACHE_DIR" ]; then
    local cached="${LOCAL_CACHE_DIR}/${blob_name}"
    if [ -f "$cached" ]; then
      echo "  ✓ Already in local runner cache at $cached."
    else
      echo "  ↑ Saving to local runner cache: $cached"
      mkdir -p "$(dirname "$cached")"
      cp "$local_path" "$cached"
      echo "  ✓ Saved to local runner cache."
    fi
  fi
}

download_from_ftp() {
  local local_path="$1"
  local blob_name="$2"
  local key
  local url
  local tmp_path

  initialize_ftp
  key="$(ftp_key_for_blob "$blob_name")"
  url="$(ftp_remote_url "$key")"

  echo "  ↓ Not in local cache. Attempting explicit FTPS download from '$FTP_BASE_DIR_NORMALIZED/$key'..."

  if ftp_object_exists "$url"; then
    mkdir -p "$(dirname "$local_path")"
    tmp_path="${local_path}.part"
    rm -f "$tmp_path"
    if curl "${FTP_CURL_TRANSFER_ARGS[@]}" --location --output "$tmp_path" "$url"; then
      mv "$tmp_path" "$local_path"
      echo "  ✓ Downloaded $FTP_BASE_DIR_NORMALIZED/$key → $local_path"
      save_to_local_cache "$local_path" "$blob_name"
    else
      rm -f "$tmp_path"
      fail "Failed to download $FTP_BASE_DIR_NORMALIZED/$key from FTP."
    fi
  else
    echo "  ✗ FTP object $FTP_BASE_DIR_NORMALIZED/$key not found."
  fi
}

download_from_azure() {
  local local_path="$1"
  local blob_name="$2"
  local container="$3"
  local storage_account
  local blob_exists

  storage_account="$(derive_azure_storage_account)"

  echo "  ↓ Not in local cache. Attempting Azure Blob Storage download..."
  if az storage account show \
      --name "$storage_account" \
      --resource-group "$RESOURCE_GROUP" &>/dev/null; then
    blob_exists=$(az storage blob exists \
      --account-name "$storage_account" \
      --container-name "$container" \
      --name "$blob_name" \
      --auth-mode login \
      --query "exists" \
      --output tsv)
    if [ "$blob_exists" = "true" ]; then
      mkdir -p "$(dirname "$local_path")"
      az storage blob download \
        --account-name "$storage_account" \
        --container-name "$container" \
        --name "$blob_name" \
        --file "$local_path" \
        --auth-mode login
      echo "  ✓ Downloaded $blob_name → $local_path"
      save_to_local_cache "$local_path" "$blob_name"
    else
      echo "  ✗ Blob $blob_name not found in container '$container'."
    fi
  else
    echo "  ✗ Storage account $storage_account not found."
  fi
}

# Iterate over each entry in the JSON array using Python (always available on runners)
echo "$CACHE_FILES" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for f in data:
    print(f.get('local-path', ''))
    print(f.get('blob-name', ''))
    print(f.get('container', 'build-cache'))
" | while IFS= read -r local_path && IFS= read -r blob_name && IFS= read -r container; do
  [ -z "$local_path" ] && continue
  [ -z "$blob_name" ] && blob_name=$(basename "$local_path")
  [ -z "$container" ] && container="build-cache"

  echo "=== Build cache: $local_path [object=$blob_name container=$container backend=$CACHE_BACKEND] ==="

  # 1. Already present in workspace → nothing to do
  if [ -f "$local_path" ]; then
    echo "  ✓ Already present at $local_path — skipping."
    continue
  fi

  # 2. Local runner shared-volume cache (macOS Tart VirtioFS) → symlink
  if [ -n "$LOCAL_CACHE_DIR" ] && [ -f "${LOCAL_CACHE_DIR}/${blob_name}" ]; then
    echo "  ✓ Found in local runner cache — creating symlink."
    mkdir -p "$(dirname "$local_path")"
    ln -sf "${LOCAL_CACHE_DIR}/${blob_name}" "$local_path"
    continue
  fi

  case "$CACHE_BACKEND" in
    ftp|ftps)
      download_from_ftp "$local_path" "$blob_name"
      ;;
    azure|azure-blob)
      download_from_azure "$local_path" "$blob_name" "$container"
      ;;
    *)
      fail "Unsupported storage backend '$CACHE_BACKEND'. Use 'ftp' or 'azure'."
      ;;
  esac
done
