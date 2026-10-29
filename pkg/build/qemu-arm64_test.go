package build

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestSelectArm64QemuMachine(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		help string
		want string
	}{
		{name: "QEMU 11 has virt-10.0", help: "Supported machines are:\nvirt                 QEMU 11.0 ARM Virtual Machine (alias of virt-11.0)\nvirt-11.0            QEMU 11.0 ARM Virtual Machine\nvirt-10.0            QEMU 10.0 ARM Virtual Machine\nvirt-10.0-ext        made up\n", want: "virt-10.0"},
		{name: "QEMU 8.2 lacks virt-10.0", help: "Supported machines are:\nvirt                 QEMU 8.2 ARM Virtual Machine (alias of virt-8.2)\nvirt-8.2             QEMU 8.2 ARM Virtual Machine\n", want: "virt"},
		{name: "prefix only", help: "virt-10.0-ext        made up\n", want: "virt"},
		{name: "empty", help: "", want: "virt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := selectArm64QemuMachine(tc.help); got != tc.want {
				t.Errorf("selectArm64QemuMachine() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestArm64QemuMachineEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as fake qemu-system-aarch64")
	}
	for _, tc := range []struct {
		name     string
		arch     string
		machines string
		userEnv  string
		noQemu   bool
		want     []string
	}{
		{name: "QEMU with virt-10.0", arch: "arm64", machines: "virt-11.0 x\\nvirt-10.0 x", want: []string{"PKR_VAR_arm64_machine=virt-10.0,highmem=on"}},
		{name: "QEMU without virt-10.0", arch: "arm64", machines: "virt-8.2 x", want: []string{"PKR_VAR_arm64_machine=virt,highmem=on"}},
		{name: "user override wins", arch: "arm64", machines: "virt-10.0 x", userEnv: "virt-9.2,highmem=on"},
		{name: "no QEMU keeps template default", arch: "arm64", noQemu: true},
		{name: "amd64 skipped", arch: "amd64", machines: "virt-10.0 x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binDir := t.TempDir()
			if !tc.noQemu {
				fake := "#!/bin/sh\nprintf 'Supported machines are:\\n" + tc.machines + "\\n'\n"
				if err := os.WriteFile(filepath.Join(binDir, "qemu-system-aarch64"), []byte(fake), 0o755); err != nil { // #nosec G306 -- test needs an executable fake QEMU.
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", binDir)
			t.Setenv(arm64QemuMachineEnvVar, tc.userEnv)

			if got := arm64QemuMachineEnv(VirtualMachineConfig{Arch: tc.arch}); !slices.Equal(got, tc.want) {
				t.Errorf("arm64QemuMachineEnv() = %q, want %q", got, tc.want)
			}
		})
	}
}
