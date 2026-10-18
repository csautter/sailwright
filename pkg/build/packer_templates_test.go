package build

import (
	"path/filepath"
	"strings"
	"testing"
)

const (
	ubuntuQemuTemplatePath   = "build/packer/linux/ubuntu/linux-ubuntu-qemu.pkr.hcl"
	ubuntuHypervTemplatePath = "build/packer/linux/ubuntu/linux-ubuntu-hyperv.pkr.hcl"
	windowsQemuTemplatePath  = "build/packer/windows/windows11-qemu.pkr.hcl"
)

func TestUbuntuPackerTemplatesUseCallerSuppliedISOChecksum(t *testing.T) {
	t.Parallel()

	for _, templatePath := range []string{ubuntuQemuTemplatePath, ubuntuHypervTemplatePath} {
		t.Run(filepath.Base(templatePath), func(t *testing.T) {
			t.Parallel()

			got := readRepoFile(t, templatePath)
			assertContainsAll(t, templatePath, got, `variable "iso_checksum"`)
			if !containsCollapsedAssignment(got, "ubuntu_iso_checksum", "var.iso_checksum") {
				t.Errorf("expected template %q to use the caller-supplied iso_checksum", templatePath)
			}
		})
	}
}

func TestUbuntuQemuTemplatePassesAutoinstallOnKernelCommandLine(t *testing.T) {
	t.Parallel()

	assertContainsAll(t, ubuntuQemuTemplatePath, readRepoFile(t, ubuntuQemuTemplatePath), "autoinstall ds=nocloud")
}

func TestUbuntuQemuTemplateQuotesShellLocalExportPaths(t *testing.T) {
	t.Parallel()

	got := readRepoFile(t, ubuntuQemuTemplatePath)
	if !strings.Contains(got, "\"mkdir -p \\\"$(dirname \\\"${local.ubuntu_qcow2}\\\")\\\"\"") {
		t.Errorf("expected template %q to quote the export directory creation command", ubuntuQemuTemplatePath)
	}
	if !strings.Contains(got, "\"cp \\\"${local.output_directory}\\\"/linux-ubuntu-${var.ubuntu_type}-packer-* \\\"${local.ubuntu_qcow2}\\\"\"") {
		t.Errorf("expected template %q to quote the QCOW2 export command", ubuntuQemuTemplatePath)
	}
}

func TestUbuntuArm64QemuBootOrderPrefersInstalledDiskAfterInstall(t *testing.T) {
	t.Parallel()

	got := readRepoFile(t, ubuntuQemuTemplatePath)
	if !strings.Contains(got, "drive=disk,serial=deadbeef,bootindex=0") {
		t.Errorf("expected template %q to prefer the installed ARM64 disk once it becomes bootable", ubuntuQemuTemplatePath)
	}
	if !strings.Contains(got, "drive=cdrom,bootindex=1") {
		t.Errorf("expected template %q to keep the ARM64 installer ISO as the blank-disk fallback", ubuntuQemuTemplatePath)
	}
	if !strings.Contains(got, "efi_boot") {
		t.Errorf("expected template %q to enable Packer EFI mode for ARM64 so the qemu builder does not inject -boot", ubuntuQemuTemplatePath)
	}
	if !strings.Contains(got, "AAVMF_CODE.no-secboot.fd") {
		t.Errorf("expected template %q to attach ARM64 AAVMF code firmware", ubuntuQemuTemplatePath)
	}
	if !strings.Contains(got, "file={{ .OutputDir }}/efivars.fd,if=pflash,unit=1,format=raw") {
		t.Errorf("expected template %q to attach Packer's writable ARM64 efivars copy", ubuntuQemuTemplatePath)
	}
	if strings.Contains(got, `["-boot",`) {
		t.Errorf("template %q uses unsupported ARM64 QEMU boot ordering through -boot", ubuntuQemuTemplatePath)
	}
}

func TestWindowsQemuTemplateIsSharedAcrossMacOSAndLinux(t *testing.T) {
	t.Parallel()

	assertContainsAll(t, "shared Windows QEMU template "+windowsQemuTemplatePath, readRepoFile(t, windowsQemuTemplatePath),
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
	)
}

func TestWindowsArm64QemuUsesWritableEfiVarsAndAccelerationAwareBootOrder(t *testing.T) {
	t.Parallel()

	got := readRepoFile(t, windowsQemuTemplatePath)
	assertContainsAll(t, "Windows ARM64 QEMU template "+windowsQemuTemplatePath, got,
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
	)

	arm64Args, ok := textBetween(got, `"arm64" = [`, "]\n  }")
	if !ok {
		t.Fatalf("failed to locate arm64 QEMU args in %q", windowsQemuTemplatePath)
	}
	if strings.Contains(arm64Args, `["-boot",`) {
		t.Errorf("template %q uses unsupported ARM64 QEMU boot ordering through -boot", windowsQemuTemplatePath)
	}
	if strings.Contains(arm64Args, `QEMU_EFI.fd`) || strings.Contains(arm64Args, `["-bios",`) {
		t.Errorf("template %q boots ARM64 Windows with non-persistent -bios firmware", windowsQemuTemplatePath)
	}
}

func TestWindowsAmd64QemuAttachesVirtioDriverIso(t *testing.T) {
	t.Parallel()

	amd64Args, ok := textBetween(readRepoFile(t, windowsQemuTemplatePath), `"amd64" = [`, `"arm64" = [`)
	if !ok {
		t.Fatalf("failed to locate amd64 QEMU args in %q", windowsQemuTemplatePath)
	}
	assertContainsAll(t, "amd64 QEMU args in "+windowsQemuTemplatePath, amd64Args,
		`["-device", "usb-storage,drive=virtio-drivers,removable=true,bootindex=2"]`,
		`["-drive", "if=none,id=virtio-drivers,format=raw,media=cdrom,file=${local.win11_virtio_iso},readonly=true"]`,
	)
}
