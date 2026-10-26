#!/bin/sh
# Install the sailwright CLI and the sailwright-gateway on macOS or Linux.
#
#   curl -fsSL https://github.com/csautter/sailwright/releases/latest/download/install.sh | sh
#
# Downloads the release archives for this OS and architecture, verifies them
# against the release checksums (and the checksums' cosign signature when
# cosign is installed), and copies the binaries to ~/.local/bin. No sudo.
#
# Options (pass after `sh -s --` when piping):
#   --version <tag>      Release to install, e.g. v0.20.0 (default: latest)
#   --install-dir <dir>  Target directory (default: ~/.local/bin)
#   --no-gateway         Install only the sailwright CLI
#
# The same settings can be given as SAILWRIGHT_VERSION, SAILWRIGHT_INSTALL_DIR
# and SAILWRIGHT_NO_GATEWAY=1.

set -eu

REPO_URL="https://github.com/csautter/sailwright"
# Base URL for release downloads; overridable so tests can serve local files.
DOWNLOAD_URL="${SAILWRIGHT_DOWNLOAD_URL:-${REPO_URL}/releases/download}"
COSIGN_IDENTITY="${REPO_URL}/.github/workflows/release-binaries.yml@refs/heads/main"
COSIGN_ISSUER="https://token.actions.githubusercontent.com"

version="${SAILWRIGHT_VERSION:-}"
install_dir="${SAILWRIGHT_INSTALL_DIR:-${HOME}/.local/bin}"
no_gateway="${SAILWRIGHT_NO_GATEWAY:-0}"

usage() {
	cat <<'EOF'
Usage: install.sh [--version <tag>] [--install-dir <dir>] [--no-gateway]

Installs sailwright and sailwright-gateway from the GitHub release.

  --version <tag>      Release to install, e.g. v0.20.0 (default: latest)
  --install-dir <dir>  Target directory (default: ~/.local/bin)
  --no-gateway         Install only the sailwright CLI
EOF
}

info() {
	printf '%s\n' "$*"
}

fail() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

while [ $# -gt 0 ]; do
	case "$1" in
	--version)
		[ $# -ge 2 ] || fail "--version needs a value"
		version="$2"
		shift 2
		;;
	--install-dir)
		[ $# -ge 2 ] || fail "--install-dir needs a value"
		install_dir="$2"
		shift 2
		;;
	--no-gateway)
		no_gateway=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		usage >&2
		fail "unknown option: $1"
		;;
	esac
done

if command -v curl >/dev/null 2>&1; then
	downloader=curl
elif command -v wget >/dev/null 2>&1; then
	downloader=wget
else
	fail "curl or wget is required"
fi

# download <url> <file>
download() {
	if [ "$downloader" = curl ]; then
		curl -fsSL -o "$2" "$1"
	else
		wget -q -O "$2" "$1"
	fi
}

# Print the tag the "latest release" page redirects to, without the GitHub API.
latest_tag() {
	if [ "$downloader" = curl ]; then
		url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "${REPO_URL}/releases/latest")"
	else
		url="$(wget -q -S --max-redirect=0 -O /dev/null "${REPO_URL}/releases/latest" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tr -d '\r' | tail -n1)" || true
	fi
	printf '%s\n' "${url##*/}"
}

detect_os() {
	case "$(uname -s)" in
	Darwin) echo darwin ;;
	Linux) echo linux ;;
	*) fail "unsupported OS: $(uname -s) (on Windows, use install.ps1)" ;;
	esac
}

detect_arch() {
	case "$(uname -m)" in
	x86_64 | amd64)
		# A shell running under Rosetta reports x86_64 on Apple silicon.
		if [ "$1" = darwin ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
			echo arm64
		else
			echo amd64
		fi
		;;
	arm64 | aarch64) echo arm64 ;;
	*) fail "unsupported architecture: $(uname -m)" ;;
	esac
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{ print $1 }'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{ print $1 }'
	else
		fail "sha256sum or shasum is required to verify the download"
	fi
}

# Print the expected SHA-256 for an asset, matched by file name so that the
# directory prefix written by sha256sum does not matter.
expected_sha256() {
	awk -v asset="$1" '{ file = $2; sub(/^\*/, "", file); sub(/.*\//, "", file); if (file == asset) { print $1; exit } }' "$2"
}

os="$(detect_os)"
arch="$(detect_arch "$os")"

if [ -z "$version" ]; then
	version="$(latest_tag)"
	case "$version" in
	v[0-9]*) ;;
	*) fail "could not determine the latest release; pass --version" ;;
	esac
fi
case "$version" in
v*) ;;
*) version="v${version}" ;;
esac
plain_version="${version#v}"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

info "Installing sailwright ${version} (${os}/${arch}) to ${install_dir}"

checksums="sailwright_${plain_version}_checksums.txt"
download "${DOWNLOAD_URL}/${version}/${checksums}" "${tmp_dir}/${checksums}" ||
	fail "could not download ${checksums}; does release ${version} exist?"

if command -v cosign >/dev/null 2>&1; then
	info "Verifying the checksums signature with cosign"
	download "${DOWNLOAD_URL}/${version}/${checksums}.sig" "${tmp_dir}/${checksums}.sig" ||
		fail "could not download ${checksums}.sig"
	download "${DOWNLOAD_URL}/${version}/${checksums}.pem" "${tmp_dir}/${checksums}.pem" ||
		fail "could not download ${checksums}.pem"
	cosign verify-blob \
		--certificate "${tmp_dir}/${checksums}.pem" \
		--signature "${tmp_dir}/${checksums}.sig" \
		--certificate-identity "$COSIGN_IDENTITY" \
		--certificate-oidc-issuer "$COSIGN_ISSUER" \
		"${tmp_dir}/${checksums}" >/dev/null 2>&1 ||
		fail "cosign could not verify ${checksums}; aborting"
fi

binaries="sailwright"
if [ "$no_gateway" != 1 ]; then
	binaries="sailwright sailwright-gateway"
fi

for name in $binaries; do
	asset="${name}_${plain_version}_${os}_${arch}.tar.gz"
	download "${DOWNLOAD_URL}/${version}/${asset}" "${tmp_dir}/${asset}" ||
		fail "could not download ${asset}"

	expected="$(expected_sha256 "$asset" "${tmp_dir}/${checksums}")"
	[ -n "$expected" ] || fail "no checksum for ${asset} in ${checksums}"
	actual="$(sha256_of "${tmp_dir}/${asset}")"
	[ "$actual" = "$expected" ] ||
		fail "checksum mismatch for ${asset} (expected ${expected}, got ${actual}); aborting"

	mkdir -p "${tmp_dir}/${name}"
	tar -xzf "${tmp_dir}/${asset}" -C "${tmp_dir}/${name}" "$name"
done

mkdir -p "$install_dir"
for name in $binaries; do
	# Copy next to the target and rename so a running binary is replaced atomically.
	cp "${tmp_dir}/${name}/${name}" "${install_dir}/.${name}.tmp"
	chmod 0755 "${install_dir}/.${name}.tmp"
	mv -f "${install_dir}/.${name}.tmp" "${install_dir}/${name}"
	info "Installed ${install_dir}/${name}"
done

case ":${PATH}:" in
*":${install_dir}:"*) ;;
*)
	info ""
	info "${install_dir} is not on your PATH. Add this line to your shell profile"
	info "(~/.zprofile for zsh, ~/.profile or ~/.bashrc for bash):"
	info "  export PATH=\"${install_dir}:\$PATH\""
	;;
esac

info ""
info "Next: sailwright install && sailwright build list"
