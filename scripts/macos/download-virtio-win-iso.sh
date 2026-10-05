#!/bin/bash

set -ex

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
VIRTIO_WIN_SHA256="e14cf2b94492c3e925f0070ba7fdfedeb2048c91eea9c5a5afb30232a3976331"

virtio_iso_path="$CACHE_DIR/windows/virtio-win.iso"
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

if [ -f "$virtio_iso_path" ]; then
	if verify_sha256 "$virtio_iso_path"; then
		echo "virtio-win.iso already exists and checksum matches, skipping download"
		exit 0
	fi

	echo "Existing virtio-win.iso checksum mismatch, re-downloading"
	rm -f "$virtio_iso_path"
fi

echo "Downloading virtio-win.iso"
mkdir -p "$CACHE_DIR/windows"
curl --fail --progress-bar -L -o "$tmp_path" "$virtio_iso_url"

if verify_sha256 "$tmp_path"; then
	mv "$tmp_path" "$virtio_iso_path"
	trap - EXIT
else
	echo "Checksum mismatch for downloaded virtio-win.iso" >&2
	exit 1
fi
