#!/bin/bash
# Syncs the virtio-win version and ISO SHA256 across all pins.
#
# The version in scripts/macos/download-virtio-win-iso.sh is the source of truth
# (Renovate updates it). This script copies it to pkg/build/dependencies.go,
# downloads the matching ISO and writes its SHA256 to both files.
#
# Usage: scripts/update-virtio-win-checksum.sh [path-to-already-downloaded-iso]

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script_file="$repo_root/scripts/macos/download-virtio-win-iso.sh"
go_file="$repo_root/pkg/build/dependencies.go"

version="$(sed -n 's/^VIRTIO_WIN_VERSION="\(.*\)"$/\1/p' "$script_file")"
if [ -z "$version" ]; then
	echo "Could not read VIRTIO_WIN_VERSION from $script_file" >&2
	exit 1
fi
file_version="${version%-*}"

iso_path="${1:-}"
if [ -z "$iso_path" ]; then
	tmp_dir="$(mktemp -d)"
	trap 'rm -rf "$tmp_dir"' EXIT
	iso_path="$tmp_dir/virtio-win.iso"
	url="https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/virtio-win-${version}/virtio-win-${file_version}.iso"
	echo "Downloading $url"
	curl --fail --silent --show-error -L -o "$iso_path" "$url"
fi

sha256="$(sha256sum "$iso_path" | awk '{print $1}')"
echo "virtio-win $version sha256: $sha256"

sed -i -E "s/^(VIRTIO_WIN_SHA256=\")[a-f0-9]+(\")/\1${sha256}\2/" "$script_file"
sed -i -E "s/^(\tvirtioWinVersion = \")[^\"]+(\")/\1${version}\2/" "$go_file"
sed -i -E "s/^(\tvirtioWinSHA256 = \")[a-f0-9]+(\")/\1${sha256}\2/" "$go_file"
