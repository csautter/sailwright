#!/bin/bash
# Pin the ISO advertised by UTM, using its versioned GitHub URL and published SHA-256.
# Updates Go, the standalone downloader and CI cache names together.
# Usage: scripts/update-utm-guest-tools-pin.sh
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
iso_url="$(curl --fail --silent --show-error --head --output /dev/null --write-out '%{redirect_url}' \
	'https://getutm.app/downloads/utm-guest-tools-latest.iso')"
if [[ "$iso_url" =~ ^https://github\.com/utmapp/qemu/releases/download/([A-Za-z0-9._-]+)/utm-guest-tools-([0-9]+\.[0-9]+\.[0-9]+)\.iso$ ]]; then
	release="${BASH_REMATCH[1]}"
	version="${BASH_REMATCH[2]}"
else
	echo "Unexpected UTM Guest Tools redirect: $iso_url" >&2
	exit 1
fi

# Fail before changing any files if the advertised asset has no published SHA-256.
if ! sha256="$(curl --fail --silent --show-error \
	"https://api.github.com/repos/utmapp/qemu/releases/tags/$release" |
	jq -er --arg url "$iso_url" '.assets[] | select(.browser_download_url == $url) | .digest | strings | select(test("^sha256:[a-f0-9]{64}$")) | ltrimstr("sha256:")')" ||
	! [[ "$sha256" =~ ^[a-f0-9]{64}$ ]]; then
	echo "Missing or invalid SHA-256 for $iso_url" >&2
	exit 1
fi

sed -i -E \
	-e "s/^(UTM_GUEST_TOOLS_VERSION=\")[^\"]+(\")/\1${version}\2/" \
	-e "s/^(UTM_GUEST_TOOLS_RELEASE=\")[^\"]+(\")/\1${release}\2/" \
	-e "s/^(UTM_GUEST_TOOLS_SHA256=\")[a-f0-9]+(\")/\1${sha256}\2/" \
	"$repo_root/scripts/macos/download-utm-guest-tools.sh"
sed -i -E \
	-e "s/^(\tutmGuestToolsVersion[[:space:]]*=[[:space:]]*\")[^\"]+(\")/\1${version}\2/" \
	-e "s/^(\tutmGuestToolsRelease[[:space:]]*=[[:space:]]*\")[^\"]+(\")/\1${release}\2/" \
	-e "s/^(\tutmGuestToolsSHA256[[:space:]]*=[[:space:]]*\")[a-f0-9]+(\")/\1${sha256}\2/" \
	"$repo_root/pkg/build/dependencies.go"
for workflow in test-build-linux.yml test-build-macos.yml; do
	sed -i -E "s/utm-guest-tools-[0-9]+\.[0-9]+\.[0-9]+\.iso/utm-guest-tools-${version}.iso/g" \
		"$repo_root/.github/workflows/$workflow"
done
echo "UTM Guest Tools $version ($release) sha256: $sha256"
