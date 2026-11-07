package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestIsUtmDeployTarget(t *testing.T) {
	config := alchemy_build.VirtualMachineConfig{
		OS:                   "ubuntu",
		UbuntuType:           "server",
		Arch:                 "arm64",
		HostOs:               alchemy_build.HostOsDarwin,
		VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
	}

	if !isUtmDeployTarget(config) {
		t.Fatal("expected darwin utm ubuntu config to be supported")
	}
}

func TestIsUtmDeployTargetInfersCanonicalDarwinUtmConfig(t *testing.T) {
	config := alchemy_build.VirtualMachineConfig{
		OS:   "windows11",
		Arch: "arm64",
	}

	if !isUtmDeployTarget(config) {
		t.Fatal("expected windows11 arm64 config to resolve to darwin utm target")
	}
}

func TestIsUtmDeployTargetRejectsExplicitNonUtmConfig(t *testing.T) {
	config := alchemy_build.VirtualMachineConfig{
		OS:                   "windows11",
		Arch:                 "arm64",
		HostOs:               alchemy_build.HostOsWindows,
		VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
	}

	if isUtmDeployTarget(config) {
		t.Fatal("expected explicit non-utm config to be rejected")
	}
}

func TestUtmVirtualMachineNameIncludesTypeAndArch(t *testing.T) {
	config := alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "desktop",
		Arch:       "amd64",
	}

	if got := utmVirtualMachineName(config); got != "ubuntu-desktop-amd64-dev-alchemy" {
		t.Fatalf("unexpected UTM VM name %q", got)
	}
}

func TestUtmVirtualMachinePathUsesUtmDocumentsBundle(t *testing.T) {
	t.Setenv("SAILWRIGHT_UTM_VM_DIR", "")
	config := alchemy_build.VirtualMachineConfig{
		OS:         "ubuntu",
		UbuntuType: "server",
		Arch:       "arm64",
	}

	got, err := utmVirtualMachinePath(config)
	if err != nil {
		t.Fatalf("utmVirtualMachinePath returned error: %v", err)
	}

	wantSuffix := filepath.Join(
		"Library",
		"Containers",
		"com.utmapp.UTM",
		"Data",
		"Documents",
		"ubuntu-server-arm64-dev-alchemy.utm",
	)
	if !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("expected UTM VM path %q to end with %q", got, wantSuffix)
	}
}

func TestUtmOverrideDestroyPreservesDefaultVM(t *testing.T) {
	vm, _, _ := mockMacOSLifecycle(t)
	t.Setenv("SAILWRIGHT_UTM_VM_DIR", "")
	defaultVM, err := utmVirtualMachinePath(vm)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(defaultVM, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAILWRIGHT_UTM_VM_DIR", filepath.Join(t.TempDir(), "custom bundles"))
	customVM, err := utmVirtualMachinePath(vm)
	if err != nil {
		t.Fatal(err)
	}
	if exists, err := CreateTargetExists(vm); err != nil || exists {
		t.Fatalf("default VM must not count as custom VM: exists=%v, err=%v", exists, err)
	}
	if err := os.MkdirAll(customVM, 0o700); err != nil {
		t.Fatal(err)
	}
	if exists, err := CreateTargetExists(vm); err != nil || !exists {
		t.Fatalf("custom VM must be found: exists=%v, err=%v", exists, err)
	}
	for range 2 {
		if err := RunUtmDestroyOnMacOS(vm); err != nil {
			t.Fatal(err)
		}
	}
	if exists, err := DestroyTargetExists(vm); err != nil || exists {
		t.Fatalf("custom VM must be absent: exists=%v, err=%v", exists, err)
	}
	if _, err := os.Stat(defaultVM); err != nil {
		t.Fatalf("default VM must survive: %v", err)
	}
	state, err := inspectUtmStartTarget(vm)
	if err != nil || state.Exists {
		t.Fatalf("start must not find the default VM: state=%+v, err=%v", state, err)
	}
}

func TestUtmStatusIndicatesRunning(t *testing.T) {
	if !utmStatusIndicatesRunning("started") {
		t.Fatal("expected started status to be running")
	}
	if !utmStatusIndicatesRunning("resuming") {
		t.Fatal("expected resuming status to be running")
	}
	if utmStatusIndicatesRunning("stopped") {
		t.Fatal("did not expect stopped status to be running")
	}
}
