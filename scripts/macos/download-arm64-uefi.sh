#!/bin/bash

set -e

host_os="$(uname -s)"
if [ "$host_os" = "Darwin" ]; then
	default_app_data_dir="$HOME/Library/Application Support/dev-alchemy"
else
	default_app_data_dir="${XDG_DATA_HOME:-$HOME/.local/share}/dev-alchemy"
fi

app_data_dir="${DEV_ALCHEMY_APP_DATA_DIR:-$default_app_data_dir}"
cache_dir="${DEV_ALCHEMY_CACHE_DIR:-$app_data_dir/cache}"
export DEV_ALCHEMY_APP_DATA_DIR="$app_data_dir"
export DEV_ALCHEMY_CACHE_DIR="$cache_dir"

# Keep in sync with pkg/build/dependencies.go. The firmware is pinned because a
# new edk2 or QEMU release can leave ARM64 guests hanging in UEFI.
# renovate: datasource=deb depName=qemu-efi-aarch64 versioning=deb registryUrl=https://deb.debian.org/debian?suite=trixie&components=main&binaryArch=all
QEMU_EFI_AARCH64_VERSION="2025.02-8+deb13u1"
# Not managed by Renovate: update together with QEMU_EFI_AARCH64_VERSION.
# Keep this comment directly below the version: it also stops the shared Renovate
# regex from capturing the SHA-256 as a digest of the version.
QEMU_EFI_AARCH64_SHA256="a00b2411a79c8aeafd95a7c868ac3cd1aab592f1af9fff965f02d81a625276ed"
QEMU_EFI_AARCH64_SNAPSHOT_TIMESTAMP="20260104T203950Z"
QEMU_EFI_AARCH64_POOL_PATH="pool/main/e/edk2/qemu-efi-aarch64_${QEMU_EFI_AARCH64_VERSION}_all.deb"

DEB_PATH="${cache_dir}/qemu-efi-aarch64_all.deb"
UEFI_DIR="${cache_dir}/qemu-uefi"
UEFI_STAMP_PATH="${UEFI_DIR}/.qemu-efi-aarch64.sha256"
UEFI_FIRMWARE_PATH="${UEFI_DIR}/usr/share/qemu-efi-aarch64/QEMU_EFI.fd"
AAVMF_CODE_PATH="${UEFI_DIR}/usr/share/AAVMF/AAVMF_CODE.no-secboot.fd"
AAVMF_VARS_PATH="${UEFI_DIR}/usr/share/AAVMF/AAVMF_VARS.fd"

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

# Ensure cache directory exists before downloading
mkdir -p "${cache_dir}"

if [ -f "${DEB_PATH}" ] && [ "$(sha256_of "${DEB_PATH}")" = "${QEMU_EFI_AARCH64_SHA256}" ]; then
	echo "qemu-efi-aarch64 ${QEMU_EFI_AARCH64_VERSION} already exists, skipping download"
else
	downloaded="false"
	for download_url in \
		"https://deb.debian.org/debian/${QEMU_EFI_AARCH64_POOL_PATH}" \
		"https://snapshot.debian.org/archive/debian/${QEMU_EFI_AARCH64_SNAPSHOT_TIMESTAMP}/${QEMU_EFI_AARCH64_POOL_PATH}"; do
		echo "Downloading ${download_url}"
		if curl -fL -o "${DEB_PATH}.tmp" "${download_url}" &&
			[ "$(sha256_of "${DEB_PATH}.tmp")" = "${QEMU_EFI_AARCH64_SHA256}" ]; then
			mv "${DEB_PATH}.tmp" "${DEB_PATH}"
			downloaded="true"
			break
		fi
		echo "Download from ${download_url} failed or did not match the pinned SHA-256" >&2
	done
	rm -f "${DEB_PATH}.tmp"
	if [ "${downloaded}" != "true" ]; then
		echo "Failed to download qemu-efi-aarch64 ${QEMU_EFI_AARCH64_VERSION}; if every source failed the SHA-256 check, the pin is probably stale (see build/packer/linux/ubuntu/README.md)" >&2
		exit 1
	fi
fi

if [ ! -f "${UEFI_FIRMWARE_PATH}" ] || [ ! -f "${AAVMF_CODE_PATH}" ] || [ ! -f "${AAVMF_VARS_PATH}" ] ||
	[ "$(cat "${UEFI_STAMP_PATH}" 2>/dev/null)" != "${QEMU_EFI_AARCH64_SHA256}" ]; then
	echo "Extracting qemu-efi-aarch64 package contents"
	tmp_extract_dir="$(mktemp -d "${cache_dir}/qemu-uefi.tmp.XXXXXX")"
	tmp_rootfs_dir="${tmp_extract_dir}/rootfs"
	trap 'rm -rf "${tmp_extract_dir}"' EXIT

	if command -v dpkg-deb >/dev/null 2>&1; then
		mkdir -p "${tmp_rootfs_dir}"
		dpkg-deb -x "${DEB_PATH}" "${tmp_rootfs_dir}"
	else
		tmp_archive_dir="${tmp_extract_dir}/deb"
		mkdir -p "${tmp_archive_dir}" "${tmp_rootfs_dir}"
		(
			cd "${tmp_archive_dir}"
			ar x "${DEB_PATH}"
		)

		data_archive="$(find "${tmp_archive_dir}" -maxdepth 1 -type f -name 'data.tar.*' | head -n 1)"
		if [ -z "${data_archive}" ]; then
			echo "Failed to locate data archive inside ${DEB_PATH}" >&2
			exit 1
		fi
		tar -xf "${data_archive}" -C "${tmp_rootfs_dir}"
	fi

	printf '%s\n' "${QEMU_EFI_AARCH64_SHA256}" >"${tmp_rootfs_dir}/.qemu-efi-aarch64.sha256"
	rm -rf "${UEFI_DIR}"
	mv "${tmp_rootfs_dir}" "${UEFI_DIR}"
	rm -rf "${tmp_extract_dir}"
	trap - EXIT
else
	echo "qemu-uefi firmware already exists, skipping extraction"
fi

if [ ! -f "${UEFI_FIRMWARE_PATH}" ]; then
	echo "Expected ARM64 UEFI firmware was not found at ${UEFI_FIRMWARE_PATH}" >&2
	exit 1
fi

if [ ! -f "${AAVMF_CODE_PATH}" ]; then
	echo "Expected ARM64 AAVMF code firmware was not found at ${AAVMF_CODE_PATH}" >&2
	exit 1
fi

if [ ! -f "${AAVMF_VARS_PATH}" ]; then
	echo "Expected ARM64 AAVMF vars firmware was not found at ${AAVMF_VARS_PATH}" >&2
	exit 1
fi
