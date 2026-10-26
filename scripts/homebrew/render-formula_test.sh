#!/usr/bin/env bash
# Tests for render-formula.sh. Usage: render-formula_test.sh

set -euo pipefail

script="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/render-formula.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

sha() { printf '%064d' "$1"; }
failures=0

fail() {
  echo "FAIL: $1" >&2
  failures=$((failures + 1))
}

write_checksums() {
  local file="$1" prefix="$2" mark="${3:- }" n=1 name os arch
  : > "$file"
  for name in sailwright sailwright-gateway; do
    for os in darwin linux; do
      for arch in arm64 amd64; do
        echo "$(sha "$n") ${mark}${prefix}${name}_1.2.3_${os}_${arch}.tar.gz" >> "$file"
        n=$((n + 1))
      done
    done
  done
}

write_checksums "$tmp/plain.txt" "dist/"
write_checksums "$tmp/binary.txt" "" "*"

for sums in plain binary; do
  out="$("$script" sailwright v1.2.3 "$tmp/$sums.txt")" || { fail "$sums: render failed"; continue; }
  grep -q '^class Sailwright < Formula' <<<"$out" || fail "$sums: class name"
  grep -q 'releases/download/v1.2.3/sailwright_1.2.3_darwin_arm64.tar.gz' <<<"$out" || fail "$sums: url"
  grep -q "sha256 \"$(sha 1)\"" <<<"$out" || fail "$sums: darwin arm64 sha"
  grep -q '^  version ' <<<"$out" && fail "$sums: redundant version line"
done

out="$("$script" sailwright-gateway 1.2.3 "$tmp/plain.txt")"
grep -q '^class SailwrightGateway < Formula' <<<"$out" || fail "gateway class name"
grep -q "sha256 \"$(sha 5)\"" <<<"$out" || fail "gateway darwin arm64 sha"

grep -v 'linux_amd64' "$tmp/plain.txt" > "$tmp/missing.txt"
"$script" sailwright 1.2.3 "$tmp/missing.txt" >/dev/null 2>&1 && fail "missing asset should fail"
"$script" sailwright not-a-version "$tmp/plain.txt" >/dev/null 2>&1 && fail "bad version should fail"
"$script" sailwright 1.2.3 "$tmp/nope.txt" >/dev/null 2>&1 && fail "missing file should fail"
"$script" other 1.2.3 "$tmp/plain.txt" >/dev/null 2>&1 && fail "unknown formula should fail"

if [[ $failures -ne 0 ]]; then
  echo "$failures failure(s)" >&2
  exit 1
fi
echo "render-formula tests passed"
