#!/bin/bash
# Syncs the qemu-efi-aarch64 version, SHA-256 and snapshot timestamp across all pins.
#
# The version in pkg/build/dependencies.go is the source of truth (Renovate updates
# it). This script copies it to scripts/macos/download-arm64-uefi.sh, downloads the
# matching deb, checks it against snapshot.debian.org and writes its SHA-256 and the
# snapshot first_seen timestamp to both files.
#
# Still manual (see build/packer/linux/ubuntu/README.md): the edk2 release in the
# template comments and a boot test on an Apple Silicon Mac.
#
# Usage: scripts/update-qemu-efi-aarch64-pin.sh [path-to-already-downloaded-deb]

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script_file="$repo_root/scripts/macos/download-arm64-uefi.sh"
go_file="$repo_root/pkg/build/dependencies.go"

version="$(sed -n -E 's/^\tqemuEfiAarch64Version[[:space:]]*=[[:space:]]*"(.*)"$/\1/p' "$go_file")"
if [ -z "$version" ]; then
	echo "Could not read qemuEfiAarch64Version from $go_file" >&2
	exit 1
fi
if ! [[ "$version" =~ ^[0-9A-Za-z.+~-]+$ ]]; then
	echo "Unexpected qemu-efi-aarch64 version: $version" >&2
	exit 1
fi

deb_path="${1:-}"
if [ -z "$deb_path" ]; then
	tmp_dir="$(mktemp -d)"
	trap 'rm -rf "$tmp_dir"' EXIT
	deb_path="$tmp_dir/qemu-efi-aarch64_all.deb"
	url="https://deb.debian.org/debian/pool/main/e/edk2/qemu-efi-aarch64_${version}_all.deb"
	echo "Downloading $url"
	curl --fail --silent --show-error -L -o "$deb_path" "$url"
fi

sha256="$(sha256sum "$deb_path" | awk '{print $1}')"
# snapshot.debian.org indexes files by SHA-1, so finding the file there also checks
# the download against a second source.
sha1="$(sha1sum "$deb_path" | awk '{print $1}')"
encoded_version="${version//+/%2B}"
fileinfo_url="https://snapshot.debian.org/mr/package/edk2/${encoded_version}/binfiles/qemu-efi-aarch64/${encoded_version}?fileinfo=1"
echo "Looking up $fileinfo_url"
first_seen="$(curl --fail --silent --show-error -L "$fileinfo_url" |
	jq -r --arg sha1 "$sha1" '[.fileinfo[$sha1][]? | select(.archive_name == "debian") | .first_seen] | min // empty')"
if ! [[ "$first_seen" =~ ^[0-9]{8}T[0-9]{6}Z$ ]]; then
	echo "snapshot.debian.org does not list qemu-efi-aarch64 $version with SHA-1 $sha1 yet; retry once it has indexed the file" >&2
	exit 1
fi

echo "qemu-efi-aarch64 $version sha256: $sha256 snapshot: $first_seen"

sed -i -E \
	-e "s/^(QEMU_EFI_AARCH64_VERSION=\")[^\"]+(\")/\1${version}\2/" \
	-e "s/^(QEMU_EFI_AARCH64_SHA256=\")[a-f0-9]+(\")/\1${sha256}\2/" \
	-e "s/^(QEMU_EFI_AARCH64_SNAPSHOT_TIMESTAMP=\")[^\"]+(\")/\1${first_seen}\2/" \
	"$script_file"
sed -i -E \
	-e "s/^(\tqemuEfiAarch64SHA256[[:space:]]*=[[:space:]]*\")[a-f0-9]+(\")/\1${sha256}\2/" \
	-e "s/^(\tqemuEfiAarch64SnapshotTimestamp[[:space:]]*=[[:space:]]*\")[^\"]+(\")/\1${first_seen}\2/" \
	-e "s/^(\tqemuEfiAarch64PinnedFor[[:space:]]*=[[:space:]]*\")[^\"]+(\")/\1${version}\2/" \
	"$go_file"
