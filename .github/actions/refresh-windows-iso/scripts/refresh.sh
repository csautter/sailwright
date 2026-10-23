#!/usr/bin/env bash
# Expects the following environment variables (set by the composite action):
#   ARCH          - amd64 or arm64
#   MODE          - refresh, resolve-only or adopt
#   FORCE         - "true" to treat the refresh as due
#   DOWNLOAD_DIR  - directory for the ISO download
#   FTP_*         - FTP server, port, username, password, and base dir. Explicit TLS is enforced.
# Writes outcome and age_days to $GITHUB_OUTPUT. A Microsoft refusal is not an error.
set -euo pipefail

: "${ARCH:?ARCH is required}"
MODE="${MODE:-refresh}"
FORCE="${FORCE:-false}"
DOWNLOAD_DIR="${DOWNLOAD_DIR:?DOWNLOAD_DIR is required}"
GITHUB_OUTPUT="${GITHUB_OUTPUT:-/dev/null}"
GITHUB_STEP_SUMMARY="${GITHUB_STEP_SUMMARY:-/dev/null}"

ISO_DIR="windows11/iso"
MANIFEST_KEY="${ISO_DIR}/win11-${ARCH}.json"
WORK_DIR="${RUNNER_TEMP:-/tmp}/win11iso-${ARCH}"
MANIFEST="${WORK_DIR}/win11-${ARCH}.json"
FTP_BASE_URL=""
FTP_BASE_DIR_NORMALIZED=""
AGE_DAYS=9999

fail() {
  echo "::error title=Windows 11 ISO refresh ($ARCH)::$*" >&2
  exit 1
}

summary() {
  printf '%s\n' "$*" >> "$GITHUB_STEP_SUMMARY"
}

finish() {
  local outcome="$1"
  local detail="$2"
  echo "outcome=${outcome}" >> "$GITHUB_OUTPUT"
  echo "age_days=${AGE_DAYS}" >> "$GITHUB_OUTPUT"
  summary "- **${ARCH}**: ${outcome} — ${detail}"
  echo "Result for ${ARCH}: ${outcome} (${detail})"
}

# Returns 1 when FTP is not configured.
init_ftp() {
  local server="${FTP_SERVER:-}"
  server="${server#ftp://}"
  server="${server#ftps://}"
  server="${server%/}"
  if [ -z "$server" ] || [ -z "${FTP_USERNAME:-}" ] || [ -z "${FTP_PASSWORD:-}" ]; then
    return 1
  fi
  if [ -n "${FTP_PORT:-}" ] && [[ "$server" != *:* ]]; then
    server="${server}:${FTP_PORT}"
  fi
  local base_dir="${FTP_BASE_DIR:-/private}"
  base_dir="/${base_dir#/}"
  base_dir="${base_dir%/}"
  FTP_BASE_URL="ftp://${server}"
  FTP_BASE_DIR_NORMALIZED="${base_dir:-/private}"
}

ftp_url() {
  printf '%s%s/%s' "$FTP_BASE_URL" "$FTP_BASE_DIR_NORMALIZED" "$1"
}

ftp_curl() {
  curl --fail --silent --show-error --ssl-reqd --connect-timeout 30 \
    --retry 3 --retry-delay 5 --user "${FTP_USERNAME}:${FTP_PASSWORD}" "$@"
}

# Prints the remote size, or nothing when the object does not exist.
ftp_size() {
  local headers
  if ! headers="$(ftp_curl --head --dump-header - --output /dev/null "$(ftp_url "$1")" 2>/dev/null)"; then
    return 0
  fi
  printf '%s\n' "$headers" | tr -d '\r' | awk -F': ' 'tolower($1)=="content-length" {print $2}' | tail -n 1
}

ftp_get() {
  ftp_curl --output "$2" "$(ftp_url "$1")"
}

# Deletes an object below the ISO directory (argument: file name only).
ftp_delete() {
  ftp_curl --output /dev/null --quote "DELE ${FTP_BASE_DIR_NORMALIZED}/${ISO_DIR}/$1" "$(ftp_url "${ISO_DIR}/")"
}

# Uploads a file and verifies the remote size, deleting a partial ISO object on mismatch.
ftp_put() {
  local file="$1"
  local key="$2"
  local expected remote
  expected="$(wc -c < "$file" | tr -d ' ')"
  ftp_curl --progress-bar --ftp-create-dirs --upload-file "$file" "$(ftp_url "$key")"
  remote="$(ftp_size "$key")"
  if [ "$remote" != "$expected" ]; then
    case "$key" in
      *.iso) ftp_delete "$(basename "$key")" || true ;;
      *) ;;
    esac
    fail "Upload of $key failed verification (local $expected bytes, remote ${remote:-missing})."
  fi
}

output_value() {
  # Reads a key=value line produced by tools/win11iso from a file.
  sed -n "s/^$2=//p" "$1" | head -n 1
}

compute_age_days() {
  if [ ! -f "$MANIFEST" ]; then
    echo 9999
    return
  fi
  python3 - "$MANIFEST" <<'PY'
import json, sys
from datetime import datetime, timezone
try:
    checked = datetime.fromisoformat(json.load(open(sys.argv[1]))["checked_at"].replace("Z", "+00:00"))
    print(max(0, (datetime.now(timezone.utc) - checked).days))
except Exception:
    print(9999)
PY
}

prune_objects() {
  local object
  for object in $1; do
    echo "Pruning ${ISO_DIR}/${object}"
    ftp_delete "$object" || echo "::warning title=Windows 11 ISO prune::Could not delete ${ISO_DIR}/${object}"
  done
}

mkdir -p "$WORK_DIR" "$DOWNLOAD_DIR"
rm -f "${MANIFEST:?}"
tool=(go run ./tools/win11iso)

summary "### Windows 11 ISO refresh (${MODE})"

if init_ftp; then
  if [ -n "$(ftp_size "$MANIFEST_KEY")" ]; then
    ftp_get "$MANIFEST_KEY" "$MANIFEST"
    echo "Loaded manifest ${MANIFEST_KEY}."
  else
    echo "No manifest at ${MANIFEST_KEY}."
  fi
elif [ "$MODE" = "resolve-only" ]; then
  echo "::notice title=Windows 11 ISO refresh::FTP credentials unavailable; resolving without a manifest."
else
  fail "Missing FTP configuration."
fi
AGE_DAYS="$(compute_age_days)"

if [ "$MODE" = "adopt" ]; then
  legacy="win11_25h2_english_${ARCH}.iso"
  if [ "$ARCH" = "amd64" ]; then
    source_name="Win11_25H2_English_x64.iso"
  else
    source_name="Win11_25H2_English_Arm64.iso"
  fi
  if [ -f "$MANIFEST" ]; then
    finish adopted "manifest already exists, nothing to migrate"
    exit 0
  fi
  [ -n "$(ftp_size "$legacy")" ] || fail "Legacy object ${legacy} not found on the FTP server."
  ftp_get "$legacy" "${DOWNLOAD_DIR}/${legacy}"
  "${tool[@]}" publish --arch "$ARCH" --iso "${DOWNLOAD_DIR}/${legacy}" \
    --source-name "$source_name" --manifest "$MANIFEST" --epoch-checked-at | tee "${WORK_DIR}/publish.out"
  object="$(output_value "${WORK_DIR}/publish.out" object)"
  ftp_put "${DOWNLOAD_DIR}/${legacy}" "${ISO_DIR}/${object}"
  ftp_put "$MANIFEST" "$MANIFEST_KEY"
  rm -f "${DOWNLOAD_DIR:?}/${legacy:?}"
  finish adopted "legacy ISO published as ${object}; the legacy object is left in place"
  exit 0
fi

if [ "$MODE" != "refresh" ] && [ "$MODE" != "resolve-only" ]; then
  fail "Unknown mode '$MODE'."
fi

# 1. Offline due rule (only talks to the docs host).
if [ "$MODE" = "resolve-only" ]; then
  due=true
  reason="resolve-only mode always resolves"
else
  force_args=()
  if [ "$FORCE" = "true" ]; then
    force_args=(--force)
  fi
  "${tool[@]}" check --arch "$ARCH" --manifest "$MANIFEST" ${force_args[@]+"${force_args[@]}"} | tee "${WORK_DIR}/check.out"
  due="$(output_value "${WORK_DIR}/check.out" due)"
  reason="$(output_value "${WORK_DIR}/check.out" reason)"
fi
if [ "$due" != "true" ]; then
  finish skipped "$reason"
  exit 0
fi
echo "Refresh is due: $reason"

# 2. Resolve the link (no download).
"${tool[@]}" resolve --arch "$ARCH" --manifest "$MANIFEST" | tee "${WORK_DIR}/resolve.out"
outcome="$(output_value "${WORK_DIR}/resolve.out" outcome)"
case "$outcome" in
  refused)
    echo "::warning title=Microsoft refused the request ($ARCH)::Microsoft rejected the Windows 11 ISO request from this runner."
    finish refused "Microsoft refused the request from this runner"
    exit 0
    ;;
  unchanged)
    if [ "$MODE" = "refresh" ]; then
      ftp_put "$MANIFEST" "$MANIFEST_KEY"
      finish unchanged "ISO unchanged, checked_at updated"
    else
      finish unchanged "ISO unchanged (resolve-only, nothing uploaded)"
    fi
    exit 0
    ;;
  new) ;;
  *) fail "Unexpected resolve outcome '${outcome}'." ;;
esac

url="$(output_value "${WORK_DIR}/resolve.out" url)"
source_name="$(output_value "${WORK_DIR}/resolve.out" source_name)"
if [ "$MODE" = "resolve-only" ]; then
  finish new "link resolved for ${source_name} (resolve-only, nothing downloaded)"
  exit 0
fi

# 3. Download, then publish the immutable object before the manifest.
sha="$(output_value "${WORK_DIR}/resolve.out" sha256)"
fetch_args=(--arch "$ARCH" --url "$url" --out "$DOWNLOAD_DIR" --manifest "$MANIFEST" --source-name "$source_name")
if [ -n "$sha" ]; then
  fetch_args+=(--sha256 "$sha")
fi
"${tool[@]}" fetch "${fetch_args[@]}" | tee "${WORK_DIR}/fetch.out"
object="$(output_value "${WORK_DIR}/fetch.out" object)"
local_path="$(output_value "${WORK_DIR}/fetch.out" local_path)"
pruned="$(output_value "${WORK_DIR}/fetch.out" pruned)"

if [ -z "$(ftp_size "${ISO_DIR}/${object}")" ]; then
  ftp_put "$local_path" "${ISO_DIR}/${object}"
else
  echo "Object ${ISO_DIR}/${object} already exists; not uploading again."
fi
ftp_put "$MANIFEST" "$MANIFEST_KEY"
rm -f "${local_path:?}"
prune_objects "$pruned"
finish new "published ${object}"
