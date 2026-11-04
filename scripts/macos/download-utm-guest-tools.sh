#!/bin/bash

# This script downloads the pinned UTM Guest Tools ISO for QEMU Windows guests
# and saves it to the cache/utm directory.
set -e

# Keep in sync with pkg/build/dependencies.go using scripts/update-utm-guest-tools-pin.sh.
UTM_GUEST_TOOLS_VERSION="0.1.271"
UTM_GUEST_TOOLS_RELEASE="v10.0.2-utm"
UTM_GUEST_TOOLS_SHA256="65b6a69b392ee01dd314c10f3dad9ebbf9c4160be43f5f0dd6bb715944d9095b"
iso_file_name="utm-guest-tools-${UTM_GUEST_TOOLS_VERSION}.iso"
if [ "${1:-}" = "--print-file-name" ]; then
	echo "$iso_file_name"
	exit 0
fi

host_os="$(uname -s)"
if [ "$host_os" = "Darwin" ]; then
	DEFAULT_APP_DATA_DIR="$HOME/Library/Application Support/dev-alchemy"
else
	DEFAULT_APP_DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/dev-alchemy"
fi

APP_DATA_DIR="${DEV_ALCHEMY_APP_DATA_DIR:-$DEFAULT_APP_DATA_DIR}"
CACHE_DIR="${DEV_ALCHEMY_CACHE_DIR:-$APP_DATA_DIR/cache}"
export DEV_ALCHEMY_APP_DATA_DIR="$APP_DATA_DIR"
export DEV_ALCHEMY_CACHE_DIR="$CACHE_DIR"
OUTPUT_DIR="$CACHE_DIR/utm"
OUTPUT_PATH="$OUTPUT_DIR/$iso_file_name"
legacy_iso_path="$OUTPUT_DIR/utm-guest-tools-latest.iso"
iso_url="https://github.com/utmapp/qemu/releases/download/${UTM_GUEST_TOOLS_RELEASE}/${iso_file_name}"

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

mkdir -p "$OUTPUT_DIR"
# Keep local cache migration and pruning in sync with prepareVersionedWebFileCache in Go.
for cached_iso_path in "$OUTPUT_DIR"/utm-guest-tools-*.iso; do
	[ -f "$cached_iso_path" ] || continue
	[ "$cached_iso_path" = "$OUTPUT_PATH" ] && continue
	if [ "$cached_iso_path" = "$legacy_iso_path" ] && [ ! -e "$OUTPUT_PATH" ] &&
		[ "$(sha256_of "$cached_iso_path")" = "$UTM_GUEST_TOOLS_SHA256" ]; then
		mv "$cached_iso_path" "$OUTPUT_PATH" || echo "Warning: could not move $cached_iso_path, keeping it" >&2
		continue
	fi
	rm -f "$cached_iso_path"
done

if [ -f "$OUTPUT_PATH" ] && [ "$(sha256_of "$OUTPUT_PATH")" = "$UTM_GUEST_TOOLS_SHA256" ]; then
	echo "UTM Guest Tools $UTM_GUEST_TOOLS_VERSION already exists and checksum matches, skipping download."
	exit 0
fi

tmp_path="$(mktemp "${OUTPUT_PATH}.XXXXXX")"
trap 'rm -f "$tmp_path"' EXIT
curl --fail --progress-bar -L -o "$tmp_path" "$iso_url"
if [ "$(sha256_of "$tmp_path")" != "$UTM_GUEST_TOOLS_SHA256" ]; then
	echo "Checksum mismatch for downloaded $iso_file_name" >&2
	exit 1
fi
chmod 644 "$tmp_path"
mv "$tmp_path" "$OUTPUT_PATH"
trap - EXIT
