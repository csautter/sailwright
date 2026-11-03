//go:build linux
// +build linux

package build

import (
	"context"
	"os"
	"testing"
)

func linuxQemuUbuntuConfig(arch string, ubuntuType string, vncPort int) VirtualMachineConfig {
	memoryMB := 4096
	if os.Getenv("GITHUB_ACTIONS") != "" {
		memoryMB = 0
	}

	return VirtualMachineConfig{
		OS:                   "ubuntu",
		Arch:                 arch,
		UbuntuType:           ubuntuType,
		VncPort:              vncPort,
		HostOs:               HostOsLinux,
		VirtualizationEngine: VirtualizationEngineQemu,
		Cpus:                 4,
		MemoryMB:             memoryMB,
		Headless:             true,
	}
}

func linuxQemuWindowsConfig(arch string, vncPort int) VirtualMachineConfig {
	memoryMB := 4096
	if os.Getenv("GITHUB_ACTIONS") != "" {
		memoryMB = 0
	}

	return VirtualMachineConfig{
		OS:                   "windows11",
		Arch:                 arch,
		VncPort:              vncPort,
		HostOs:               HostOsLinux,
		VirtualizationEngine: VirtualizationEngineQemu,
		Cpus:                 4,
		MemoryMB:             memoryMB,
		Headless:             true,
	}
}

func TestIntegrationDependencyReconciliationQemuUbuntuAmd64OnLinux(t *testing.T) {
	requireIntegrationTests(t)

	if err := DependencyReconciliation(context.Background(), linuxQemuUbuntuConfig("amd64", "server", 5922)); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationDependencyReconciliationQemuUbuntuArm64OnLinux(t *testing.T) {
	requireIntegrationTests(t)

	if err := DependencyReconciliation(context.Background(), linuxQemuUbuntuConfig("arm64", "server", 5921)); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationDependencyReconciliationQemuUbuntuDesktopAmd64OnLinux(t *testing.T) {
	requireIntegrationTests(t)

	if err := DependencyReconciliation(context.Background(), linuxQemuUbuntuConfig("amd64", "desktop", 5924)); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationDependencyReconciliationQemuUbuntuDesktopArm64OnLinux(t *testing.T) {
	requireIntegrationTests(t)

	if err := DependencyReconciliation(context.Background(), linuxQemuUbuntuConfig("arm64", "desktop", 5923)); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationDependencyReconciliationQemuWindows11Amd64OnLinux(t *testing.T) {
	requireIntegrationTests(t)

	if err := DependencyReconciliation(context.Background(), linuxQemuWindowsConfig("amd64", 5932)); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationDependencyReconciliationQemuWindows11Arm64OnLinux(t *testing.T) {
	requireIntegrationTests(t)

	if err := DependencyReconciliation(context.Background(), linuxQemuWindowsConfig("arm64", 5931)); err != nil {
		t.Fatal(err)
	}
}

func TestBuildQemuUbuntuServerAmd64OnLinux(t *testing.T) {
	requireIntegrationTests(t)
	t.Parallel()

	if err := RunQemuUbuntuBuildOnLinux(context.Background(), linuxQemuUbuntuConfig("amd64", "server", 5922)); err != nil {
		t.Fatalf("Failed to build QEMU Ubuntu Server Amd64 on Linux: %v", err)
	}
}

func TestBuildQemuUbuntuServerArm64OnLinux(t *testing.T) {
	requireIntegrationTests(t)
	t.Parallel()

	if err := RunQemuUbuntuBuildOnLinux(context.Background(), linuxQemuUbuntuConfig("arm64", "server", 5921)); err != nil {
		t.Fatalf("Failed to build QEMU Ubuntu Server Arm64 on Linux: %v", err)
	}
}

func TestBuildQemuUbuntuDesktopAmd64OnLinux(t *testing.T) {
	requireIntegrationTests(t)
	t.Parallel()

	if err := RunQemuUbuntuBuildOnLinux(context.Background(), linuxQemuUbuntuConfig("amd64", "desktop", 5924)); err != nil {
		t.Fatalf("Failed to build QEMU Ubuntu Desktop Amd64 on Linux: %v", err)
	}
}

func TestBuildQemuUbuntuDesktopArm64OnLinux(t *testing.T) {
	requireIntegrationTests(t)
	t.Parallel()

	if err := RunQemuUbuntuBuildOnLinux(context.Background(), linuxQemuUbuntuConfig("arm64", "desktop", 5923)); err != nil {
		t.Fatalf("Failed to build QEMU Ubuntu Desktop Arm64 on Linux: %v", err)
	}
}

func TestBuildQemuWindows11Amd64OnLinux(t *testing.T) {
	requireIntegrationTests(t)
	t.Parallel()

	if err := RunQemuWindowsBuildOnLinux(context.Background(), linuxQemuWindowsConfig("amd64", 5932)); err != nil {
		t.Fatalf("Failed to build QEMU Windows 11 Amd64 on Linux: %v", err)
	}
}

func TestBuildQemuWindows11Arm64OnLinux(t *testing.T) {
	requireIntegrationTests(t)
	t.Parallel()

	if err := RunQemuWindowsBuildOnLinux(context.Background(), linuxQemuWindowsConfig("arm64", 5931)); err != nil {
		t.Fatalf("Failed to build QEMU Windows 11 Arm64 on Linux: %v", err)
	}
}
