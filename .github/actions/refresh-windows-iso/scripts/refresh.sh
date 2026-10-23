#!/usr/bin/env bash
# Expects the following environment variables (set by the composite action):
#   ARCH          - amd64 or arm64
#   MODE          - refresh, resolve-only or adopt
#   FORCE         - "true" to treat the refresh as due
#   DOWNLOAD_DIR  - directory for the ISO download
#   FTP_*         - FTP server, port, username, password, and base dir. Explicit TLS is enforced.
#   WIN11ISO_TOOL - optional command replacing "go run ./tools/win11iso" (tests)
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

# Prints the remote size, or nothing when the object does not exist. Any other failure
# (authentication, TLS, network) fails the run instead of looking like a missing object.
# Assign the result to a variable: inside "[ -n "$(ftp_size …)" ]" a failure would be ignored.
ftp_size() {
  local headers
  local status=0
  headers="$(ftp_curl --head --dump-header - --output /dev/null "$(ftp_url "$1")" 2>"${WORK_DIR}/curl.err")" || status=$?
  case "$status" in
    0) ;;
    19|78) return 0 ;;
    *)
      cat "${WORK_DIR}/curl.err" >&2
      fail "FTP size check of $1 failed with curl exit code $status."
      ;;
  esac
  printf '%s\n' "$headers" | tr -d '\r' | awk -F': ' 'tolower($1)=="content-length" {print $2}' | tail -n 1
}

ftp_get() {
  ftp_curl --output "$2" "$(ftp_url "$1")"
}

# Deletes a remote object (argument: key below the base dir).
ftp_delete() {
  ftp_curl --output /dev/null --quote "DELE ${FTP_BASE_DIR_NORMALIZED}/$1" "$(ftp_url "${ISO_DIR}/")"
}

# Renames a remote object (arguments: source and target key below the base dir).
ftp_rename() {
  ftp_curl --output /dev/null \
    --quote "RNFR ${FTP_BASE_DIR_NORMALIZED}/$1" --quote "RNTO ${FTP_BASE_DIR_NORMALIZED}/$2" \
    "$(ftp_url "${ISO_DIR}/")"
}

# Both supported architectures share the ISO directory. Read the remote manifests, including
# their histories, before replacing a leftover; the local manifest may already be promoted.
assert_object_unreferenced() {
  local object="$1" arch key size snapshot
  for arch in amd64 arm64; do
    key="${ISO_DIR}/win11-${arch}.json"
    size="$(ftp_size "$key")"
    [ -n "$size" ] || continue
    snapshot="${WORK_DIR}/remote-${arch}.json"
    ftp_get "$key" "$snapshot"
    if ! python3 - "$snapshot" "$object" <<'PYREF'
import json, sys
m = json.load(open(sys.argv[1]))
objects = [m["object"]] + [entry["object"] for entry in m.get("history", [])]
sys.exit(1 if sys.argv[2] in objects else 0)
PYREF
    then
      fail "Refusing to replace object ${object}: referenced by remote manifest ${key}, or the manifest could not be read."
    fi
  done
}

# Uploads a file under a temporary name, verifies the remote size and renames it to its key, so
# readers only ever see a complete ISO or manifest. An interrupted upload leaves at most a
# temporary object behind, never a truncated file under the final name.
ftp_publish() {
  local file="$1"
  local key="$2"
  local tmp_key="${key}.upload-${GITHUB_RUN_ID:-local}-$$"
  local expected remote
  if [[ "$key" == "${ISO_DIR}/"*.iso ]]; then
    remote="$(ftp_size "$key")"
    if [ -n "$remote" ]; then
      assert_object_unreferenced "${key##*/}"
      echo "Replacing unreferenced object ${key} left over from an interrupted run."
    fi
  fi
  expected="$(wc -c < "$file" | tr -d ' ')"
  if ! ftp_curl --progress-bar --ftp-create-dirs --upload-file "$file" "$(ftp_url "$tmp_key")"; then
    ftp_delete "$tmp_key" 2>/dev/null || true
    fail "Upload of $key failed."
  fi
  remote="$(ftp_size "$tmp_key")"
  if [ "$remote" != "$expected" ]; then
    ftp_delete "$tmp_key" || true
    fail "Upload of $key failed verification (local $expected bytes, remote ${remote:-missing})."
  fi
  if ftp_rename "$tmp_key" "$key"; then
    return 0
  fi
  # Some servers refuse to rename onto an existing file. Fall back to delete + rename; the target
  # is missing only for the duration of two FTP commands.
  echo "::warning title=Windows 11 ISO refresh ($ARCH)::The FTP server refused to replace $key by rename; replacing it non-atomically."
  if ftp_delete "$key" && ftp_rename "$tmp_key" "$key"; then
    return 0
  fi
  ftp_delete "$tmp_key" || true
  fail "Could not move the uploaded $tmp_key to $key."
}

# Check immutable referenced objects before accepting an unchanged result. Missing objects can be
# restored from verified bytes; a conflicting size indicates corruption and must fail the refresh.
referenced_object_available() {
  local object="$1" expected="$2" remote
  remote="$(ftp_size "${ISO_DIR}/${object}")" || exit $?
  if [ -z "$remote" ]; then
    summary "- **${ARCH}**: referenced object ${object} is missing; restoring verified bytes"
    return 1
  fi
  [ "$remote" = "$expected" ] || fail "Referenced object ${object} has unexpected size: expected $expected, got $remote."
  summary "- **${ARCH}**: referenced object ${object} available (${remote} bytes)"
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
    ftp_delete "${ISO_DIR}/${object}" || echo "::warning title=Windows 11 ISO prune::Could not delete ${ISO_DIR}/${object}"
  done
}

mkdir -p "$WORK_DIR" "$DOWNLOAD_DIR"
rm -f "${MANIFEST:?}"
read -r -a tool <<< "${WIN11ISO_TOOL:-go run ./tools/win11iso}"

summary "### Windows 11 ISO refresh (${MODE})"

if init_ftp; then
  manifest_size="$(ftp_size "$MANIFEST_KEY")"
  if [ -n "$manifest_size" ]; then
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
  legacy_size="$(ftp_size "$legacy")"
  [ -n "$legacy_size" ] || fail "Legacy object ${legacy} not found on the FTP server."
  ftp_get "$legacy" "${DOWNLOAD_DIR}/${legacy}"
  "${tool[@]}" publish --arch "$ARCH" --iso "${DOWNLOAD_DIR}/${legacy}" \
    --source-name "$source_name" --manifest "$MANIFEST" --mark-due | tee "${WORK_DIR}/publish.out"
  object="$(output_value "${WORK_DIR}/publish.out" object)"
  ftp_publish "${DOWNLOAD_DIR}/${legacy}" "${ISO_DIR}/${object}"
  ftp_publish "$MANIFEST" "$MANIFEST_KEY"
  rm -f "${DOWNLOAD_DIR:?}/${legacy:?}"
  finish adopted "legacy ISO published as ${object}; the legacy object is left in place"
  exit 0
fi

if [ "$MODE" != "refresh" ] && [ "$MODE" != "resolve-only" ]; then
  fail "Unknown mode '$MODE'."
fi

# 1. Offline due rule (only talks to the docs host).
latest_release=""
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
  latest_release="$(output_value "${WORK_DIR}/check.out" latest_release)"
fi
announced_args=()
if [ -n "$latest_release" ]; then
  announced_args=(--announced-release "$latest_release")
fi
if [ "$due" != "true" ]; then
  finish skipped "$reason"
  exit 0
fi
echo "Refresh is due: $reason"

# 2. Resolve the link (no download).
"${tool[@]}" resolve --arch "$ARCH" --manifest "$MANIFEST" ${announced_args[@]+"${announced_args[@]}"} | tee "${WORK_DIR}/resolve.out"
outcome="$(output_value "${WORK_DIR}/resolve.out" outcome)"
case "$outcome" in
  refused)
    echo "::warning title=Microsoft refused the request ($ARCH)::Microsoft rejected the Windows 11 ISO request from this runner."
    finish refused "Microsoft refused the request from this runner"
    exit 0
    ;;
  unchanged)
    if [ "$MODE" = "resolve-only" ]; then
      finish unchanged "ISO unchanged (resolve-only, nothing uploaded)"
      exit 0
    fi
    object="$(output_value "${WORK_DIR}/resolve.out" object)"
    expected_size="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["size"])' "$MANIFEST")"
    if referenced_object_available "$object" "$expected_size"; then
      ftp_publish "$MANIFEST" "$MANIFEST_KEY"
      finish unchanged "ISO unchanged (${object}), remote size verified, checked_at updated"
      exit 0
    fi
    # Resolve emits the current digest on unchanged results so repair downloads must match it,
    # including when unchanged was inferred from the source name and size.
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
  sha_source="verified against Microsoft's published sha256"
else
  echo "::warning title=Windows 11 ISO refresh ($ARCH)::Microsoft's sha256 for ${source_name} was not found; the ISO is published without an end-to-end checksum."
  sha_source="no Microsoft sha256 available"
fi
"${tool[@]}" fetch "${fetch_args[@]}" ${announced_args[@]+"${announced_args[@]}"} | tee "${WORK_DIR}/fetch.out"
object="$(output_value "${WORK_DIR}/fetch.out" object)"
local_path="$(output_value "${WORK_DIR}/fetch.out" local_path)"
pruned="$(output_value "${WORK_DIR}/fetch.out" pruned)"
existing="$(output_value "${WORK_DIR}/fetch.out" existing)"
unchanged="$(output_value "${WORK_DIR}/fetch.out" unchanged)"

if [ "$unchanged" = "true" ]; then
  expected_size="$(wc -c < "$local_path" | tr -d ' ')"
  if ! referenced_object_available "$object" "$expected_size"; then
    ftp_publish "$local_path" "${ISO_DIR}/${object}"
  fi
  ftp_publish "$MANIFEST" "$MANIFEST_KEY"
  rm -f "${local_path:?}"
  finish unchanged "downloaded ISO is identical to ${object}, remote object verified/restored, checked_at updated"
  exit 0
fi

# Objects are immutable once a manifest references them. "existing" means the manifest history
# already lists this object with the same sha256. Before replacing any leftover, ftp_publish
# checks both remote manifests to ensure neither architecture references it.
remote_size="$(ftp_size "${ISO_DIR}/${object}")"
if [ "$existing" = "true" ] && [ -n "$remote_size" ]; then
  expected_size="$(wc -c < "$local_path" | tr -d ' ')"
  referenced_object_available "$object" "$expected_size"
  echo "Object ${ISO_DIR}/${object} is already published with the same sha256; not uploading again."
else
  ftp_publish "$local_path" "${ISO_DIR}/${object}"
fi
ftp_publish "$MANIFEST" "$MANIFEST_KEY"
rm -f "${local_path:?}"
prune_objects "$pruned"
finish new "published ${object} (${sha_source})"
