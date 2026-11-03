#!/bin/bash

set -e
# --print-file-name prints the cache file name only; keep its stdout and stderr free of xtrace noise.
if [ "${1:-}" != "--print-file-name" ]; then
	set -x
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

# renovate: datasource=custom.virtio-win depName=virtio-win versioning=loose
VIRTIO_WIN_VERSION="0.1.302-1"
VIRTIO_WIN_FILE_VERSION="${VIRTIO_WIN_VERSION%-*}"
# Updated by scripts/update-virtio-win-checksum.sh (see the "Sync virtio-win checksum" workflow)
VIRTIO_WIN_SHA256="303f7ae40dad495d6ae474fdc571df58958a4dbc5c37a522d80f9a203867949d"

# The cache file name carries the full version so a version bump never reuses a stale ISO.
# Keep it in sync with virtioWinISOCacheFileName in pkg/build/dependencies.go.
virtio_iso_file_name="virtio-win-${VIRTIO_WIN_VERSION}.iso"
if [ "${1:-}" = "--print-file-name" ]; then
	echo "$virtio_iso_file_name"
	exit 0
fi

virtio_iso_path="$CACHE_DIR/windows/$virtio_iso_file_name"
# Cache path used before the version was part of the file name.
legacy_virtio_iso_path="$CACHE_DIR/windows/virtio-win.iso"
virtio_iso_url="https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/virtio-win-${VIRTIO_WIN_VERSION}/virtio-win-${VIRTIO_WIN_FILE_VERSION}.iso"
tmp_path="${virtio_iso_path}.tmp"

verify_sha256() {
	local path="$1"

	if command -v sha256sum >/dev/null 2>&1; then
		printf '%s  %s\n' "$VIRTIO_WIN_SHA256" "$path" | sha256sum -c -
		return
	fi

	local actual_checksum
	actual_checksum="$(shasum -a 256 "$path" | awk '{print $1}')"
	[ "$actual_checksum" = "$VIRTIO_WIN_SHA256" ]
}

cleanup_tmp() {
	rm -f "$tmp_path"
}

trap cleanup_tmp EXIT

# Remove every other virtio-win ISO so outdated versions do not pile up after a version bump. The
# unversioned legacy file is moved into place instead when it matches the checksum.
# Mirrors prepareVirtioWinISOCache in pkg/build/dependencies.go.
for cached_iso_path in "$CACHE_DIR"/windows/virtio-win*.iso; do
	[ -f "$cached_iso_path" ] || continue
	[ "$cached_iso_path" = "$virtio_iso_path" ] && continue
	if [ "$cached_iso_path" = "$legacy_virtio_iso_path" ] && [ ! -e "$virtio_iso_path" ] && verify_sha256 "$cached_iso_path"; then
		echo "Moving $cached_iso_path to $virtio_iso_path"
		mv "$cached_iso_path" "$virtio_iso_path" || echo "Warning: could not move $cached_iso_path, keeping it" >&2
		continue
	fi
	echo "Removing outdated $cached_iso_path"
	rm -f "$cached_iso_path"
done

if [ -f "$virtio_iso_path" ]; then
	if verify_sha256 "$virtio_iso_path"; then
		echo "$virtio_iso_file_name already exists and checksum matches, skipping download"
		exit 0
	fi

	echo "Existing $virtio_iso_file_name checksum mismatch, re-downloading"
	rm -f "$virtio_iso_path"
fi

echo "Downloading $virtio_iso_file_name"
mkdir -p "$CACHE_DIR/windows"
curl --fail --progress-bar -L -o "$tmp_path" "$virtio_iso_url"

if verify_sha256 "$tmp_path"; then
	mv "$tmp_path" "$virtio_iso_path"
	trap - EXIT
else
	echo "Checksum mismatch for downloaded $virtio_iso_file_name" >&2
	exit 1
fi
