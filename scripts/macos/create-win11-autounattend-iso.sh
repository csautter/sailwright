#!/usr/bin/env bash

set -e

SCRIPT_DIR=$(
	cd "$(dirname "$0")" || exit 1
	pwd -P
)

host_os="$(uname -s)"
if [[ "$host_os" == "Darwin" ]]; then
	default_app_data_dir="$HOME/Library/Application Support/dev-alchemy"
elif [[ "$host_os" == "Linux" ]]; then
	default_app_data_dir="${XDG_DATA_HOME:-$HOME/.local/share}/dev-alchemy"
else
	echo "Unsupported host OS for Windows ARM64 unattended ISO creation: $host_os" >&2
	exit 1
fi

app_data_dir="${DEV_ALCHEMY_APP_DATA_DIR:-$default_app_data_dir}"
cache_dir="${DEV_ALCHEMY_CACHE_DIR:-$app_data_dir/cache}"
export DEV_ALCHEMY_APP_DATA_DIR="$app_data_dir"
export DEV_ALCHEMY_CACHE_DIR="$cache_dir"

vendor_dir="$cache_dir/windows"
iso_dir="$cache_dir/windows11/iso"
autounattend_xml_path="$SCRIPT_DIR/../../build/packer/windows/qemu-arm64/autounattend.xml"
windows_source_iso_path="$iso_dir/win11_english_arm64.iso"
windows_target_iso_path="$iso_dir/Win11_ARM64_Unattended.iso"
# One-time rename of the pre-versioning file name; mirrors migrateLegacyWindows11ISO in pkg/build/win11iso.go.
legacy_windows_source_iso_path="$iso_dir/win11_25h2_english_arm64.iso"
if [[ ! -f "$windows_source_iso_path" && -f "$legacy_windows_source_iso_path" ]]; then
  echo "Renaming legacy Windows 11 ISO $legacy_windows_source_iso_path to $windows_source_iso_path"
  mv "$legacy_windows_source_iso_path" "$windows_source_iso_path"
fi
windows_extract_dir="$vendor_dir/win11_arm64_iso_files"

if [[ ! -f "$windows_source_iso_path" ]]; then
	echo "Windows 11 ARM64 source ISO is missing: $windows_source_iso_path" >&2
	exit 1
fi
if ! command -v xorriso >/dev/null 2>&1; then
	echo "xorriso is required to create the Windows 11 ARM64 unattended ISO." >&2
	exit 1
fi

mounted_source_iso_dir=""
cleanup_mounted_source_iso() {
	if [[ -n "$mounted_source_iso_dir" ]]; then
		hdiutil detach "$mounted_source_iso_dir" >/dev/null 2>&1 || true
	fi
}
trap cleanup_mounted_source_iso EXIT

tar_supports_libarchive() {
	local tar_path="$1"
	local version_output

	version_output="$("$tar_path" --version 2>&1 || true)"
	[[ "$version_output" == *bsdtar* || "$version_output" == *libarchive* ]]
}

find_extracted_efisys_bin() {
	find "$windows_extract_dir" -type f -ipath "*/efi/microsoft/boot/efisys.bin" -print -quit
}

find_extracted_sources_dir() {
	find "$windows_extract_dir" -type d -iname "sources" -print -quit
}

windows_source_extraction_is_complete() {
	[[ -n "$(find_extracted_efisys_bin)" && -n "$(find_extracted_sources_dir)" ]]
}

reset_windows_extract_dir() {
	rm -rf "$windows_extract_dir"
	mkdir -p "$windows_extract_dir"
}

try_extract_windows_source_iso() {
	local label="$1"

	shift
	reset_windows_extract_dir
	echo "Extracting Windows 11 ARM64 source ISO with $label..."
	if "$@"; then
		chmod -R u+w "$windows_extract_dir"
		if windows_source_extraction_is_complete; then
			return 0
		fi
		echo "Extraction with $label did not expose the Windows setup files; trying another extractor." >&2
	else
		echo "Extraction with $label failed; trying another extractor." >&2
	fi

	return 1
}

extract_with_hdiutil() {
	local mount_dir="$vendor_dir/win11_arm64_source_iso"

	rm -rf "$mount_dir"
	mkdir -p "$mount_dir"
	mounted_source_iso_dir="$mount_dir"
	hdiutil attach -readonly -nobrowse -mountpoint "$mount_dir" "$windows_source_iso_path" >/dev/null
	cp -R "$mount_dir"/. "$windows_extract_dir"
	hdiutil detach "$mount_dir" >/dev/null
	mounted_source_iso_dir=""
}

extract_windows_source_iso() {
	if command -v 7z >/dev/null 2>&1; then
		if try_extract_windows_source_iso "7z" 7z x -y -o"$windows_extract_dir" "$windows_source_iso_path"; then
			return 0
		fi
	elif command -v 7zz >/dev/null 2>&1; then
		if try_extract_windows_source_iso "7zz" 7zz x -y -o"$windows_extract_dir" "$windows_source_iso_path"; then
			return 0
		fi
	fi

	if command -v bsdtar >/dev/null 2>&1; then
		if try_extract_windows_source_iso "bsdtar" bsdtar -C "$windows_extract_dir" -xf "$windows_source_iso_path"; then
			return 0
		fi
	fi

	if command -v tar >/dev/null 2>&1 && tar_supports_libarchive "$(command -v tar)"; then
		if try_extract_windows_source_iso "libarchive tar" tar -C "$windows_extract_dir" -xf "$windows_source_iso_path"; then
			return 0
		fi
	fi

	if [[ "$host_os" == "Darwin" ]] && command -v hdiutil >/dev/null 2>&1; then
		if try_extract_windows_source_iso "hdiutil" extract_with_hdiutil; then
			return 0
		fi
	fi

	echo "Unable to extract the Windows 11 ARM64 source ISO." >&2
	echo "Install 7-Zip (for example: sudo apt-get install 7zip or p7zip-full) or libarchive tools, then retry." >&2
	return 1
}

mkdir -p "$vendor_dir" "$iso_dir"
rm -f "$vendor_dir/autounattend.iso"
xorriso -as mkisofs \
	-V autounattend \
	-o "$vendor_dir/autounattend.iso" \
	"$SCRIPT_DIR/../../build/packer/windows/qemu-arm64/"

rm -rf "$windows_extract_dir"
mkdir -p "$windows_extract_dir"

extract_windows_source_iso

efisys_bin_path="$(find_extracted_efisys_bin)"
sources_dir="$(find_extracted_sources_dir)"
if [[ -z "$efisys_bin_path" || -z "$sources_dir" ]]; then
	echo "Windows 11 ARM64 source ISO extraction appears incomplete." >&2
	echo "Expected to find both sources/ and efi/microsoft/boot/efisys.bin under $windows_extract_dir." >&2
	exit 1
fi

cp "$autounattend_xml_path" "$windows_extract_dir/autounattend.xml"
ls "$windows_extract_dir/autounattend.xml"

rm -f "$windows_target_iso_path"

source_size_kb=$(du -sk "$windows_extract_dir" | awk '{print $1}')
required_space_kb=$((source_size_kb * 15 / 10))
available_space_kb=$(df -k "$iso_dir" | awk 'NR==2 {print $4}')
echo "Disk space check:"
echo "  Source files: $((source_size_kb / 1024)) MB"
echo "  Required: $((required_space_kb / 1024)) MB"
echo "  Available: $((available_space_kb / 1024)) MB"
if [[ "$available_space_kb" -lt "$required_space_kb" ]]; then
	echo "ERROR: Not enough disk space to create the ISO image." >&2
	exit 1
fi

xorriso -as mkisofs \
	-iso-level 3 \
	-volid "CCCOMA_A64FRE_EN_US_DV9" \
	-eltorito-alt-boot \
	-e --interval:appended_partition_2:all:: \
	-no-emul-boot \
	-isohybrid-gpt-basdat \
	-append_partition 2 0xef "$efisys_bin_path" \
	-o "$windows_target_iso_path" \
	"$windows_extract_dir"

echo "Created $windows_target_iso_path"
rm -rf "$windows_extract_dir"
