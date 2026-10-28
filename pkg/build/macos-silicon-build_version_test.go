package build

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMacOSQemuVersionCheck(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		output  string
		want    macOSQemuVersionStatus
		wantMsg string
	}{
		{name: "tested 11", output: "QEMU emulator version 11.1.2\nCopyright (c) 2003-2026 Fabrice Bellard", want: macOSQemuVersionOK, wantMsg: "QEMU 11.1"},
		{name: "tested 10", output: "QEMU emulator version 10.0.3", want: macOSQemuVersionOK, wantMsg: "QEMU 10.0"},
		{name: "untested major", output: "QEMU emulator version 12.0.0", want: macOSQemuVersionWarn, wantMsg: "has not been tested"},
		{name: "too old for virt-10.0", output: "QEMU emulator version 9.2.4", want: macOSQemuVersionTooOld, wantMsg: "virt-10.0 machine needs QEMU 10.0"},
		{name: "10.0 release candidate", output: "QEMU emulator version 9.2.90", want: macOSQemuVersionTooOld, wantMsg: "QEMU 9.2 is too old"},
		{name: "unparsable", output: "something else", want: macOSQemuVersionWarn, wantMsg: "could not parse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			msg, status := macOSQemuVersionCheck(tc.output)
			if status != tc.want || !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("macOSQemuVersionCheck(%q) = %q, %v; want %v and message containing %q", tc.output, msg, status, tc.want, tc.wantMsg)
			}
		})
	}
}

// A QEMU without the virt-10.0 machine must stop the build before artifacts are staged.
func TestCheckMacOSArm64QemuVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as fake qemu-system-aarch64")
	}
	for _, tc := range []struct {
		name    string
		arch    string
		version string
		wantErr string
	}{
		{name: "too old fails", arch: "arm64", version: "9.2.4", wantErr: "brew upgrade qemu"},
		{name: "untested only warns", arch: "arm64", version: "12.0.0"},
		{name: "amd64 skipped", arch: "amd64", version: "9.2.4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binDir := t.TempDir()
			fake := "#!/bin/sh\necho 'QEMU emulator version " + tc.version + "'\n"
			if err := os.WriteFile(filepath.Join(binDir, "qemu-system-aarch64"), []byte(fake), 0o755); err != nil { // #nosec G306 -- test needs an executable fake QEMU.
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir)

			err := checkMacOSArm64QemuVersion(VirtualMachineConfig{Arch: tc.arch})
			if tc.wantErr == "" && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// Without qemu-system-aarch64 the check only warns; Packer reports the missing binary later.
func TestCheckMacOSArm64QemuVersionWithoutQemu(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if err := checkMacOSArm64QemuVersion(VirtualMachineConfig{Arch: "arm64"}); err != nil {
		t.Fatalf("expected no error without qemu-system-aarch64, got %v", err)
	}
}
