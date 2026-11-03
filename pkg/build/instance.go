package build

import (
	"crypto/sha256"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var instanceNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,22}[a-z0-9])?$`)

// ValidateInstanceName accepts an empty name for the legacy default instance.
func ValidateInstanceName(name string) error {
	if name == "" {
		return nil
	}
	if name == "default" || name == "all" || !instanceNamePattern.MatchString(name) {
		return fmt.Errorf("invalid instance name %q: use 1–24 lowercase letters, digits or internal hyphens; default and all are reserved", name)
	}
	return nil
}

// ValidateInstance checks selectors before any provider command is executed.
func ValidateInstance(vm VirtualMachineConfig) error {
	if err := ValidateInstanceName(vm.Name); err != nil {
		return err
	}
	if vm.Name != "" && vm.VirtualizationEngine == VirtualizationEngineTart && strings.TrimSpace(os.Getenv("TART_MACOS_VM_NAME")) != "" {
		return fmt.Errorf("TART_MACOS_VM_NAME cannot be combined with --name")
	}
	return nil
}

// InstanceKey is the logical address, independent of provider and image cache.
func InstanceKey(vm VirtualMachineConfig) string {
	key := vm.OS + "/" + vm.UbuntuType + "/" + vm.Arch
	if vm.Name != "" {
		key += "/" + vm.Name
	}
	return key
}

// InstanceName derives provider identity while preserving every legacy default.
// A future OS version belongs after OS, before type; image slugs exclude Name.
func InstanceName(vm VirtualMachineConfig) string {
	if vm.Name == "" {
		switch vm.VirtualizationEngine {
		case VirtualizationEngineTart:
			if value := strings.TrimSpace(os.Getenv("TART_MACOS_VM_NAME")); value != "" {
				return value
			}
			return "tahoe-base-alchemy"
		case VirtualizationEngineHyperv:
			if vm.OS == "windows11" {
				return "win11-packer"
			}
			typ := vm.UbuntuType
			if typ == "" {
				typ = "server"
			}
			return "linux-ubuntu-" + typ + "-packer"
		}
	}
	name := GetVirtualMachineNameWithType(vm) + "-" + vm.Arch
	if vm.Name != "" {
		name += "-" + vm.Name
	}
	return name + "-dev-alchemy"
}

// ParseInstanceName recognizes provider names without depending on local metadata.
func ParseInstanceName(providerName string, base VirtualMachineConfig) (VirtualMachineConfig, bool) {
	base.Name = ""
	if providerName == InstanceName(base) {
		return base, true
	}
	prefix := GetVirtualMachineNameWithType(base) + "-" + base.Arch + "-"
	if !strings.HasPrefix(providerName, prefix) || !strings.HasSuffix(providerName, "-dev-alchemy") {
		return base, false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(providerName, prefix), "-dev-alchemy")
	if name == "" || ValidateInstanceName(name) != nil {
		return base, false
	}
	base.Name = name
	return base, true
}

// InstanceHostname obeys Windows' 15-character computer-name limit. Unix guests
// use the full provider name, which fits the DNS label limit.
func InstanceHostname(vm VirtualMachineConfig) string {
	if vm.OS == "windows11" {
		return fmt.Sprintf("sw-%x", sha256.Sum256([]byte(InstanceKey(vm))))[:15]
	}
	return InstanceName(vm)
}
