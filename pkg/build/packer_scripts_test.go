package build

import (
	"path/filepath"
	"strings"
	"testing"
)

const (
	ubuntuQemuScriptPath         = "build/packer/linux/ubuntu/linux-ubuntu-qemu.sh"
	ubuntuMacOSQemuScriptPath    = "build/packer/linux/ubuntu/linux-ubuntu-on-macos.sh"
	windowsQemuScriptPath        = "build/packer/windows/windows11-qemu.sh"
	windowsAutounattendIsoPath   = "scripts/macos/create-win11-autounattend-iso.sh"
	linuxDependencyInstallerPath = "scripts/linux/sailwright-install-dependencies.sh"
)

func TestUbuntuQemuWrapperScriptsUseSharedTemplate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		scriptPath string
		hostOS     string
	}{
		{scriptPath: ubuntuMacOSQemuScriptPath, hostOS: "darwin"},
		{scriptPath: ubuntuQemuScriptPath, hostOS: "linux"},
	} {
		t.Run(filepath.Base(tc.scriptPath), func(t *testing.T) {
			t.Parallel()

			got := readRepoFile(t, tc.scriptPath)
			if !strings.Contains(got, `packer_file="`+ubuntuQemuTemplatePath+`"`) {
				t.Errorf("expected script %q to use the shared QEMU Packer template", tc.scriptPath)
			}
			if !strings.Contains(got, `-var "host_os=`+tc.hostOS+`"`) {
				t.Errorf("expected script %q to pass host_os=%s", tc.scriptPath, tc.hostOS)
			}
			if !strings.Contains(got, `-var "host_arch=$host_arch"`) {
				t.Errorf("expected script %q to pass the detected host architecture", tc.scriptPath)
			}
			if !strings.Contains(got, `-var "artifact_output_path=$artifact_output_path"`) {
				t.Errorf("expected script %q to pass an optional staged artifact output path", tc.scriptPath)
			}
		})
	}
}

func TestUbuntuMacOSQemuWrapperSupportsPackerStartOnlyProbe(t *testing.T) {
	t.Parallel()

	got := readRepoFile(t, ubuntuMacOSQemuScriptPath)
	assertContainsAll(t, ubuntuMacOSQemuScriptPath, got,
		`DEV_ALCHEMY_PACKER_START_ONLY`,
		`--packer-start-only`,
		`run_packer_build_start_only`,
		`Using isolated cache directory for start-only Packer probe`,
		`mktemp -d "/tmp/da-pc.XXXXXX"`,
		`build_output_dir="$effective_cache_dir/o"`,
		`-var "cache_dir=$effective_cache_dir"`,
	)
	if strings.Contains(got, `.XXXXXX.log`) {
		t.Errorf("expected %q not to use mktemp templates with a suffix after XXXXXX; BSD mktemp does not support them", ubuntuMacOSQemuScriptPath)
	}
}

func TestWindowsQemuScriptPassesSharedTemplateVariables(t *testing.T) {
	t.Parallel()

	assertContainsAll(t, windowsQemuScriptPath, readRepoFile(t, windowsQemuScriptPath),
		`packer_file="`+windowsQemuTemplatePath+`"`,
		`-var "host_os=${host_os}"`,
		`-var "host_arch=${host_arch}"`,
		`-var "use_hardware_acceleration=${use_hardware_acceleration}"`,
		`-var "cache_dir=${effective_cache_dir}"`,
		`-var "artifact_output_path=${artifact_output_path}"`,
		`DEV_ALCHEMY_PACKER_START_ONLY`,
		`--packer-start-only`,
		`run_packer_build_start_only`,
		`Using isolated cache directory for start-only Packer probe`,
	)
}

func TestWindowsQemuScriptPreparesVirtioIsoAndArm64Firmware(t *testing.T) {
	t.Parallel()

	got := readRepoFile(t, windowsQemuScriptPath)
	virtioDownload := `bash "${project_root}/scripts/macos/download-virtio-win-iso.sh"`
	if !strings.Contains(got, virtioDownload) {
		t.Errorf("expected script %q to download the virtio-win ISO", windowsQemuScriptPath)
	}

	arm64Block, ok := textBetween(got, `if [[ "$arch" == "arm64" ]]; then`, `echo "Creating QCOW2 disk image..."`)
	if !ok {
		t.Fatalf("failed to locate ARM64-specific block in script %q", windowsQemuScriptPath)
	}
	if strings.Contains(arm64Block, virtioDownload) {
		t.Errorf("expected script %q to download virtio-win outside the ARM64-only block", windowsQemuScriptPath)
	}
	assertContainsAll(t, "ARM64-specific block in "+windowsQemuScriptPath, arm64Block,
		`qemu-uefi/usr/share/AAVMF/AAVMF_CODE.no-secboot.fd`,
		`qemu-uefi/usr/share/AAVMF/AAVMF_VARS.fd`,
	)
}

func TestWindowsQemuHostWrapperScriptsDelegateToSharedScript(t *testing.T) {
	t.Parallel()

	for _, scriptPath := range []string{
		"build/packer/windows/windows11-on-macos.sh",
		"build/packer/windows/windows11-on-linux.sh",
	} {
		t.Run(filepath.Base(scriptPath), func(t *testing.T) {
			t.Parallel()

			assertContainsAll(t, "wrapper script "+scriptPath, readRepoFile(t, scriptPath), filepath.Base(windowsQemuScriptPath))
		})
	}
}

func TestWindowsArm64UnattendIsoScriptUsesUDFCapableExtraction(t *testing.T) {
	t.Parallel()

	got := readRepoFile(t, windowsAutounattendIsoPath)
	assertContainsAll(t, windowsAutounattendIsoPath, got,
		`extract_windows_source_iso`,
		`try_extract_windows_source_iso`,
		`windows_source_extraction_is_complete`,
		`command -v 7z`,
		`command -v 7zz`,
		`command -v bsdtar`,
		`tar_supports_libarchive`,
		`hdiutil attach -readonly`,
		`find_extracted_efisys_bin`,
		`Windows 11 ARM64 source ISO extraction appears incomplete.`,
		`-append_partition 2 0xef "$efisys_bin_path"`,
	)
	if strings.Contains(got, `xorriso -osirrox on`) {
		t.Errorf("expected %q not to use xorriso for source ISO extraction; Windows media is UDF", windowsAutounattendIsoPath)
	}
}

func TestLinuxDependencyInstallerIncludesWindowsIsoExtractionTools(t *testing.T) {
	t.Parallel()

	assertContainsAll(t, linuxDependencyInstallerPath, readRepoFile(t, linuxDependencyInstallerPath),
		`apt-cache show 7zip`,
		`p7zip-full`,
		`libarchive-tools`,
		`xorriso`,
	)
}
