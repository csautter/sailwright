package deploy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	build "github.com/csautter/dev-alchemy/pkg/build"
)

var runInstanceDiscoveryCommand = runCommandWithCombinedOutput

// DiscoverInstances queries the hypervisor, not the registry. Defaults are supplied
// by the caller, so old installations need no metadata or migration.
func DiscoverInstances(bases []build.VirtualMachineConfig) ([]build.VirtualMachineConfig, error) {
	var found []build.VirtualMachineConfig
	engines := map[build.VirtualizationEngine]bool{}
	for _, base := range bases {
		engine := base.VirtualizationEngine
		if engines[engine] {
			continue
		}
		engines[engine] = true
		names, err := providerInstanceNames(engine)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			for _, candidate := range bases {
				if candidate.VirtualizationEngine != engine {
					continue
				}
				vm, ok := build.ParseInstanceName(name, candidate)
				if ok && vm.Name != "" {
					found = append(found, vm)
					break
				}
			}
		}
	}
	sort.Slice(found, func(i, j int) bool { return build.InstanceKey(found[i]) < build.InstanceKey(found[j]) })
	return found, nil
}

func providerInstanceNames(engine build.VirtualizationEngine) ([]string, error) {
	project := build.GetDirectoriesInstance().ProjectDir
	var output string
	var err error
	switch engine {
	case build.VirtualizationEngineQemu:
		output, err = runLinuxLibvirtCommandWithCombinedOut(project, linuxLibvirtCommandTimeout, "virsh", []string{"--connect", linuxLibvirtURI(), "list", "--all", "--name"})
	case build.VirtualizationEngineHyperv:
		output, err = runHypervCommandWithCombinedOutput(project, instanceIDQueryTimeout, "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", "Get-VM | Select-Object -ExpandProperty Name"})
	case build.VirtualizationEngineTart:
		output, err = runInstanceDiscoveryCommand(project, instanceIDQueryTimeout, "tart", []string{"list", "--format", "json"})
		var entries []struct {
			Name   string `json:"Name"`
			Source string `json:"Source"`
		}
		if err == nil && json.Unmarshal([]byte(output), &entries) == nil {
			var names []string
			for _, entry := range entries {
				if strings.EqualFold(entry.Source, "local") {
					names = append(names, entry.Name)
				}
			}
			return names, nil
		}
		output, err = runInstanceDiscoveryCommand(project, instanceIDQueryTimeout, "tart", []string{"list"})
		if err != nil {
			return nil, err
		}
		var names []string
		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && strings.EqualFold(fields[0], "local") {
				names = append(names, fields[1])
			}
		}
		return names, nil

	case build.VirtualizationEngineUtm:
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(filepath.Join(home, "Library", "Containers", "com.utmapp.UTM", "Data", "Documents"))
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var names []string
		for _, entry := range entries {
			if entry.IsDir() && strings.HasSuffix(entry.Name(), ".utm") {
				names = append(names, strings.TrimSuffix(entry.Name(), ".utm"))
			}
		}
		return names, nil
	default:
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s instances: %w", engine, err)
	}
	var names []string
	for _, line := range strings.Split(output, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// InstanceRecord is supplemental metadata. Hypervisor state remains authoritative.
// SchemaVersion leaves room for additional lifecycle policy fields.
type InstanceRecord struct {
	SchemaVersion  int                        `json:"schema_version"`
	Name           string                     `json:"name"`
	OS             string                     `json:"os"`
	Type           string                     `json:"type"`
	Arch           string                     `json:"arch"`
	Engine         build.VirtualizationEngine `json:"engine"`
	HypervisorName string                     `json:"hypervisor_name"`
	InstanceID     string                     `json:"instance_id"`
	CreatedAt      time.Time                  `json:"created_at"`
	ImageVersion   string                     `json:"image_version"`
}

func instanceRecordPath(vm build.VirtualMachineConfig) string {
	return filepath.Join(build.GetDirectoriesInstance().AppDataDir, "instances", string(vm.VirtualizationEngine), fmt.Sprintf("%x.json", sha256.Sum256([]byte(build.InstanceKey(vm)))))
}

// RecordInstance stores metadata only after successful creation. Unknown image
// provenance is represented by an empty image_version, never an invented version.
func RecordInstance(vm build.VirtualMachineConfig) error {
	if err := build.ValidateInstance(vm); err != nil {
		return err
	}
	id, exists, err := InspectInstanceID(vm)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("created VM is absent from hypervisor")
	}
	record := InstanceRecord{SchemaVersion: 1, Name: vm.Name, OS: vm.OS, Type: vm.UbuntuType, Arch: vm.Arch, Engine: vm.VirtualizationEngine, HypervisorName: build.InstanceName(vm), InstanceID: id, CreatedAt: time.Now().UTC()}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	file := instanceRecordPath(vm)
	if err = os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".instance-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		// Preserve the write error; closing cannot make the incomplete file usable.
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

func RemoveInstanceRecord(vm build.VirtualMachineConfig) error {
	if err := build.ValidateInstance(vm); err != nil {
		return err
	}
	err := os.Remove(instanceRecordPath(vm))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// InstanceStorageDirectory selects the filesystem used for the new clone.
func InstanceStorageDirectory(vm build.VirtualMachineConfig) string {
	switch vm.VirtualizationEngine {
	case build.VirtualizationEngineQemu:
		return linuxLibvirtImageDir()
	case build.VirtualizationEngineUtm:
		if path, err := utmVirtualMachinePath(vm); err == nil {
			return filepath.Dir(path)
		}
	case build.VirtualizationEngineTart:
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".tart", "vms")
		}
	}
	return build.GetDirectoriesInstance().AppDataDir
}
