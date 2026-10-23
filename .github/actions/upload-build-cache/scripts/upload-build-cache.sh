#!/usr/bin/env bash
# Expects the following environment variables (set by the composite action):
#   CACHE_FILES       - JSON array of cache file descriptors
#   CACHE_BACKEND     - Remote cache backend: ftp or azure
#   SUBSCRIPTION_ID   - Azure subscription ID (azure backend only)
#   FTP_*             - FTP server, port, username, password, and base dir. Explicit TLS is enforced.
#   LOCAL_CACHE_DIR   - Path to the local runner shared-volume cache (may be empty)
set -euo pipefail

CACHE_BACKEND=$(printf '%s' "${CACHE_BACKEND:-ftp}" | tr '[:upper:]' '[:lower:]')
FTP_READY=false
FTP_BASE_URL=""
FTP_BASE_DIR_NORMALIZED=""
FTP_CURL_COMMON_ARGS=()

fail() {
  echo "  ✗ $*" >&2
  exit 1
}

# The remote cache is an optimization: report problems without failing the job.
warn_cache_skipped() {
  echo "::warning title=Build cache upload skipped::$*"
}

resolve_real_path() {
  local path="$1"

  if command -v realpath >/dev/null 2>&1; then
    realpath "$path"
  else
    python3 -c 'import os, sys; print(os.path.realpath(sys.argv[1]))' "$path"
  fi
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

  # Anything else (e.g. exit 28, which curl also reports for an FTP 421 reply)
  # means the server could not be queried.
  echo "  ✗ FTP object check failed with curl exit code $status." >&2
  return 2
}

upload_to_ftp() {
  local real_path="$1"
  local blob_name="$2"
  local overwrite="$3"
  local key
  local url

  initialize_ftp
  key="$(ftp_key_for_blob "$blob_name")"
  url="$(ftp_remote_url "$key")"

  if [ "$overwrite" != "true" ]; then
    local exists_status=0
    ftp_object_exists "$url" || exists_status=$?
    if [ "$exists_status" -eq 0 ]; then
      echo "  ✓ FTP object $FTP_BASE_DIR_NORMALIZED/$key already exists — skipping upload."
      return
    fi
    if [ "$exists_status" -eq 2 ]; then
      warn_cache_skipped "Could not check FTP object $FTP_BASE_DIR_NORMALIZED/$key; not uploading it."
      return
    fi
  fi

  if [ "$overwrite" = "true" ]; then
    echo "  ↻ Uploading $FTP_BASE_DIR_NORMALIZED/$key via explicit FTPS with overwrite enabled..."
  else
    echo "  ↑ Uploading $FTP_BASE_DIR_NORMALIZED/$key via explicit FTPS..."
  fi
  if ! curl "${FTP_CURL_COMMON_ARGS[@]}" --ftp-create-dirs --upload-file "$real_path" "$url"; then
    warn_cache_skipped "Failed to upload FTP object $FTP_BASE_DIR_NORMALIZED/$key."
    return
  fi
  echo "  ✓ Uploaded $FTP_BASE_DIR_NORMALIZED/$key."
}

upload_to_azure() {
  local real_path="$1"
  local blob_name="$2"
  local container="$3"
  local overwrite="$4"
  local storage_account
  local blob_exists

  storage_account="$(derive_azure_storage_account)"

  if [ "$overwrite" = "true" ]; then
    echo "  ↻ Uploading $blob_name to container '$container' with overwrite enabled..."
    az storage blob upload \
      --account-name "$storage_account" \
      --container-name "$container" \
      --name "$blob_name" \
      --file "$real_path" \
      --auth-mode login \
      --overwrite true
    echo "  ✓ Uploaded $blob_name."
  else
    blob_exists=$(az storage blob exists \
      --account-name "$storage_account" \
      --container-name "$container" \
      --name "$blob_name" \
      --auth-mode login \
      --query "exists" \
      --output tsv)

    if [ "$blob_exists" = "true" ]; then
      echo "  ✓ Blob $blob_name already exists in '$container' — skipping upload."
    else
      echo "  ↑ Uploading $blob_name to container '$container'..."
      az storage blob upload \
        --account-name "$storage_account" \
        --container-name "$container" \
        --name "$blob_name" \
        --file "$real_path" \
        --auth-mode login \
        --overwrite false
      echo "  ✓ Uploaded $blob_name."
    fi
  fi
}

echo "$CACHE_FILES" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for f in data:
    print(f.get('local-path', ''))
    print(f.get('blob-name', ''))
    print(f.get('container', 'build-cache'))
    print(str(f.get('overwrite', False)).lower())
    print(f.get('manifest', ''))
" | while IFS= read -r local_path && IFS= read -r blob_name && IFS= read -r container && IFS= read -r overwrite && IFS= read -r manifest; do
  [ -z "$local_path" ] && continue
  [ -z "$blob_name" ] && blob_name=$(basename "$local_path")
  [ -z "$container" ] && container="build-cache"
  [ -z "$overwrite" ] && overwrite=false

  echo "=== Build cache upload: $local_path [object=$blob_name container=$container overwrite=$overwrite backend=$CACHE_BACKEND] ==="

  if [ -n "$manifest" ]; then
    echo "  ✓ Entry is resolved via manifest '$manifest' — managed by refresh workflow, skipping upload."
    continue
  fi

  # Resolve symlinks so we operate on the real bytes
  real_path="$local_path"
  if [ -L "$local_path" ]; then
    real_path=$(resolve_real_path "$local_path")
  fi

  if [ ! -f "$real_path" ]; then
    echo "  ✗ File not found at $local_path — skipping."
    continue
  fi

  # 1. Save to local runner shared-volume cache (macOS Tart)
  if [ -n "$LOCAL_CACHE_DIR" ] && [ -d "$LOCAL_CACHE_DIR" ]; then
    cached="${LOCAL_CACHE_DIR}/${blob_name}"
    if [ -L "$local_path" ]; then
      echo "  ✓ File is a symlink to local cache — already cached."
    elif [ -f "$cached" ] && [ "$overwrite" != "true" ]; then
      echo "  ✓ Already in local runner cache at $cached."
    else
      mkdir -p "$(dirname "$cached")"
      if [ -f "$cached" ] && [ "$overwrite" = "true" ]; then
        echo "  ↻ Replacing local runner cache entry at $cached"
      else
        echo "  ↑ Saving to local runner cache: $cached"
      fi
      cp "$real_path" "$cached"
      echo "  ✓ Saved to local runner cache."
    fi
  fi

  case "$CACHE_BACKEND" in
    ftp|ftps)
      upload_to_ftp "$real_path" "$blob_name" "$overwrite"
      ;;
    azure|azure-blob)
      upload_to_azure "$real_path" "$blob_name" "$container" "$overwrite"
      ;;
    *)
      fail "Unsupported storage backend '$CACHE_BACKEND'. Use 'ftp' or 'azure'."
      ;;
  esac
done
