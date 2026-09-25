#!/usr/bin/env bash

set -e

if [[ "${DEV_ALCHEMY_INSTALL_TRACE:-false}" == "true" ]]; then
	set -x
fi

# Renovate-managed version pins
# renovate: datasource=custom.hashicorp depName=packer packageName=packer versioning=semver
PACKER_VERSION="1.15.4"
# renovate: datasource=custom.homebrew-formula depName=go packageName=go versioning=loose
GO_VERSION="1.26.5"
# renovate: datasource=custom.homebrew-cask depName=utm packageName=utm versioning=loose
UTM_VERSION="4.7.5"
# renovate: datasource=custom.homebrew-formula depName=qemu packageName=qemu versioning=loose
QEMU_VERSION="11.1.1"
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

install_go="false"

while [[ $# -gt 0 ]]; do
	case "$1" in
	--with-go)
		install_go="true"
		shift
		;;
	*)
		echo "Unknown option: $1" >&2
		exit 1
		;;
	esac
done

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
	local list_pkg="${pkg##*/}"
	local installed_version
	local version_satisfied="false"
	local installed_description
	local -a install_args=("${pkg}")
	local -a list_flags=()

	if [[ -n "${tap}" ]]; then
		brew tap "${tap}" || echo "WARNING: tap ${tap} failed, continuing..."
	fi

	if [[ " ${flags} " == *" --cask "* ]]; then
		install_args=(--cask "${pkg}")
		list_flags=(--cask)
	fi

	installed_version="$(brew_installed_version "${list_pkg}" "${list_flags[@]}")"

	if ! brew_version_matches "${installed_version}" "${version}" "${flags}"; then
		if [[ -n "${installed_version}" ]]; then
			echo "${label} version ${installed_version} is older than required ${version}; updating via Homebrew..."
			if [[ " ${flags} " != *" --cask "* ]]; then
				brew unpin "${list_pkg}" || true
			fi
			brew upgrade "${install_args[@]}" || brew install "${install_args[@]}"
		else
			echo "Installing ${label} ${version}..."
			brew install "${install_args[@]}"
		fi

		installed_version="$(brew_installed_version "${list_pkg}" "${list_flags[@]}")"
	fi

	if brew_version_matches "${installed_version}" "${version}" "${flags}"; then
		version_satisfied="true"
	else
		echo "WARNING: ${label} installed version ${installed_version:-<missing>} does not satisfy required ${version} after Homebrew update/install." >&2
		echo "         Continuing because Homebrew may not have the Renovate-managed version available yet." >&2
	fi

	if [[ "${version_satisfied}" == "true" ]]; then
		installed_description="at version ${installed_version:-<unknown>} (satisfies ${version})"
	else
		installed_description="at version ${installed_version:-<missing>} (expected ${version})"
	fi

	if [[ "${cmd}" == /* ]]; then
		if [[ ! -e "${cmd}" ]]; then
			echo "ERROR: ${label} installed ${installed_description}, but expected path ${cmd} was not found." >&2
			exit 1
		fi
	elif ! command -v "${cmd}" &>/dev/null; then
		echo "ERROR: ${label} installed ${installed_description}, but expected command ${cmd} was not found." >&2
		exit 1
	fi

	if [[ " ${flags} " != *" --cask "* ]]; then
		if [[ "${version_satisfied}" == "true" ]]; then
			brew pin "${list_pkg}" || true
			echo "${label} ${installed_version} installed and pinned (satisfies ${version})."
		else
			echo "${label} ${installed_version:-<unknown>} installed but does not satisfy ${version}; leaving unpinned."
		fi
	else
		if [[ "${version_satisfied}" == "true" ]]; then
			echo "${label} ${installed_version} installed and satisfies ${version}."
		else
			echo "${label} ${installed_version:-<unknown>} installed but does not satisfy ${version}; continuing."
		fi
	fi
}

brew_install "Packer"      packer                    hashicorp/tap/packer  "${PACKER_VERSION}"       hashicorp/tap
brew_install "UTM"         /Applications/UTM.app     utm                   "${UTM_VERSION}"          ""  --cask
brew_install "QEMU"        qemu-img                  qemu                  "${QEMU_VERSION}"

brew_install "xz"          xz                        xz                    "${XZ_VERSION}"
brew_install "FFmpeg"      ffmpeg                    ffmpeg                "${FFMPEG_VERSION}"
brew_install "vncsnapshot" vncsnapshot               vncsnapshot           "${VNCSNAPSHOT_VERSION}"
brew_install "xorriso"     xorriso                   xorriso               "${XORRISO_VERSION}"
brew_install "Ansible"     ansible                   ansible               "${ANSIBLE_VERSION}"

if [[ "${install_go}" == "true" ]]; then
	brew_install "Go" go go "${GO_VERSION}"
fi
