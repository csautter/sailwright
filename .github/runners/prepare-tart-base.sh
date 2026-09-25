#!/bin/bash
set -e

# One-time provisioning script that builds a golden tart VM image with
# pre-installed tooling (Homebrew, Azure CLI, gh CLI, ...).
#
# Run this once – or whenever you need to update the toolset.
# The resulting image is used as VM_BASE_IMAGE by create-macos-tart-runner.sh.
#
# Optional environment variables:
#   TART_SOURCE_IMAGE  - local name for the upstream base  (default: tahoe-base)
#   TART_SOURCE_REMOTE - remote OCI image to pull if missing (default: ghcr.io/cirruslabs/macos-tahoe-base:latest)
#   TART_GOLDEN_IMAGE  - name of the golden image to produce (default: tahoe-runner)
#   VM_SSH_USER        - SSH user inside the VM             (default: admin)
#   VM_SSH_PASS        - SSH password inside the VM         (default: admin)
#   NET_INTERFACE      - bridged network interface          (default: Wi-Fi)
#   RUNNER_DIR         - install directory inside the VM    (default: /Users/admin/actions-runner)
#   RUNNER_VERSION     - GitHub Actions runner version      (default: pinned below)

# ─── Configuration ─────────────────────────────────────────────────────────────
TART_SOURCE_IMAGE="${TART_SOURCE_IMAGE:-tahoe-base}"
TART_SOURCE_REMOTE="${TART_SOURCE_REMOTE:-ghcr.io/cirruslabs/macos-tahoe-base:latest}"
TART_GOLDEN_IMAGE="${TART_GOLDEN_IMAGE:-tahoe-runner}"
VM_SSH_USER="${VM_SSH_USER:-admin}"
VM_SSH_PASS="${VM_SSH_PASS:-admin}"
NET_INTERFACE="${NET_INTERFACE:-Wi-Fi}"
RUNNER_DIR="${RUNNER_DIR:-/Users/admin/actions-runner}"
BUILD_VM="build-${TART_GOLDEN_IMAGE}"

# ─── Renovate-managed version pins ────────────────────────────────────────────
# renovate: datasource=github-releases depName=actions/runner versioning=loose
DEFAULT_RUNNER_VERSION="2.337.0"
# renovate: datasource=custom.homebrew-formula depName=azure-cli packageName=azure-cli versioning=loose
AZURE_CLI_VERSION="2.88.0"
# renovate: datasource=custom.homebrew-formula depName=gh packageName=gh versioning=loose
GH_CLI_VERSION="2.96.0"
# renovate: datasource=custom.hashicorp depName=packer packageName=packer versioning=semver
PACKER_VERSION="1.15.4"
# renovate: datasource=custom.homebrew-formula depName=go packageName=go versioning=loose
GO_VERSION="1.26.5"
# renovate: datasource=custom.homebrew-cask depName=utm packageName=utm versioning=loose
UTM_VERSION="4.7.5"
# renovate: datasource=custom.homebrew-formula depName=qemu packageName=qemu versioning=loose
QEMU_VERSION="11.0.2"
# renovate: datasource=custom.homebrew-formula depName=xz packageName=xz versioning=loose
XZ_VERSION="5.8.3"
# renovate: datasource=custom.homebrew-formula depName=ffmpeg packageName=ffmpeg versioning=loose
FFMPEG_VERSION="8.1.2"
# renovate: datasource=custom.homebrew-formula depName=vncsnapshot packageName=vncsnapshot versioning=loose
VNCSNAPSHOT_VERSION="1.2a"
# renovate: datasource=custom.homebrew-formula depName=xorriso packageName=xorriso versioning=loose
XORRISO_VERSION="1.5.8.pl02"
# renovate: datasource=custom.homebrew-formula depName=ansible packageName=ansible versioning=loose
ANSIBLE_VERSION="14.1.0"
# renovate: datasource=custom.homebrew-formula depName=python@3.13 packageName=python@3.13 versioning=loose
PYTHON_3_13_VERSION="3.13.14"

RUNNER_VERSION="${RUNNER_VERSION:-$DEFAULT_RUNNER_VERSION}"
# ───────────────────────────────────────────────────────────────────────────────

# ─── Pre-flight checks ─────────────────────────────────────────────────────────
for cmd in tart sshpass; do
	if ! command -v "$cmd" &>/dev/null; then
		echo "ERROR: '$cmd' not found."
		case "$cmd" in
			tart)    echo "  Install: brew install tart" ;;
			sshpass) echo "  Install: brew install sshpass" ;;
		esac
		exit 1
	fi
done

echo "Runner version: ${RUNNER_VERSION}"
RUNNER_ARCHIVE="actions-runner-osx-arm64-${RUNNER_VERSION}.tar.gz"
RUNNER_DOWNLOAD_URL="https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/${RUNNER_ARCHIVE}"

# ─── Ensure upstream base image is present ────────────────────────────────────
if ! tart list | awk 'NR>1 && $1=="local" {print $2}' | grep -qx "${TART_SOURCE_IMAGE}"; then
	echo "Pulling upstream base image '${TART_SOURCE_IMAGE}' from ${TART_SOURCE_REMOTE} ..."
	tart clone "${TART_SOURCE_REMOTE}" "${TART_SOURCE_IMAGE}"
else
	echo "Upstream base image '${TART_SOURCE_IMAGE}' already present."
fi

# ─── Remove any stale build VM ────────────────────────────────────────────────
if tart list | awk 'NR>1 && $1=="local" {print $2}' | grep -qx "${BUILD_VM}"; then
	echo "Removing stale build VM '${BUILD_VM}'..."
	tart stop "${BUILD_VM}" 2>/dev/null || true
	tart delete "${BUILD_VM}"
fi

# ─── Clone source → ephemeral build VM ───────────────────────────────────────
echo "Cloning '${TART_SOURCE_IMAGE}' → '${BUILD_VM}' ..."
tart clone "${TART_SOURCE_IMAGE}" "${BUILD_VM}"

# ─── Increase disk size by 40 GB ─────────────────────────────────────────────
# The CI job needs ~6 GB for the extracted Windows ISO source files,
# ~6 GB for the output ISO, plus the existing tooling (~14 GB used on base).
# 17 GB free after +20 GB was insufficient; +40 GB gives ~37 GB free headroom.
current_disk_gb=$(tart get "${BUILD_VM}" --format json | python3 -c "import sys,json,math; print(math.ceil(float(json.load(sys.stdin)['Size'])))")
new_disk_gb=$((current_disk_gb + 50))
echo "Resizing disk: ${current_disk_gb} GB → ${new_disk_gb} GB ..."
tart set "${BUILD_VM}" --disk-size "${new_disk_gb}"

# ─── Cleanup trap (only deletes build VM on failure) ─────────────────────────
BUILD_SUCCESS=false

cleanup_build_vm() {
	echo "Stopping build VM '${BUILD_VM}'..."
	tart stop "${BUILD_VM}" 2>/dev/null || true
	if [[ "${BUILD_SUCCESS}" != "true" ]]; then
		echo "Provisioning failed – removing build VM."
		tart delete "${BUILD_VM}" 2>/dev/null || true
	fi
}
trap cleanup_build_vm EXIT INT TERM

# ─── Start build VM ───────────────────────────────────────────────────────────
echo "Starting build VM '${BUILD_VM}'..."
tart run --no-graphics --net-bridged="${NET_INTERFACE}" "${BUILD_VM}" &

# ─── Wait for IP ──────────────────────────────────────────────────────────────
VM_IP=""
echo "Waiting for VM IP..."
while [[ -z "$VM_IP" ]]; do
	VM_IP=$(tart ip --resolver=arp "${BUILD_VM}" 2>/dev/null || echo "")
	sleep 1
done
echo "VM IP: ${VM_IP}"

# ─── SSH helper ───────────────────────────────────────────────────────────────
vm_ssh() {
	sshpass -p "${VM_SSH_PASS}" ssh \
		-o "StrictHostKeyChecking=no" \
		-o "UserKnownHostsFile=/dev/null" \
		-o "ConnectTimeout=5" \
		"${VM_SSH_USER}@${VM_IP}" "$@"
}

ssh_quote() {
	printf '%q' "$1"
}

# ─── Wait for SSH ─────────────────────────────────────────────────────────────
until vm_ssh "true" 2>/dev/null; do
	echo "Waiting for SSH to become available..."
	sleep 2
done
echo "SSH connection successful."

# ─── Provision tools ──────────────────────────────────────────────────────────
echo "Starting provisioning..."

# ─── Provision helpers ────────────────────────────────────────────────────────
# Opens a fresh SSH session per step
provision_step() {
	local label="$1"
	local body="$2"
	echo "── ${label} ─────────────────────────────────────────────────────"
	vm_ssh bash <<ENDSSH
eval "\$(/opt/homebrew/bin/brew shellenv)" 2>/dev/null || true
set -e
${body}
ENDSSH
}

# brew_install LABEL CMD PKG VERSION [TAP] [FLAGS]
#   LABEL   - human-readable name shown in output
#   CMD     - binary name (checked via command -v) or absolute path (checked via -e)
#   PKG     - brew package to install (use "org/tap/pkg" form when a tap is needed)
#   VERSION - Renovate-managed package version pin
#   TAP     - (optional) brew tap to register before installing
#   FLAGS   - (optional) extra brew install flags, e.g. --cask
brew_version_compare() {
	local left="$1" right="$2"

	awk -v left="${left}" -v right="${right}" '
function tokenize(version, values, types,    i, char, type, last_type, count, token) {
	version = tolower(version)
	count = 0
	token = ""
	last_type = ""

	for (i = 1; i <= length(version); i++) {
		char = substr(version, i, 1)
		if (char ~ /[0-9]/) {
			type = "number"
		} else if (char ~ /[a-z]/) {
			type = "text"
		} else {
			type = "delimiter"
		}

		if (type == "delimiter") {
			if (token != "") {
				values[++count] = token
				types[count] = last_type
				token = ""
			}
			last_type = ""
			continue
		}

		if (token != "" && type != last_type) {
			values[++count] = token
			types[count] = last_type
			token = ""
		}

		token = token char
		last_type = type
	}

	if (token != "") {
		values[++count] = token
		types[count] = last_type
	}

	return count
}

function normalize_number(value) {
	sub(/^0+/, "", value)
	return value == "" ? "0" : value
}

BEGIN {
	left_count = tokenize(left, left_values, left_types)
	right_count = tokenize(right, right_values, right_types)
	max_count = left_count > right_count ? left_count : right_count

	for (i = 1; i <= max_count; i++) {
		if (i > left_count) {
			print -1
			exit
		}
		if (i > right_count) {
			print 1
			exit
		}

		if (left_types[i] == "number" && right_types[i] == "number") {
			left_number = normalize_number(left_values[i])
			right_number = normalize_number(right_values[i])

			if (length(left_number) != length(right_number)) {
				print (length(left_number) > length(right_number) ? 1 : -1)
				exit
			}
			if (left_number != right_number) {
				print (left_number > right_number ? 1 : -1)
				exit
			}
		} else if (left_values[i] != right_values[i]) {
			if (left_types[i] == right_types[i]) {
				print (left_values[i] > right_values[i] ? 1 : -1)
				exit
			}
			print (left_types[i] == "number" ? 1 : -1)
			exit
		}
	}

	print 0
}'
}

brew_version_without_revision() {
	local version="$1"

	if [[ "${version}" =~ ^(.+)_([0-9]+)$ ]]; then
		printf '%s\n' "${BASH_REMATCH[1]}"
	else
		printf '%s\n' "${version}"
	fi
}

brew_version_matches() {
	local installed="$1" pinned="$2" flags="${3:-}"
	local normalized_installed normalized_pinned comparison

	if [[ -z "${installed}" ]]; then
		return 1
	fi

	if [[ "${installed}" == "${pinned}" ]]; then
		return 0
	fi

	normalized_installed="${installed}"
	normalized_pinned="${pinned}"
	if [[ " ${flags} " != *" --cask "* ]]; then
		normalized_installed="$(brew_version_without_revision "${installed}")"
		normalized_pinned="$(brew_version_without_revision "${pinned}")"
		if [[ "${normalized_installed}" == "${normalized_pinned}" ]]; then
			return 0
		fi
	fi

	comparison="$(brew_version_compare "${normalized_installed}" "${normalized_pinned}")"
	[[ "${comparison}" =~ ^-?[0-9]+$ && "${comparison}" -ge 0 ]]
}

brew_installed_version() {
	local list_pkg="$1"
	shift

	local -a list_flags=("$@")
	local version best_version comparison

	while read -r version; do
		[[ -n "${version}" ]] || continue

		if [[ -z "${best_version:-}" ]]; then
			best_version="${version}"
			continue
		fi

		comparison="$(brew_version_compare "${version}" "${best_version}")"
		if [[ "${comparison}" =~ ^-?[0-9]+$ && "${comparison}" -gt 0 ]]; then
			best_version="${version}"
		fi
	done < <(brew list "${list_flags[@]}" --versions "${list_pkg}" 2>/dev/null | awk '{ for (i = 2; i <= NF; i++) print $i }')

	printf '%s\n' "${best_version:-}"
}

brew_install() {
	local label="$1" cmd="$2" pkg="$3" version="$4" tap="${5:-}" flags="${6:-}"
	local tap_line="" check list_pkg list_flags pin_line unpin_line status_line version_helpers

	list_pkg="${pkg##*/}"
	list_flags=""
	pin_line="brew pin ${list_pkg} || true"
	unpin_line="brew unpin ${list_pkg} || true"
	status_line="echo '${label}' \"\${installed_version}\" 'installed and pinned (satisfies ${version}).'"
	if [[ " ${flags} " == *" --cask "* ]]; then
		list_flags="--cask"
		pin_line=""
		unpin_line=""
		status_line="echo '${label}' \"\${installed_version}\" 'installed and satisfies ${version}.'"
	fi

	version_helpers="$(declare -f brew_version_compare brew_version_without_revision brew_version_matches brew_installed_version)"

	[[ -n "$tap" ]] && tap_line="brew tap ${tap} || echo 'WARNING: tap ${tap} failed, continuing...'"
	if [[ "$cmd" == /* ]]; then
		check="[[ -e ${cmd} ]]"
	else
		check="command -v ${cmd} &>/dev/null"
	fi
	provision_step "${label}" "
${tap_line}
${version_helpers}

installed_version=\"\$(brew_installed_version ${list_pkg} ${list_flags})\"

if ! brew_version_matches \"\${installed_version}\" \"${version}\" \"${flags}\"; then
	if [[ -n \"\${installed_version}\" ]]; then
		echo '${label} version' \"\${installed_version}\" 'is older than required ${version}; updating via Homebrew...'
		${unpin_line}
		brew upgrade ${flags} ${pkg} || brew install ${flags} ${pkg}
	else
		echo 'Installing ${label} ${version}...'
		brew install ${flags} ${pkg}
	fi

	installed_version=\"\$(brew_installed_version ${list_pkg} ${list_flags})\"
fi

if ! brew_version_matches \"\${installed_version}\" \"${version}\" \"${flags}\"; then
	echo 'ERROR: ${label} installed version' \"\${installed_version:-<missing>}\" 'is older than required ${version}.' >&2
	echo '       Update the Renovate-managed pin or ensure Homebrew can provide the required version.' >&2
	exit 1
fi

if ! ${check}; then
	echo 'ERROR: ${label} installed at pinned version ${version}, but expected command/path ${cmd} was not found.' >&2
	exit 1
fi

${pin_line}
${status_line}
"
}

# ── Homebrew (must run first; brew may not exist yet) ─────────────────────────
vm_ssh bash <<'PROVISION'
set -e

BREW="/opt/homebrew/bin/brew"
BREW_PROFILE_LINE='eval "$(/opt/homebrew/bin/brew shellenv)"'

if [[ ! -x "$BREW" ]]; then
	echo "Installing Homebrew..."
	NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
else
	echo "Homebrew already installed."
fi

# Ensure brew is on PATH for login shells
if ! grep -qF "$BREW_PROFILE_LINE" ~/.zprofile 2>/dev/null; then
	echo "$BREW_PROFILE_LINE" >> ~/.zprofile
fi
eval "$(/opt/homebrew/bin/brew shellenv)"

# Make Homebrew binaries available system-wide (non-login shells, GH Actions runner)
if [[ ! -f /etc/paths.d/homebrew ]]; then
	echo "Registering Homebrew in /etc/paths.d/homebrew..."
	printf '/opt/homebrew/bin\n/opt/homebrew/sbin\n' | sudo tee /etc/paths.d/homebrew > /dev/null
fi
PROVISION

# ── Tools ─────────────────────────────────────────────────────────────────────
brew_install "Azure CLI"   az                        azure-cli             "${AZURE_CLI_VERSION}"
brew_install "GitHub CLI"  gh                        gh                    "${GH_CLI_VERSION}"
brew_install "Packer"      packer                    hashicorp/tap/packer  "${PACKER_VERSION}"       hashicorp/tap
brew_install "Go"          go                        go                    "${GO_VERSION}"
brew_install "UTM"         /Applications/UTM.app     utm                   "${UTM_VERSION}"          ""  --cask
brew_install "QEMU"        qemu-img                  qemu                  "${QEMU_VERSION}"
brew_install "xz"          xz                        xz                    "${XZ_VERSION}"
brew_install "FFmpeg"      ffmpeg                    ffmpeg                "${FFMPEG_VERSION}"
brew_install "vncsnapshot" vncsnapshot               vncsnapshot           "${VNCSNAPSHOT_VERSION}"
brew_install "xorriso"     xorriso                   xorriso               "${XORRISO_VERSION}"
brew_install "Ansible"     ansible                   ansible               "${ANSIBLE_VERSION}"
brew_install "Python 3"    python3                   python@3.13           "${PYTHON_3_13_VERSION}"

# ─── Download and install GitHub Actions runner ───────────────────────────────
echo "Installing GitHub Actions runner ${RUNNER_VERSION} into golden image..."

vm_ssh bash -s -- \
	"$(ssh_quote "${RUNNER_DIR}")" \
	"$(ssh_quote "${RUNNER_ARCHIVE}")" \
	"$(ssh_quote "${RUNNER_DOWNLOAD_URL}")" \
	"$(ssh_quote "${RUNNER_VERSION}")" <<'RUNNER_INSTALL'
set -e

RUNNER_DIR="$1"
RUNNER_ARCHIVE="$2"
RUNNER_DOWNLOAD_URL="$3"
RUNNER_VERSION="$4"

mkdir -p "${RUNNER_DIR}"
cd "${RUNNER_DIR}"

echo "Downloading runner ${RUNNER_ARCHIVE}..."
curl -fsSL -o "${RUNNER_ARCHIVE}" "${RUNNER_DOWNLOAD_URL}"
tar xzf "${RUNNER_ARCHIVE}"
rm -f "${RUNNER_ARCHIVE}"

echo "Runner ${RUNNER_VERSION} installed to ${RUNNER_DIR}."
RUNNER_INSTALL

echo "Runner installation complete."
echo "Provisioning finished successfully."


# ─── Graceful shutdown ────────────────────────────────────────────────────────
echo "Shutting down build VM..."
vm_ssh "sudo shutdown -h now" 2>/dev/null || true
sleep 8
tart stop "${BUILD_VM}" 2>/dev/null || true

# Mark success before the trap fires
BUILD_SUCCESS=true

# ─── Promote build VM → golden image ─────────────────────────────────────────
if tart list | awk 'NR>1 && $1=="local" {print $2}' | grep -qx "${TART_GOLDEN_IMAGE}"; then
	echo "Removing previous golden image '${TART_GOLDEN_IMAGE}'..."
	tart delete "${TART_GOLDEN_IMAGE}"
fi

echo "Cloning '${BUILD_VM}' → '${TART_GOLDEN_IMAGE}' (golden image)..."
tart clone "${BUILD_VM}" "${TART_GOLDEN_IMAGE}"
tart delete "${BUILD_VM}"

echo ""
echo "════════════════════════════════════════════════════════════"
echo " Golden image '${TART_GOLDEN_IMAGE}' is ready."
echo " Runner ${RUNNER_VERSION} pre-installed at ${RUNNER_DIR}."
echo " Use it with: VM_BASE_IMAGE=${TART_GOLDEN_IMAGE} ./create-macos-tart-runner.sh"
echo "════════════════════════════════════════════════════════════"
