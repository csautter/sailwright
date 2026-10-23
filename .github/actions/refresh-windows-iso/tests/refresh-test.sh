#!/usr/bin/env bash
# Deterministic tests for scripts/refresh.sh: a fake FTPS server (curl) backed by a directory and a
# fake win11iso helper whose "fetch" copies a local file and hands it to the real "publish".
# No network, no Microsoft. Run from the repository root:
#   bash .github/actions/refresh-windows-iso/tests/refresh-test.sh
set -euo pipefail

repo_root="$(pwd)"
refresh_script="${repo_root}/.github/actions/refresh-windows-iso/scripts/refresh.sh"
root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT

remote="$root/remote"
fake_bin="$root/bin"
mkdir -p "$remote/windows11/iso" "$fake_bin"

go build -o "$fake_bin/win11iso-real" ./tools/win11iso

# Fake curl: ftp://localhost/cache/<key> maps to $remote/<key>; quote paths are /cache/<key>.
cat > "$fake_bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FAKE_FTP_LOG"
out=""
upload=""
head=false
quotes=()
prev=""
for arg in "$@"; do
  case "$prev" in
    --output) out="$arg" ;;
    --upload-file) upload="$arg" ;;
    --quote) quotes+=("$arg") ;;
  esac
  [ "$arg" = "--head" ] && head=true
  prev="$arg"
done
url="${*: -1}"
key="${url#ftp://localhost/cache/}"
path="$FAKE_FTP_REMOTE/$key"

if [ "${#quotes[@]}" -gt 0 ]; then
  rnfr=""
  for q in "${quotes[@]}"; do
    target="$FAKE_FTP_REMOTE/${q#* /cache/}"
    case "$q" in
      "DELE "*) [ -f "$target" ] || exit 21; rm -f "$target" ;;
      "RNFR "*) [ -f "$target" ] || exit 21; rnfr="$target" ;;
      "RNTO "*)
        if [ -e "$target" ] && [ -n "${FAKE_FTP_NO_RENAME_OVERWRITE:-}" ]; then exit 21; fi
        mv -f "$rnfr" "$target"
        ;;
    esac
  done
  exit 0
fi
if [ -n "$upload" ]; then
  mkdir -p "$(dirname "$path")"
  # shellcheck disable=SC2053 # glob match on purpose
  if [ -n "${FAKE_FTP_FAIL_UPLOAD:-}" ] && [[ "$key" == $FAKE_FTP_FAIL_UPLOAD ]]; then
    head -c 3 "$upload" > "$path"
    echo "curl: (55) connection reset" >&2
    exit 55
  fi
  cp "$upload" "$path"
  exit 0
fi
if [ "$head" = "true" ]; then
  if [ -n "${FAKE_FTP_HEAD_EXIT:-}" ] && { [ -z "${FAKE_FTP_HEAD_KEY:-}" ] || [ "$key" = "$FAKE_FTP_HEAD_KEY" ]; }; then
    echo "curl: (${FAKE_FTP_HEAD_EXIT}) fake failure" >&2
    exit "$FAKE_FTP_HEAD_EXIT"
  fi
  [ -f "$path" ] || { echo "curl: (78) not found" >&2; exit 78; }
  printf 'Content-Length: %s\r\n' "$(wc -c < "$path" | tr -d ' ')"
  exit 0
fi
[ -f "$path" ] || exit 78
cp "$path" "$out"
EOF

# Fake win11iso: check/resolve are scripted via FAKE_* variables, fetch "downloads" $FAKE_ISO.
cat > "$fake_bin/win11iso" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
cmd="$1"
shift
case "$cmd" in
  check)
    printf 'due=true\nreason=fake\nlatest_release=%s\n' "${FAKE_LATEST:-}"
    ;;
  resolve)
    if [ "${FAKE_OUTCOME:-new}" = unchanged ]; then
      while [ "$#" -gt 0 ]; do
        if [ "$1" = --manifest ]; then manifest="$2"; break; fi
        shift
      done
      python3 - "$manifest" <<'PYRESOLVE'
import json, sys
m = json.load(open(sys.argv[1]))
print("object=" + m["object"])
print("sha256=" + m["sha256"])
PYRESOLVE
    fi
    printf 'outcome=%s\nurl=https://example.invalid/%s\nsource_name=%s\nsha256=\n' \
      "${FAKE_OUTCOME:-new}" "${FAKE_SOURCE:-Win11_25H2_English_x64.iso}" "${FAKE_SOURCE:-Win11_25H2_English_x64.iso}"
    ;;
  fetch)
    args=("$@")
    out="" manifest="" source="" arch=""
    for ((i = 0; i < ${#args[@]}; i++)); do
      case "${args[i]}" in
        --out) out="${args[i + 1]}" ;;
        --manifest) manifest="${args[i + 1]}" ;;
        --source-name) source="${args[i + 1]}" ;;
        --arch) arch="${args[i + 1]}" ;;
      esac
    done
    cp "$FAKE_ISO" "$out/download.iso"
    exec "$(dirname "$0")/win11iso-real" publish --arch "$arch" --iso "$out/download.iso" \
      --source-name "$source" --manifest "$manifest"
    ;;
  publish)
    exec "$(dirname "$0")/win11iso-real" publish "$@"
    ;;
esac
EOF
chmod +x "$fake_bin/curl" "$fake_bin/win11iso" "$fake_bin/win11iso-real"

export PATH="$fake_bin:$PATH"
export FAKE_FTP_REMOTE="$remote"
export FAKE_FTP_LOG="$root/ftp.log"
export WIN11ISO_TOOL="$fake_bin/win11iso"
export ARCH=amd64 FTP_SERVER=localhost FTP_USERNAME=user FTP_PASSWORD=password FTP_BASE_DIR=/cache
export DOWNLOAD_DIR="$root/download" RUNNER_TEMP="$root/runner-temp"
export GITHUB_OUTPUT="$root/github-output"
mkdir -p "$DOWNLOAD_DIR" "$RUNNER_TEMP"

manifest="$remote/windows11/iso/win11-amd64.json"
today="$(date -u +%Y%m%d)"
object="win11_25h2_english_x64_${today}.iso"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

run_refresh() {
  : > "$GITHUB_OUTPUT"
  : > "$FAKE_FTP_LOG"
  MODE="${MODE:-refresh}" bash "$refresh_script" > "$root/refresh.out" 2>&1
}

outcome() {
  sed -n 's/^outcome=//p' "$GITHUB_OUTPUT" | tail -n 1
}

manifest_field() {
  python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$manifest" "$1"
}

sha_of() {
  sha256sum "$1" | cut -d' ' -f1
}

assert_no_upload_leftovers() {
  if find "$remote" -name '*.upload-*' | grep . >/dev/null; then
    fail "temporary upload objects left behind: $(find "$remote" -name '*.upload-*')"
  fi
}

reset_remote() {
  rm -rf "$remote"
  mkdir -p "$remote/windows11/iso"
}

echo "--- interrupted ISO upload never appears under the final name"
printf 'iso-v1-bytes' > "$root/v1.iso"
export FAKE_ISO="$root/v1.iso"
if FAKE_FTP_FAIL_UPLOAD="windows11/iso/*.iso.upload-*" run_refresh; then
  fail "an interrupted upload must fail the run"
fi
[ ! -e "$remote/windows11/iso/$object" ] || fail "partial object published under its final name"
[ ! -e "$manifest" ] || fail "manifest must not be published after a failed upload"
assert_no_upload_leftovers

echo "--- first publish: object, then manifest, both renamed into place"
run_refresh || { cat "$root/refresh.out"; fail "first publish"; }
[ "$(outcome)" = "new" ] || fail "outcome $(outcome)"
[ "$(manifest_field object)" = "$object" ] || fail "manifest object $(manifest_field object)"
[ "$(manifest_field sha256)" = "$(sha_of "$remote/windows11/iso/$object")" ] || fail "manifest sha256 does not match the stored object"
object_rename="$(grep -n "RNTO /cache/windows11/iso/${object}" "$FAKE_FTP_LOG" | cut -d: -f1)"
manifest_rename="$(grep -n "RNTO /cache/windows11/iso/win11-amd64.json" "$FAKE_FTP_LOG" | cut -d: -f1)"
if [ -z "$object_rename" ] || [ -z "$manifest_rename" ] || [ "$object_rename" -gt "$manifest_rename" ]; then
  fail "the object must be in place before the manifest"
fi
assert_no_upload_leftovers
[ -z "$(ls -A "$DOWNLOAD_DIR")" ] || fail "download not cleaned up"

echo "--- same day, different bytes under the same name: refused, nothing changes"
cp "$manifest" "$root/manifest.before"
cp "$remote/windows11/iso/$object" "$root/object.before"
printf 'iso-v1-changed' > "$root/v1-changed.iso"
if FAKE_ISO="$root/v1-changed.iso" run_refresh; then
  fail "changed bytes under a published name must fail"
fi
grep -F "already published" "$root/refresh.out" >/dev/null || fail "missing conflict message"
cmp -s "$manifest" "$root/manifest.before" || fail "manifest changed on conflict"
cmp -s "$remote/windows11/iso/$object" "$root/object.before" || fail "published object changed on conflict"

echo "--- identical bytes: unchanged, only the manifest is rewritten"
run_refresh || { cat "$root/refresh.out"; fail "unchanged run"; }
[ "$(outcome)" = "unchanged" ] || fail "outcome $(outcome)"
if grep -F -- "--upload-file" "$FAKE_FTP_LOG" | grep -F ".iso.upload-" >/dev/null; then
  fail "an identical ISO must not be uploaded again"
fi
assert_no_upload_leftovers

echo "--- resolve reports unchanged: manifest re-published, no download"
FAKE_OUTCOME=unchanged run_refresh || { cat "$root/refresh.out"; fail "resolve unchanged"; }
[ "$(outcome)" = "unchanged" ] || fail "outcome $(outcome)"
grep -F "RNTO /cache/windows11/iso/win11-amd64.json" "$FAKE_FTP_LOG" >/dev/null || fail "manifest not re-published"

for resolve_outcome in new unchanged; do
  echo "--- missing object is repaired on $resolve_outcome resolution"
  rm -f "$remote/windows11/iso/$object"
  FAKE_OUTCOME="$resolve_outcome" run_refresh || { cat "$root/refresh.out"; fail "repair $resolve_outcome"; }
  [ "$(outcome)" = "unchanged" ] || fail "repair outcome $(outcome)"
  cmp -s "$remote/windows11/iso/$object" "$root/v1.iso" || fail "missing object not restored"
  assert_no_upload_leftovers

  echo "--- inconsistent remote size fails on $resolve_outcome resolution"
  cp "$manifest" "$root/manifest.before"
  printf 'bad' > "$remote/windows11/iso/$object"
  if FAKE_OUTCOME="$resolve_outcome" run_refresh; then
    fail "inconsistent remote size must fail"
  fi
  grep -F "unexpected size" "$root/refresh.out" >/dev/null || fail "missing size error"
  cmp -s "$manifest" "$root/manifest.before" || fail "manifest published despite invalid object"
  cp "$root/v1.iso" "$remote/windows11/iso/$object"
done

# Errors during conditional availability checks must not be treated as missing objects.
if FAKE_OUTCOME=unchanged FAKE_FTP_HEAD_EXIT=67 FAKE_FTP_HEAD_KEY="windows11/iso/$object" run_refresh; then
  fail "authentication failure must fail unchanged resolution"
fi

echo "--- refused: no FTP writes"
FAKE_OUTCOME=refused run_refresh || { cat "$root/refresh.out"; fail "refused run"; }
[ "$(outcome)" = "refused" ] || fail "outcome $(outcome)"
if grep -E -- "--upload-file|--quote" "$FAKE_FTP_LOG" >/dev/null; then
  fail "a refused run must not write to the FTP server"
fi

echo "--- leftover object from an interrupted run is replaced, retention prunes the oldest object"
reset_remote
cat > "$manifest" <<EOF
{
  "schema": 1,
  "arch": "amd64",
  "object": "win11_25h2_english_x64_20250301.iso",
  "source_name": "Win11_25H2_English_x64.iso",
  "release": "25H2",
  "sha256": "$(printf 'c' | sha256sum | cut -d' ' -f1)",
  "size": 1,
  "checked_at": "2025-03-01T00:00:00Z",
  "history": [
    {"object": "win11_25h2_english_x64_20250301.iso", "sha256": "$(printf 'c' | sha256sum | cut -d' ' -f1)"},
    {"object": "win11_25h2_english_x64_20250201.iso", "sha256": "$(printf 'b' | sha256sum | cut -d' ' -f1)"},
    {"object": "win11_25h2_english_x64_20250101.iso", "sha256": "$(printf 'a' | sha256sum | cut -d' ' -f1)"}
  ]
}
EOF
for day in 20250101 20250201 20250301; do
  printf 'x' > "$remote/windows11/iso/win11_25h2_english_x64_${day}.iso"
done
printf 'par' > "$remote/windows11/iso/$object"
# A file is not a leftover when the other architecture still references it.
for reference in current history; do
  echo "--- refuse replacement of object in another architecture's $reference"
  python3 - "$remote/windows11/iso/win11-arm64.json" "$object" "$reference" <<'PYREF'
import json, sys
obj = sys.argv[2]
m = {"schema": 1, "arch": "arm64", "object": obj, "sha256": "a" * 64, "history": []}
if sys.argv[3] == "history":
    m["object"] = "other.iso"
    m["history"] = [{"object": obj, "sha256": "a" * 64}]
with open(sys.argv[1], "w") as f:
    json.dump(m, f)
PYREF
  cp "$manifest" "$root/manifest.before"
  cp "$remote/windows11/iso/$object" "$root/object.before"
  if run_refresh; then fail "must not overwrite an object referenced by another architecture"; fi
  grep -F "Refusing to replace object" "$root/refresh.out" >/dev/null || fail "missing cross-manifest conflict message"
  cmp -s "$manifest" "$root/manifest.before" || fail "manifest changed on cross-manifest conflict"
  cmp -s "$remote/windows11/iso/$object" "$root/object.before" || fail "referenced object overwritten"
  if grep -F -- "--upload-file" "$FAKE_FTP_LOG" >/dev/null; then fail "conflict must prevent uploads"; fi
done
rm "$remote/windows11/iso/win11-arm64.json"
run_refresh || { cat "$root/refresh.out"; fail "publish over leftover"; }
grep -F "Replacing unreferenced object" "$root/refresh.out" >/dev/null || fail "leftover not reported"
cmp -s "$remote/windows11/iso/$object" "$root/v1.iso" || fail "leftover object not replaced"
[ ! -e "$remote/windows11/iso/win11_25h2_english_x64_20250101.iso" ] || fail "oldest object not pruned"
[ -e "$remote/windows11/iso/win11_25h2_english_x64_20250201.iso" ] || fail "retained object pruned"

echo "--- server refuses rename onto an existing file: delete + rename fallback"
printf 'iso-v2-bytes' > "$root/v2.iso"
reset_remote
FAKE_ISO="$root/v1.iso" run_refresh || fail "seed publish"
cp "$manifest" "$root/seed-manifest.json"
reset_remote
cp "$root/seed-manifest.json" "$manifest"
cp "$root/v1.iso" "$remote/windows11/iso/$object"
FAKE_FTP_NO_RENAME_OVERWRITE=1 FAKE_SOURCE=Win11_25H2_English_x64v2.iso FAKE_ISO="$root/v2.iso" run_refresh ||
  { cat "$root/refresh.out"; fail "publish with rename fallback"; }
grep -F "replacing it non-atomically" "$root/refresh.out" >/dev/null || fail "missing rename fallback warning"
[ "$(manifest_field object)" = "win11_25h2_english_x64v2_${today}.iso" ] || fail "manifest object $(manifest_field object)"
assert_no_upload_leftovers

echo "--- FTP errors other than 'not found' fail instead of looking like an empty cache"
if FAKE_FTP_HEAD_EXIT=67 run_refresh; then
  fail "an authentication failure must fail the run"
fi
grep -F "failed with curl exit code 67" "$root/refresh.out" >/dev/null || fail "missing FTP error message"

echo "--- adopt: legacy flat object becomes a versioned object, due on the next run"
reset_remote
cp "$root/v1.iso" "$remote/win11_25h2_english_amd64.iso"
MODE=adopt run_refresh || { cat "$root/refresh.out"; fail "adopt"; }
[ "$(outcome)" = "adopted" ] || fail "outcome $(outcome)"
cmp -s "$remote/windows11/iso/$object" "$root/v1.iso" || fail "adopted object missing"
checked_at="$(manifest_field checked_at)"
expected_checked="$(python3 -c 'from datetime import datetime, timedelta, timezone; print((datetime.now(timezone.utc) - timedelta(days=28)).strftime("%Y-%m-%d"))')"
[[ "$checked_at" == "$expected_checked"* ]] || fail "adopt checked_at $checked_at, want $expected_checked"
[ -e "$remote/win11_25h2_english_amd64.iso" ] || fail "legacy object must be left in place"

echo "--- unexpected source names produce distinct objects for each requested architecture"
reset_remote
FAKE_SOURCE=Win11_26H1_English_x64_v2.iso run_refresh || { cat "$root/refresh.out"; fail "amd64 fallback"; }
ARCH=arm64 FAKE_SOURCE=Win11_26H1_English_ARM.iso FAKE_ISO="$root/v2.iso" run_refresh || { cat "$root/refresh.out"; fail "arm64 fallback"; }
cmp -s "$remote/windows11/iso/win11_unknown_english_amd64_${today}.iso" "$root/v1.iso" || fail "amd64 fallback changed"
cmp -s "$remote/windows11/iso/win11_unknown_english_arm64_${today}.iso" "$root/v2.iso" || fail "arm64 fallback missing"

echo "All refresh.sh tests passed."
