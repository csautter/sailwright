//go:build windows
// +build windows

package build

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestBuildHypervWindows11Amd64OnWindows(t *testing.T) {
	requireIntegrationTests(t)
	t.Parallel()

	memoryMB := 4096
	if os.Getenv("GITHUB_ACTIONS") != "" {
		memoryMB = 0
	}

	VirtualMachineConfig := VirtualMachineConfig{
		OS:                   "windows11",
		Arch:                 "amd64",
		VncPort:              5912,
		HostOs:               HostOsWindows,
		VirtualizationEngine: VirtualizationEngineHyperv,
		Cpus:                 4,
		MemoryMB:             memoryMB,
	}
	err := RunHypervWindowsBuildOnWindows(context.Background(), VirtualMachineConfig)
	if err != nil {
		t.Fatalf("Failed to build Hyper-V Windows 11 Amd64 on Windows: %v", err)
	}
}

func TestBuildVirtualBoxWindows11Amd64OnWindows(t *testing.T) {
	requireIntegrationTests(t)
	t.Parallel()

	memoryMB := 4096
	if os.Getenv("GITHUB_ACTIONS") != "" {
		memoryMB = 0
	}

	VirtualMachineConfig := VirtualMachineConfig{
		OS:                   "windows11",
		Arch:                 "amd64",
		VncPort:              5913,
		HostOs:               HostOsWindows,
		VirtualizationEngine: VirtualizationEngineVirtualBox,
		Cpus:                 4,
		MemoryMB:             memoryMB,
	}
	err := RunVirtualBoxWindowsBuildOnWindows(context.Background(), VirtualMachineConfig)
	if err != nil {
		t.Fatalf("Failed to build VirtualBox Windows 11 Amd64 on Windows: %v", err)
	}
}

// TestInitializePackerPropagatesError verifies that a failed packer init is not silently
// swallowed — the error must be returned so callers can fail fast.
func TestInitializePackerPropagatesError(t *testing.T) {
	// A non-existent HCL path guarantees packer init exits non-zero (either the packer
	// binary is missing entirely, or it rejects the unknown file).
	err := initializePacker(context.Background(), VirtualMachineConfig{}, "/nonexistent/path/that/does/not/exist.pkr.hcl")
	if err == nil {
		t.Fatal("expected initializePacker to return an error for a non-existent packer file, got nil")
	}
}

// TestRunWindowsBuildPropagatesInitError verifies that runWindowsBuild surfaces the
// error from initializePacker rather than proceeding to the build step.
func TestRunWindowsBuildPropagatesInitError(t *testing.T) {
	cfg := VirtualMachineConfig{
		OS:                   "windows11",
		Arch:                 "amd64",
		HostOs:               HostOsWindows,
		VirtualizationEngine: VirtualizationEngineHyperv,
	}

	err := runWindowsBuild(context.Background(), cfg, "/nonexistent/path/that/does/not/exist.pkr.hcl")
	if err == nil {
		t.Fatal("expected runWindowsBuild to return an error when packer init fails, got nil")
	}

	// The error message must mention the init step so callers can diagnose the root cause.
	const wantSubstr = "failed to initialize packer"
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error message %q does not contain expected substring %q", err.Error(), wantSubstr)
	}
}
