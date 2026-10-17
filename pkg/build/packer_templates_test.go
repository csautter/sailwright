package build

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var ubuntuQemuTemplatePaths = []string{
	"build/packer/linux/ubuntu/linux-ubuntu-qemu.pkr.hcl",
}

var windowsQemuTemplatePaths = []string{
	"build/packer/windows/windows11-qemu.pkr.hcl",
}

func TestUbuntuPackerTemplatesUseCallerSuppliedISOChecksum(t *testing.T) {
	t.Parallel()

	for _, templatePath := range []string{
		"build/packer/linux/ubuntu/linux-ubuntu-qemu.pkr.hcl",
		"build/packer/linux/ubuntu/linux-ubuntu-hyperv.pkr.hcl",
	} {
		templatePath := templatePath
		t.Run(filepath.Base(templatePath), func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(repoPath(t, templatePath))
			if err != nil {
				t.Fatalf("failed to read template %q: %v", templatePath, err)
			}

			got := string(content)
			if strings.Contains(got, "c3514bf0056180d09376462a7a1b4f213c1d6e8ea67fae5c25099c6fd3d8274b") {
				t.Fatalf("template %q still contains the Ubuntu 24.04.3 amd64 checksum", templatePath)
			}
			if !strings.Contains(got, `variable "iso_checksum"`) {
				t.Fatalf("expected template %q to declare iso_checksum", templatePath)
			}
			if !containsCollapsedAssignment(got, "ubuntu_iso_checksum", "var.iso_checksum") {
				t.Fatalf("expected template %q to use the caller-supplied iso_checksum", templatePath)
			}
		})
	}
}

func TestUbuntuLiveServerPinsStayInSync(t *testing.T) {
	t.Parallel()

	for _, scriptPath := range []string{
		"build/packer/linux/ubuntu/linux-ubuntu-qemu.sh",
		"build/packer/linux/ubuntu/linux-ubuntu-on-macos.sh",
	} {
		scriptPath := scriptPath
		t.Run(filepath.Base(scriptPath), func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(repoPath(t, scriptPath))
			if err != nil {
				t.Fatalf("failed to read script %q: %v", scriptPath, err)
			}

			got := string(content)
			for _, want := range []string{
				`UBUNTU_LIVE_SERVER_AMD64_VERSION="` + ubuntuLiveServerAMD64Version + `"`,
				`UBUNTU_LIVE_SERVER_AMD64_SHA256="` + ubuntuLiveServerAMD64SHA256 + `"`,
				`UBUNTU_LIVE_SERVER_ARM64_VERSION="` + ubuntuLiveServerArm64Version + `"`,
				`UBUNTU_LIVE_SERVER_ARM64_SHA256="` + ubuntuLiveServerArm64SHA256 + `"`,
				`-var "iso_checksum=sha256:$iso_checksum"`,
			} {
				if !strings.Contains(got, want) {
					t.Fatalf("expected script %q to contain %q", scriptPath, want)
				}
			}
		})
	}

	hypervTemplatePath := "build/packer/linux/ubuntu/linux-ubuntu-hyperv.pkr.hcl"
	hypervTemplate, err := os.ReadFile(repoPath(t, hypervTemplatePath))
	if err != nil {
		t.Fatalf("failed to read template %q: %v", hypervTemplatePath, err)
	}
	if !strings.Contains(string(hypervTemplate), `default = "`+ubuntuLiveServerAMD64Version+`"`) {
		t.Fatalf("expected %q to default to Ubuntu %s", hypervTemplatePath, ubuntuLiveServerAMD64Version)
	}

	for _, filePath := range []string{
		".github/workflows/test-build-linux.yml",
		".github/workflows/test-build-macos.yml",
		"build/packer/linux/ubuntu/README.md",
	} {
		filePath := filePath
		t.Run(filepath.Base(filePath)+" static ISO references", func(t *testing.T) {
			t.Parallel()
			assertUbuntuLiveServerISOReferencesUsePinnedVersions(t, filePath)
		})
	}
}

func TestWindowsQemuTemplateIsSharedAcrossMacOSAndLinux(t *testing.T) {
	t.Parallel()

	for _, templatePath := range windowsQemuTemplatePaths {
		templatePath := templatePath
		t.Run(filepath.Base(templatePath), func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(repoPath(t, templatePath))
			if err != nil {
				t.Fatalf("failed to read template %q: %v", templatePath, err)
			}

			got := string(content)
			for _, want := range []string{
				`variable "host_os"`,
				`variable "host_arch"`,
				`variable "use_hardware_acceleration"`,
				`host_is_linux`,
				`host_is_darwin`,
				`amd64_accelerator`,
				`arm64_accelerator`,
				`qemu_display`,
				`variable "artifact_output_path"`,
				`win11_qcow2       = var.artifact_output_path != "" ? var.artifact_output_path`,
			} {
				if !strings.Contains(got, want) {
					t.Fatalf("expected shared Windows QEMU template %q to contain %q", templatePath, want)
				}
			}
		})
	}
}

func TestWindowsArm64QemuUsesWritableEfiVarsAndAccelerationAwareBootOrder(t *testing.T) {
	t.Parallel()

	for _, templatePath := range windowsQemuTemplatePaths {
		templatePath := templatePath
		t.Run(filepath.Base(templatePath), func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(repoPath(t, templatePath))
			if err != nil {
				t.Fatalf("failed to read template %q: %v", templatePath, err)
			}

			got := string(content)
			arm64Args, ok := textBetween(got, `"arm64" = [`, "]\n  }")
			if !ok {
				t.Fatalf("failed to locate arm64 QEMU args in %q", templatePath)
			}
			for _, want := range []string{
				`AAVMF_CODE.no-secboot.fd`,
				`AAVMF_VARS.fd`,
				`arm64_install_bootindex           = local.arm64_can_use_native_acceleration ? 1 : 0`,
				`arm64_disk_bootindex              = local.arm64_can_use_native_acceleration ? 0 : 1`,
				`file=${local.win11_uefi_code},if=pflash,unit=0,format=raw,readonly=on`,
				`file={{ .OutputDir }}/efivars.fd,if=pflash,unit=1,format=raw`,
				`drive=nvme0,serial=deadbeef,bootindex=${local.arm64_disk_bootindex}`,
				`drive=install,removable=true,bootindex=${local.arm64_install_bootindex}`,
				`efi_boot`,
				`efi_firmware_code`,
				`efi_firmware_vars`,
				`efi_drop_efivars`,
			} {
				if !strings.Contains(got, want) {
					t.Fatalf("expected Windows ARM64 QEMU template %q to contain %q", templatePath, want)
				}
			}
			if strings.Contains(arm64Args, `["-boot",`) {
				t.Fatalf("template %q uses unsupported ARM64 QEMU boot ordering through -boot", templatePath)
			}
			if strings.Contains(arm64Args, `QEMU_EFI.fd`) || strings.Contains(arm64Args, `["-bios",`) {
				t.Fatalf("template %q still boots ARM64 Windows with non-persistent -bios firmware", templatePath)
			}
		})
	}
}

func TestWindowsQemuScriptsUseSharedTemplateAndPins(t *testing.T) {
	t.Parallel()

	for _, scriptPath := range []string{
		"build/packer/windows/windows11-qemu.sh",
		"build/packer/windows/windows11-on-macos.sh",
		"build/packer/windows/windows11-on-linux.sh",
	} {
		scriptPath := scriptPath
		t.Run(filepath.Base(scriptPath), func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(repoPath(t, scriptPath))
			if err != nil {
				t.Fatalf("failed to read script %q: %v", scriptPath, err)
			}

			got := string(content)
			if scriptPath == "build/packer/windows/windows11-qemu.sh" {
				for _, want := range []string{
					`packer_file="build/packer/windows/windows11-qemu.pkr.hcl"`,
					`-var "host_os=${host_os}"`,
					`-var "host_arch=${host_arch}"`,
					`-var "use_hardware_acceleration=${use_hardware_acceleration}"`,
					`-var "cache_dir=${effective_cache_dir}"`,
					`-var "artifact_output_path=${artifact_output_path}"`,
					`DEV_ALCHEMY_PACKER_START_ONLY`,
					`--packer-start-only`,
					`run_packer_build_start_only`,
					`Using isolated cache directory for start-only Packer probe`,
				} {
					if !strings.Contains(got, want) {
						t.Fatalf("expected script %q to contain %q", scriptPath, want)
					}
				}

				virtioDownload := `bash "${project_root}/scripts/macos/download-virtio-win-iso.sh"`
				if !strings.Contains(got, virtioDownload) {
					t.Fatalf("expected script %q to download the virtio-win ISO", scriptPath)
				}
				arm64Block, ok := textBetween(got, `if [[ "$arch" == "arm64" ]]; then`, `echo "Creating QCOW2 disk image..."`)
				if !ok {
					t.Fatalf("failed to locate ARM64-specific block in script %q", scriptPath)
				}
				if strings.Contains(arm64Block, virtioDownload) {
					t.Fatalf("expected script %q to download virtio-win outside the ARM64-only block", scriptPath)
				}
				for _, want := range []string{
					`qemu-uefi/usr/share/AAVMF/AAVMF_CODE.no-secboot.fd`,
					`qemu-uefi/usr/share/AAVMF/AAVMF_VARS.fd`,
				} {
					if !strings.Contains(arm64Block, want) {
						t.Fatalf("expected ARM64-specific block in script %q to validate %q", scriptPath, want)
					}
				}
				return
			}

			if !strings.Contains(got, `windows11-qemu.sh`) {
				t.Fatalf("expected wrapper script %q to call windows11-qemu.sh", scriptPath)
			}
		})
	}

	virtioScriptPath := "scripts/macos/download-virtio-win-iso.sh"
	content, err := os.ReadFile(repoPath(t, virtioScriptPath))
	if err != nil {
		t.Fatalf("failed to read script %q: %v", virtioScriptPath, err)
	}
	if !strings.Contains(string(content), `VIRTIO_WIN_VERSION="`+virtioWinVersion+`"`) {
		t.Fatalf("expected %q to use virtio-win %s", virtioScriptPath, virtioWinVersion)
	}
	if !strings.Contains(string(content), `VIRTIO_WIN_SHA256="`+virtioWinSHA256+`"`) {
		t.Fatalf("expected %q to verify virtio-win checksum %s", virtioScriptPath, virtioWinSHA256)
	}
	for _, want := range []string{
		`verify_sha256 "$virtio_iso_path"`,
		`verify_sha256 "$tmp_path"`,
		`sha256sum -c -`,
		`shasum -a 256`,
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("expected %q to contain %q", virtioScriptPath, want)
		}
	}
}

func TestWindowsArm64UnattendIsoScriptUsesUDFCapableExtraction(t *testing.T) {
	t.Parallel()

	scriptPath := "scripts/macos/create-win11-autounattend-iso.sh"
	content, err := os.ReadFile(repoPath(t, scriptPath))
	if err != nil {
		t.Fatalf("failed to read script %q: %v", scriptPath, err)
	}

	got := string(content)
	for _, want := range []string{
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
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q to contain %q", scriptPath, want)
		}
	}

	if strings.Contains(got, `xorriso -osirrox on`) {
		t.Fatalf("expected %q not to use xorriso for source ISO extraction; Windows media is UDF", scriptPath)
	}
}

func TestLinuxDependencyInstallerIncludesWindowsIsoExtractionTools(t *testing.T) {
	t.Parallel()

	scriptPath := "scripts/linux/sailwright-install-dependencies.sh"
	content, err := os.ReadFile(repoPath(t, scriptPath))
	if err != nil {
		t.Fatalf("failed to read script %q: %v", scriptPath, err)
	}

	got := string(content)
	for _, want := range []string{
		`apt-cache show 7zip`,
		`p7zip-full`,
		`libarchive-tools`,
		`xorriso`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q to contain %q", scriptPath, want)
		}
	}
}

func TestWindowsQemuAmd64MountsVirtioIsoForQxlDriverInstall(t *testing.T) {
	t.Parallel()

	templatePath := "build/packer/windows/windows11-qemu.pkr.hcl"
	templateContent, err := os.ReadFile(repoPath(t, templatePath))
	if err != nil {
		t.Fatalf("failed to read template %q: %v", templatePath, err)
	}

	amd64Args, ok := textBetween(string(templateContent), `"amd64" = [`, `"arm64" = [`)
	if !ok {
		t.Fatalf("failed to locate amd64 QEMU args in %q", templatePath)
	}
	for _, want := range []string{
		`["-device", "usb-storage,drive=virtio-drivers,removable=true,bootindex=2"]`,
		`["-drive", "if=none,id=virtio-drivers,format=raw,media=cdrom,file=${local.win11_virtio_iso},readonly=true"]`,
	} {
		if !strings.Contains(amd64Args, want) {
			t.Fatalf("expected amd64 QEMU args in %q to contain %q", templatePath, want)
		}
	}

	autounattendPath := "build/packer/windows/qemu-amd64/autounattend.xml"
	autounattendContent, err := os.ReadFile(repoPath(t, autounattendPath))
	if err != nil {
		t.Fatalf("failed to read autounattend file %q: %v", autounattendPath, err)
	}
	got := string(autounattendContent)
	for _, want := range []string{
		`C:\QXLDriverInstall.log`,
		`Starting QXL DOD driver install/stage during first logon.`,
		`qxldod\w10\amd64\qxldod.inf`,
		`pnputil.exe /add-driver`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q to contain %q", autounattendPath, want)
		}
	}
}

var ubuntuLiveServerISOReferenceRE = regexp.MustCompile(`ubuntu-([0-9]{2}\.[0-9]{2}(?:\.[0-9]+)?)-live-server-(amd64|arm64)\.iso`)

func assertUbuntuLiveServerISOReferencesUsePinnedVersions(t *testing.T, filePath string) {
	t.Helper()

	content, err := os.ReadFile(repoPath(t, filePath))
	if err != nil {
		t.Fatalf("failed to read %q: %v", filePath, err)
	}

	matches := ubuntuLiveServerISOReferenceRE.FindAllStringSubmatch(string(content), -1)
	if len(matches) == 0 {
		t.Fatalf("expected %q to contain Ubuntu live-server ISO references", filePath)
	}

	for _, match := range matches {
		version, arch := match[1], match[2]
		wantVersion := ubuntuLiveServerAMD64Version
		if arch == "arm64" {
			wantVersion = ubuntuLiveServerArm64Version
		}
		if version != wantVersion {
			t.Fatalf("expected %q to use Ubuntu %s for %s ISO reference, found %q", filePath, wantVersion, arch, match[0])
		}
	}
}

func containsCollapsedAssignment(content, name, value string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.Join(strings.Fields(line), " ") == name+" = "+value {
			return true
		}
	}
	return false
}

func textBetween(content, start, end string) (string, bool) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	startIndex := strings.Index(content, start)
	if startIndex == -1 {
		return "", false
	}
	startIndex += len(start)
	endIndex := strings.Index(content[startIndex:], end)
	if endIndex == -1 {
		return "", false
	}
	return content[startIndex : startIndex+endIndex], true
}

func TestTextBetweenAcceptsWindowsLineEndings(t *testing.T) {
	t.Parallel()

	got, ok := textBetween("before\r\nstart\r\nwanted\r\nend\r\nafter", "start\n", "\nend")
	if !ok {
		t.Fatal("expected textBetween to find delimiters across CRLF line endings")
	}
	if got != "wanted" {
		t.Fatalf("expected extracted text to normalize line endings, got %q", got)
	}
}

func TestUbuntuPackerTemplatesQuoteShellLocalExportPaths(t *testing.T) {
	t.Parallel()

	for _, templatePath := range ubuntuQemuTemplatePaths {
		templatePath := templatePath
		t.Run(filepath.Base(templatePath), func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(repoPath(t, templatePath))
			if err != nil {
				t.Fatalf("failed to read template %q: %v", templatePath, err)
			}

			got := string(content)
			wantQuotedMkdir := "\"mkdir -p \\\"$(dirname \\\"${local.ubuntu_qcow2}\\\")\\\"\""
			wantQuotedCp := "\"cp \\\"${local.output_directory}\\\"/linux-ubuntu-${var.ubuntu_type}-packer-* \\\"${local.ubuntu_qcow2}\\\"\""
			oldUnquotedCp := "\"cp ${local.output_directory}/linux-ubuntu-${var.ubuntu_type}-packer-* ${local.cache_directory}/ubuntu/qemu-ubuntu-${var.ubuntu_type}-packer-${var.arch}.qcow2\""

			if !strings.Contains(got, wantQuotedMkdir) {
				t.Fatalf("expected template %q to quote the export directory creation command", templatePath)
			}
			if !strings.Contains(got, wantQuotedCp) {
				t.Fatalf("expected template %q to quote the QCOW2 export command", templatePath)
			}
			if strings.Contains(got, oldUnquotedCp) {
				t.Fatalf("template %q still contains the unquoted QCOW2 export command", templatePath)
			}
		})
	}
}

func TestQemuAutoinstallUsesBootCommandWithoutSubiquityRestart(t *testing.T) {
	t.Parallel()

	userDataPath := "build/packer/linux/ubuntu/cloud-init/qemu-server/user-data"
	userData, err := os.ReadFile(repoPath(t, userDataPath))
	if err != nil {
		t.Fatalf("failed to read %q: %v", userDataPath, err)
	}

	gotUserData := string(userData)
	for _, oldHack := range []string{
		"runcmd:",
		"/proc/cmdline",
		"snap restart subiquity",
	} {
		if strings.Contains(gotUserData, oldHack) {
			t.Fatalf("qemu server user-data still contains Subiquity restart hack %q", oldHack)
		}
	}

	for _, templatePath := range ubuntuQemuTemplatePaths {
		templatePath := templatePath
		t.Run(filepath.Base(templatePath), func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(repoPath(t, templatePath))
			if err != nil {
				t.Fatalf("failed to read template %q: %v", templatePath, err)
			}
			if !strings.Contains(string(content), "autoinstall ds=nocloud") {
				t.Fatalf("expected template %q to pass autoinstall on the kernel command line", templatePath)
			}
		})
	}
}

func TestQemuCloudInitConfiguresPersistentDHCPNetworking(t *testing.T) {
	t.Parallel()

	const explicitDHCPNetwork = `  network:
    version: 2
    ethernets:
      default:
        dhcp4: true
        dhcp6: true
        match:
          name: e*`

	for _, userDataPath := range []string{
		"build/packer/linux/ubuntu/cloud-init/qemu-server/user-data",
		"build/packer/linux/ubuntu/cloud-init/qemu-desktop/user-data",
	} {
		userDataPath := userDataPath
		t.Run(filepath.Base(filepath.Dir(userDataPath)), func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(repoPath(t, userDataPath))
			if err != nil {
				t.Fatalf("failed to read %q: %v", userDataPath, err)
			}
			if !strings.Contains(normalizeLineEndings(string(content)), explicitDHCPNetwork) {
				t.Fatalf("expected %q to configure persistent DHCP networking for QEMU interfaces", userDataPath)
			}
		})
	}
}

func TestQemuCloudInitExtendsInstallerBusctlTimeout(t *testing.T) {
	t.Parallel()

	for _, userDataPath := range []string{
		"build/packer/linux/ubuntu/cloud-init/qemu-server/user-data",
		"build/packer/linux/ubuntu/cloud-init/qemu-desktop/user-data",
	} {
		userDataPath := userDataPath
		t.Run(filepath.Base(filepath.Dir(userDataPath)), func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(repoPath(t, userDataPath))
			if err != nil {
				t.Fatalf("failed to read %q: %v", userDataPath, err)
			}

			got := string(content)
			for _, want := range []string{
				"early-commands:",
				"/usr/bin/busctl.dev-alchemy-original --timeout=120",
			} {
				if !strings.Contains(got, want) {
					t.Fatalf("expected %q to contain %q", userDataPath, want)
				}
			}
		})
	}
}

func TestArm64QemuBootOrderPrefersInstalledDiskAfterInstall(t *testing.T) {
	t.Parallel()

	for _, templatePath := range ubuntuQemuTemplatePaths {
		templatePath := templatePath
		t.Run(filepath.Base(templatePath), func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(repoPath(t, templatePath))
			if err != nil {
				t.Fatalf("failed to read template %q: %v", templatePath, err)
			}

			got := string(content)
			if !strings.Contains(got, "drive=disk,serial=deadbeef,bootindex=0") {
				t.Fatalf("expected template %q to prefer the installed ARM64 disk once it becomes bootable", templatePath)
			}
			if !strings.Contains(got, "drive=cdrom,bootindex=1") {
				t.Fatalf("expected template %q to keep the ARM64 installer ISO as the blank-disk fallback", templatePath)
			}
			if !strings.Contains(got, "efi_boot") {
				t.Fatalf("expected template %q to enable Packer EFI mode for ARM64 so the qemu builder does not inject -boot", templatePath)
			}
			if !strings.Contains(got, "AAVMF_CODE.no-secboot.fd") {
				t.Fatalf("expected template %q to attach ARM64 AAVMF code firmware", templatePath)
			}
			if !strings.Contains(got, "file={{ .OutputDir }}/efivars.fd,if=pflash,unit=1,format=raw") {
				t.Fatalf("expected template %q to attach Packer's writable ARM64 efivars copy", templatePath)
			}
			if strings.Contains(got, "[\"-boot\",") {
				t.Fatalf("template %q uses unsupported ARM64 QEMU boot ordering through -boot", templatePath)
			}
		})
	}
}

func TestQemuWrapperScriptsUseSharedTemplate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		scriptPath string
		hostOS     string
	}{
		{
			scriptPath: "build/packer/linux/ubuntu/linux-ubuntu-on-macos.sh",
			hostOS:     "darwin",
		},
		{
			scriptPath: "build/packer/linux/ubuntu/linux-ubuntu-qemu.sh",
			hostOS:     "linux",
		},
	} {
		tc := tc
		t.Run(filepath.Base(tc.scriptPath), func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(repoPath(t, tc.scriptPath))
			if err != nil {
				t.Fatalf("failed to read script %q: %v", tc.scriptPath, err)
			}

			got := string(content)
			if !strings.Contains(got, `packer_file="build/packer/linux/ubuntu/linux-ubuntu-qemu.pkr.hcl"`) {
				t.Fatalf("expected script %q to use the shared QEMU Packer template", tc.scriptPath)
			}
			if !strings.Contains(got, `-var "host_os=`+tc.hostOS+`"`) {
				t.Fatalf("expected script %q to pass host_os=%s", tc.scriptPath, tc.hostOS)
			}
			if !strings.Contains(got, `-var "host_arch=$host_arch"`) {
				t.Fatalf("expected script %q to pass the detected host architecture", tc.scriptPath)
			}
			if !strings.Contains(got, `-var "artifact_output_path=$artifact_output_path"`) {
				t.Fatalf("expected script %q to pass an optional staged artifact output path", tc.scriptPath)
			}
		})
	}
}

func TestMacOSQemuWrapperSupportsPackerStartOnlyProbe(t *testing.T) {
	t.Parallel()

	scriptPath := "build/packer/linux/ubuntu/linux-ubuntu-on-macos.sh"
	content, err := os.ReadFile(repoPath(t, scriptPath))
	if err != nil {
		t.Fatalf("failed to read script %q: %v", scriptPath, err)
	}

	got := string(content)
	for _, want := range []string{
		`DEV_ALCHEMY_PACKER_START_ONLY`,
		`--packer-start-only`,
		`run_packer_build_start_only`,
		`Using isolated cache directory for start-only Packer probe`,
		`mktemp -d "/tmp/da-pc.XXXXXX"`,
		`build_output_dir="$effective_cache_dir/o"`,
		`-var "cache_dir=$effective_cache_dir"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected macOS QEMU wrapper to contain %q", want)
		}
	}

	for _, oldLongPath := range []string{
		`dev-alchemy-packer-start-only-cache.XXXXXX`,
		`build_output_dir="$effective_cache_dir/qemu-out-ubuntu-${ubuntu_type}-${arch}"`,
		`.XXXXXX.log`,
	} {
		if strings.Contains(got, oldLongPath) {
			t.Fatalf("macOS QEMU start-only probe still contains long or non-portable path pattern %q", oldLongPath)
		}
	}
}

func TestMacOSWorkflowUsesStartOnlyProbeForUbuntuQemuBuilds(t *testing.T) {
	t.Parallel()

	workflowPath := ".github/workflows/test-build-macos.yml"
	content, err := os.ReadFile(repoPath(t, workflowPath))
	if err != nil {
		t.Fatalf("failed to read workflow %q: %v", workflowPath, err)
	}

	got := string(content)
	for _, testName := range []string{
		"TestBuildQemuUbuntuServerArm64OnMacos",
		"TestBuildQemuUbuntuServerAmd64OnMacos",
		"TestBuildQemuUbuntuDesktopArm64OnMacos",
		"TestBuildQemuUbuntuDesktopAmd64OnMacos",
	} {
		entry := workflowMatrixEntryForTest(t, got, testName)
		if !strings.Contains(entry, `packer_start_only: "true"`) {
			t.Fatalf("expected workflow matrix entry for %s to run as a start-only Packer probe", testName)
		}
	}

	if !strings.Contains(got, `DEV_ALCHEMY_PACKER_START_ONLY: ${{ matrix.packer_start_only }}`) {
		t.Fatal("expected workflow to pass packer_start_only through to the build step")
	}
	if !strings.Contains(got, `steps.packer_build.outcome == 'success' && matrix.packer_start_only != 'true'`) {
		t.Fatal("expected workflow to skip deploy smoke tests for start-only Packer probes")
	}
}

func TestLinuxWorkflowRunsWindowsQemuBuilds(t *testing.T) {
	t.Parallel()

	workflowPath := ".github/workflows/test-build-linux.yml"
	content, err := os.ReadFile(repoPath(t, workflowPath))
	if err != nil {
		t.Fatalf("failed to read workflow %q: %v", workflowPath, err)
	}

	got := string(content)
	for _, tc := range []struct {
		testName           string
		dependencyTestName string
		runsOn             string
		isoPath            string
		expectedEntries    int
	}{
		{
			testName:           "TestBuildQemuWindows11Amd64OnLinux",
			dependencyTestName: "TestIntegrationDependencyReconciliationQemuWindows11Amd64OnLinux",
			runsOn:             "runs_on: ubuntu-24.04",
			isoPath:            "./.dev-alchemy/cache/windows11/iso/win11_25h2_english_amd64.iso",
			expectedEntries:    1,
		},
		{
			testName:           "TestBuildQemuWindows11Arm64OnLinux",
			dependencyTestName: "TestIntegrationDependencyReconciliationQemuWindows11Arm64OnLinux",
			runsOn:             "runs_on: ubuntu-24.04-arm",
			isoPath:            "./.dev-alchemy/cache/windows11/iso/win11_25h2_english_arm64.iso",
			expectedEntries:    1,
		},
	} {
		entries := workflowMatrixEntriesForTest(t, got, tc.testName)
		if len(entries) != tc.expectedEntries {
			t.Fatalf("expected Linux workflow to include %d entries for %s, got %d", tc.expectedEntries, tc.testName, len(entries))
		}

		entry := workflowMatrixEntryForTest(t, got, tc.testName)
		for _, want := range []string{
			tc.dependencyTestName,
			tc.runsOn,
			tc.isoPath,
			"./.dev-alchemy/cache/utm/utm-guest-tools-latest.iso",
			"./.dev-alchemy/cache/windows/virtio-win.iso",
			`packer_start_only: "true"`,
			`packer_start_timeout: "180"`,
		} {
			if !strings.Contains(entry, want) {
				t.Fatalf("expected Linux workflow matrix entry for %s to contain %q", tc.testName, want)
			}
		}
		for _, entry := range entries {
			if !strings.Contains(entry, `packer_start_only: "true"`) {
				t.Fatalf("expected every Linux workflow matrix entry for %s to run as a start-only Packer probe", tc.testName)
			}
			if !strings.Contains(entry, `packer_start_timeout: "180"`) {
				t.Fatalf("expected every Linux workflow matrix entry for %s to set a Packer start timeout", tc.testName)
			}
		}
		if strings.Count(got, tc.isoPath) != tc.expectedEntries {
			t.Fatalf("expected Linux workflow to cache %s in %d entries", tc.isoPath, tc.expectedEntries)
		}
	}

	for _, testName := range []string{
		"TestBuildQemuUbuntuServerAmd64OnLinux",
		"TestBuildQemuUbuntuDesktopAmd64OnLinux",
		"TestBuildQemuUbuntuServerArm64OnLinux",
		"TestBuildQemuUbuntuDesktopArm64OnLinux",
	} {
		for _, entry := range workflowMatrixEntriesForTest(t, got, testName) {
			if !strings.Contains(entry, `packer_start_only: "false"`) {
				t.Fatalf("expected Linux workflow matrix entry for %s to run a full Packer build", testName)
			}
		}
	}

	arm64Entry := workflowMatrixEntryForTest(t, got, "TestBuildQemuWindows11Arm64OnLinux")
	if !strings.Contains(arm64Entry, "./.dev-alchemy/cache/qemu-efi-aarch64_all.deb") {
		t.Fatal("expected Windows 11 ARM64 Linux QEMU entry to cache qemu-efi-aarch64")
	}
	for _, want := range []string{
		`DEV_ALCHEMY_PACKER_START_ONLY: ${{ matrix.packer_start_only }}`,
		`DEV_ALCHEMY_PACKER_START_TIMEOUT: ${{ matrix.packer_start_timeout }}`,
		`name: packer-qemu-${{ matrix.go_test_name }}.vnc.mp4`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected Linux workflow to contain %q", want)
		}
	}
}

func TestLinuxWorkflowPublishesUbuntuArtifactsToGHCR(t *testing.T) {
	t.Parallel()

	workflowPath := ".github/workflows/test-build-linux.yml"
	content, err := os.ReadFile(repoPath(t, workflowPath))
	if err != nil {
		t.Fatalf("failed to read workflow %q: %v", workflowPath, err)
	}

	got := normalizeLineEndings(string(content))
	for _, want := range []string{
		`packages: write`,
		`branches: [main]`,
		`pkg/oci/**`,
		`OCI_UBUNTU_IMAGE: ghcr.io/${{ github.repository_owner }}/ubuntu-24`,
		`name: Upload Ubuntu OCI build artifact`,
		`ubuntu-oci-${{ matrix.ubuntu_type }}-${{ matrix.arch }}`,
		`include-hidden-files: true`,
		`publish-ubuntu-oci-artifacts:`,
		`github.event_name != 'pull_request'`,
		`permissions:` + "\n" + `      contents: read` + "\n" + `      packages: write`,
		`uses: actions/download-artifact@v7`,
		`name: Push Ubuntu OCI artifact to GHCR`,
		`matrix.vm_os == 'ubuntu'`,
		`github.ref == 'refs/heads/main'`,
		`reference="${OCI_UBUNTU_IMAGE}:${{ matrix.ubuntu_type }}-${{ matrix.arch }}-linux-build"`,
		`go run cmd/main.go push "${reference}"`,
		`--os ubuntu`,
		`--host-os linux`,
		`--engine qemu`,
		`--username "${GITHUB_ACTOR}"`,
		`--password-stdin`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected Linux workflow to contain %q", want)
		}
	}

	jobsIndex := strings.Index(got, "\njobs:")
	if jobsIndex == -1 {
		t.Fatal("expected Linux workflow to define jobs")
	}
	if strings.Contains(got[:jobsIndex], `packages: write`) {
		t.Fatal("expected Linux workflow not to grant packages: write at workflow scope")
	}

	if strings.Count(got, `name: Upload Ubuntu OCI build artifact`) != 1 {
		t.Fatalf("expected the Linux build job to upload Ubuntu OCI artifacts for publishing")
	}
	if strings.Count(got, `name: Push Ubuntu OCI artifact to GHCR`) != 1 {
		t.Fatalf("expected only the dedicated publish job to push Ubuntu OCI artifacts")
	}
	if strings.Contains(got, `steps.oci_push`) {
		t.Fatal("expected Linux build jobs not to depend on inline OCI publish steps")
	}
	if strings.Contains(got, `--os windows11`) {
		t.Fatal("expected Linux workflow not to publish Windows OCI artifacts")
	}

	for _, tc := range []struct {
		testName   string
		ubuntuType string
	}{
		{testName: "TestBuildQemuUbuntuServerAmd64OnLinux", ubuntuType: "server"},
		{testName: "TestBuildQemuUbuntuDesktopAmd64OnLinux", ubuntuType: "desktop"},
		{testName: "TestBuildQemuUbuntuServerArm64OnLinux", ubuntuType: "server"},
		{testName: "TestBuildQemuUbuntuDesktopArm64OnLinux", ubuntuType: "desktop"},
	} {
		for _, entry := range workflowMatrixEntriesForTest(t, got, tc.testName) {
			for _, want := range []string{
				`vm_os: ubuntu`,
				`ubuntu_type: ` + tc.ubuntuType,
				`packer_start_only: "false"`,
			} {
				if !strings.Contains(entry, want) {
					t.Fatalf("expected Linux workflow matrix entry for %s to contain %q", tc.testName, want)
				}
			}
		}
	}

	for _, testName := range []string{
		"TestBuildQemuWindows11Amd64OnLinux",
		"TestBuildQemuWindows11Arm64OnLinux",
	} {
		for _, entry := range workflowMatrixEntriesForTest(t, got, testName) {
			if !strings.Contains(entry, `vm_os: windows11`) {
				t.Fatalf("expected Linux workflow matrix entry for %s to mark Windows VM OS", testName)
			}
		}
	}
}

func TestMacOSWorkflowDefaultsToGitHubHostedRunnersWithTartOptIn(t *testing.T) {
	t.Parallel()

	workflowPath := ".github/workflows/test-build-macos.yml"
	content, err := os.ReadFile(repoPath(t, workflowPath))
	if err != nil {
		t.Fatalf("failed to read workflow %q: %v", workflowPath, err)
	}

	got := string(content)
	runnerExpression := `${{ vars.USE_TART_MACOS_RUNNERS == 'true' && matrix.tart_runs_on || matrix.runs_on }}`
	for _, want := range []string{
		`runs-on: ` + runnerExpression,
		`runner_os ` + runnerExpression,
		`USE_TART_MACOS_RUNNERS: ${{ vars.USE_TART_MACOS_RUNNERS == 'true' && 'true' || 'false' }}`,
		`uses: actions/setup-go@v6`,
		`go-version-file: go.mod`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected macOS workflow to contain %q", want)
		}
	}

	for _, testName := range []string{
		"TestBuildQemuWindows11Arm64OnMacos",
		"TestBuildQemuWindows11Amd64OnMacos",
		"TestBuildQemuUbuntuServerArm64OnMacos",
		"TestBuildQemuUbuntuServerAmd64OnMacos",
		"TestBuildQemuUbuntuDesktopArm64OnMacos",
		"TestBuildQemuUbuntuDesktopAmd64OnMacos",
	} {
		entry := workflowMatrixEntryForTest(t, got, testName)
		if !strings.Contains(entry, "runs_on: macos-26") {
			t.Fatalf("expected workflow matrix entry for %s to default to the GitHub-hosted macos-26 runner", testName)
		}
		if !strings.Contains(entry, "tart_runs_on: macos-26-tart") {
			t.Fatalf("expected workflow matrix entry for %s to keep the Tart runner opt-in label", testName)
		}
	}

	setupGoIndex := strings.Index(got, `uses: actions/setup-go@v6`)
	installDepsIndex := strings.Index(got, `name: Install dependencies macos`)
	if setupGoIndex == -1 || installDepsIndex == -1 || setupGoIndex > installDepsIndex {
		t.Fatal("expected macOS workflow to set up the Go version from go.mod before installing dependencies")
	}
}

func workflowMatrixEntryForTest(t *testing.T, workflow string, testName string) string {
	t.Helper()

	return workflowMatrixEntriesForTest(t, workflow, testName)[0]
}

func workflowMatrixEntriesForTest(t *testing.T, workflow string, testName string) []string {
	t.Helper()

	testMarker := "go_test_name: " + testName
	offset := 0
	var entries []string

	for {
		relativeStart := strings.Index(workflow[offset:], testMarker)
		if relativeStart == -1 {
			break
		}
		start := offset + relativeStart
		entryStart := strings.LastIndex(workflow[:start], "\n          - ")
		if entryStart == -1 {
			t.Fatalf("failed to find start of workflow matrix entry for %s", testName)
		}
		entryStart++

		nextEntry := strings.Index(workflow[start:], "\n          - ")
		if nextEntry == -1 {
			entries = append(entries, workflow[entryStart:])
		} else {
			entries = append(entries, workflow[entryStart:start+nextEntry])
		}
		offset = start + len(testMarker)
	}

	if len(entries) == 0 {
		t.Fatalf("failed to find workflow matrix entry for %s", testName)
	}
	return entries
}

func normalizeLineEndings(content string) string {
	return strings.ReplaceAll(content, "\r\n", "\n")
}

func repoPath(t *testing.T, relPath string) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine test file path")
	}

	return filepath.Join(filepath.Dir(currentFile), "..", "..", relPath)
}
