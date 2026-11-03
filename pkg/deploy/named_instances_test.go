package deploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	build "github.com/csautter/dev-alchemy/pkg/build"
)

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
