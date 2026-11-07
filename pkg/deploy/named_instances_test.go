package deploy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/internal/operationlock"
	build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestDefaultHypervDestroySharesBoxLockAcrossAppData(t *testing.T) {
	restore := stubHypervStopDependencies(t)
	defer restore()
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LOCALAPPDATA", cache)
	oldTimeout := hypervBoxLockTimeout
	hypervBoxLockTimeout = 0
	t.Cleanup(func() { hypervBoxLockTimeout = oldTimeout })
	vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", HostOs: build.HostOsWindows, VirtualizationEngine: build.VirtualizationEngineHyperv}
	release, err := acquireHypervBoxLock("linux-ubuntu-server-packer", "import named clone", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { release() }()
	// The default VM may already be gone, but removing its shared box must wait.
	hypervVagrantMachineExistsChecker = func(string, []string) (bool, error) { return false, nil }
	checked := false
	hypervVagrantBoxInstalledChecker = func(string, string) (bool, error) { checked = true; return false, nil }
	dirs := build.GetDirectoriesInstance()
	oldApp := dirs.AppDataDir
	dirs.AppDataDir = t.TempDir()
	t.Cleanup(func() { dirs.AppDataDir = oldApp })
	if err := RunHypervVagrantDestroyOnWindows(vm); !errors.Is(err, operationlock.ErrBusy) || checked {
		t.Fatalf("destroy accessed a box being imported: err=%v checked=%v", err, checked)
	}
	release()
	// Lock release functions are single-use.
	release = func() {}
	if err := RunHypervVagrantDestroyOnWindows(vm); err != nil || !checked {
		t.Fatalf("destroy after release: %v checked=%v", err, checked)
	}
}

func TestNamedHypervUbuntuRejectsLegacyImageBeforeImport(t *testing.T) {
	vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", Name: "work", HostOs: build.HostOsWindows, VirtualizationEngine: build.VirtualizationEngineHyperv, ExpectedBuildArtifacts: []string{filepath.Join(t.TempDir(), "legacy.box")}}
	if err := RunHypervVagrantDeployOnWindows(vm); err == nil || !strings.Contains(err.Error(), "--no-cache") {
		t.Fatalf("legacy image was not rejected before Vagrant execution: %v", err)
	}
}

func TestNamedProviderPaths(t *testing.T) {
	t.Setenv("TART_MACOS_VM_NAME", "")
	t.Setenv(linuxLibvirtImageDirEnvVar, t.TempDir())
	for _, engine := range []build.VirtualizationEngine{build.VirtualizationEngineQemu, build.VirtualizationEngineUtm, build.VirtualizationEngineTart, build.VirtualizationEngineHyperv} {
		t.Run(string(engine), func(t *testing.T) {
			vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", VirtualizationEngine: engine}
			if engine == build.VirtualizationEngineTart {
				vm.OS = "macos"
				vm.UbuntuType = ""
				vm.Arch = "arm64"
			}
			seen := map[string]bool{}
			for _, name := range []string{"", "a", "b"} {
				vm.Name = name
				var identity string
				switch engine {
				case build.VirtualizationEngineQemu:
					identity = linuxLibvirtDiskPath(vm)
					if filepath.Base(identity) != build.InstanceName(vm)+".qcow2" {
						t.Fatal(identity)
					}
					// SPICE uses provider auto-allocation; no fixed port or MAC in XML args.
					args := strings.Join(linuxLibvirtVirtInstallArgs(vm, "qemu:///system", identity), " ")
					if strings.Contains(args, "port=") || strings.Contains(args, "mac=") {
						t.Fatal(args)
					}
				case build.VirtualizationEngineUtm:
					var err error
					identity, err = utmVirtualMachinePath(vm)
					if err != nil {
						t.Fatal(err)
					}
					if filepath.Base(identity) != build.InstanceName(vm)+".utm" {
						t.Fatal(identity)
					}
				case build.VirtualizationEngineTart:
					identity = tartMacOSVMName(vm)
				case build.VirtualizationEngineHyperv:
					settings, err := resolveHypervVagrantDeploySettings(vm, t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					vmName, err := hypervVagrantVMName(settings.VagrantEnv)
					if err != nil {
						t.Fatal(err)
					}
					identity = hypervVagrantDotfilePath(vmName)
					if name != "" && !strings.Contains(vmName, "-"+name+"-dev-alchemy") {
						t.Fatal(vmName)
					}
				}
				if seen[identity] {
					t.Fatalf("shared path %s", identity)
				}
				seen[identity] = true
			}
		})
	}
}

func TestDiscoveryDoesNotNeedRegistry(t *testing.T) {
	old := runLinuxLibvirtCommandWithCombinedOut
	t.Cleanup(func() { runLinuxLibvirtCommandWithCombinedOut = old })
	runLinuxLibvirtCommandWithCombinedOut = func(_ string, _ time.Duration, _ string, args []string) (string, error) {
		return "ubuntu-server-amd64-a-dev-alchemy\nubuntu-server-amd64-work-long-dev-alchemy\nubuntu-server-amd64-dev-alchemy\nunmanaged\n", nil
	}
	vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", VirtualizationEngine: build.VirtualizationEngineQemu}
	found, err := DiscoverInstances([]build.VirtualMachineConfig{vm})
	if err != nil || len(found) != 2 || found[0].Name != "a" || found[1].Name != "work-long" {
		t.Fatalf("discovery: %+v %v", found, err)
	}
}

func TestInstanceRegistry(t *testing.T) {
	oldLookPath := lookPathLinuxLibvirtCommand
	t.Cleanup(func() { lookPathLinuxLibvirtCommand = oldLookPath })
	lookPathLinuxLibvirtCommand = func(command string) (string, error) { return command, nil }
	dirs := build.GetDirectoriesInstance()
	oldDir := dirs.AppDataDir
	dirs.AppDataDir = t.TempDir()
	t.Cleanup(func() { dirs.AppDataDir = oldDir })
	old := runLinuxLibvirtCommandWithCombinedOut
	t.Cleanup(func() { runLinuxLibvirtCommandWithCombinedOut = old })
	runLinuxLibvirtCommandWithCombinedOut = func(_ string, _ time.Duration, _ string, args []string) (string, error) {
		return "12345678-1234-1234-1234-123456789abc", nil
	}
	vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", Name: "a", HostOs: build.HostOsLinux, VirtualizationEngine: build.VirtualizationEngineQemu}
	if err := RecordInstance(vm); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(instanceRecordPath(vm))
	if err != nil {
		t.Fatal(err)
	}
	var record InstanceRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.SchemaVersion != 1 || record.Name != "a" || record.CreatedAt.IsZero() || record.InstanceID == "" {
		t.Fatalf("%+v", record)
	}
	vm.Name = "b"
	if _, err := os.Stat(instanceRecordPath(vm)); !os.IsNotExist(err) {
		t.Fatal("registry identity collision")
	}
	vm.Name = "a"
	for i := 0; i < 2; i++ {
		if err := RemoveInstanceRecord(vm); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNamedHypervFallbackUsesExactProviderName(t *testing.T) {
	vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", Name: "work", VirtualizationEngine: build.VirtualizationEngineHyperv}
	for _, action := range []string{"start", "stop", "destroy"} {
		script, err := namedHypervActionScript(vm, action)
		if err != nil || !strings.Contains(script, "Get-VM -Name 'ubuntu-server-amd64-work-dev-alchemy'") {
			t.Fatal(script, err)
		}
		if action == "destroy" && !strings.Contains(script, "$users.Count -eq 0") {
			t.Fatal("shared disk deletion guard missing")
		}
	}
	vm.Name = ""
	if err := runNamedHypervAction(vm, "destroy"); err == nil {
		t.Fatal("direct action accepted a default instance")
	}
}

func TestTartDiscoverySupportsLegacyListing(t *testing.T) {
	old := runInstanceDiscoveryCommand
	defer func() { runInstanceDiscoveryCommand = old }()
	calls := 0
	runInstanceDiscoveryCommand = func(_ string, _ time.Duration, _ string, args []string) (string, error) {
		calls++
		if len(args) > 1 {
			return "not JSON", nil
		}
		return "Source Name Disk Size\nlocal macos-arm64-work-dev-alchemy 20 GB\nOCI ghcr.io/example/macos:latest 20 GB", nil
	}
	vm := build.VirtualMachineConfig{OS: "macos", Arch: "arm64", VirtualizationEngine: build.VirtualizationEngineTart}
	found, err := DiscoverInstances([]build.VirtualMachineConfig{vm})
	if err != nil || len(found) != 1 || found[0].Name != "work" || calls != 2 {
		t.Fatal(found, err, calls)
	}
}
