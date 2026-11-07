package build

import (
	"strings"
	"testing"
)

func TestInstanceIdentity(t *testing.T) {
	t.Setenv("TART_MACOS_VM_NAME", "")
	for _, vm := range AvailableVirtualMachineConfigs() {
		if vm.VirtualizationEngine == VirtualizationEngineVirtualBox {
			continue
		}
		legacy := InstanceName(vm)
		image := GenerateVirtualMachineSlug(&vm)
		seen := map[string]bool{legacy: true}
		for _, name := range []string{"a", "b", "work-long-name", strings.Repeat("a", 24)} {
			vm.Name = name
			if err := ValidateInstance(vm); err != nil {
				t.Fatal(err)
			}
			provider := InstanceName(vm)
			if seen[provider] {
				t.Fatalf("duplicate %s", provider)
			}
			seen[provider] = true
			parsed, ok := ParseInstanceName(provider, vm)
			if !ok || parsed.Name != name {
				t.Fatalf("round trip %q: %+v %t", provider, parsed, ok)
			}
			if GenerateVirtualMachineSlug(&vm) != image {
				t.Fatal("instance changed image identity")
			}
		}
		vm.Name = ""
		parsed, ok := ParseInstanceName(legacy, vm)
		if !ok || parsed.Name != "" {
			t.Fatalf("legacy %q not found", legacy)
		}
	}
}

func TestValidateInstanceName(t *testing.T) {
	for _, name := range []string{"default", "all", "A", "-a", "a-", "a_b", "../x", "a/b", strings.Repeat("a", 25), "a\n"} {
		if ValidateInstanceName(name) == nil {
			t.Errorf("accepted %q", name)
		}
	}
	for _, name := range []string{"", "a", "0", "a-b", strings.Repeat("1", 24)} {
		if err := ValidateInstanceName(name); err != nil {
			t.Error(err)
		}
	}
	t.Setenv("TART_MACOS_VM_NAME", "custom")
	if ValidateInstance(VirtualMachineConfig{Name: "work", VirtualizationEngine: VirtualizationEngineTart}) == nil {
		t.Fatal("accepted Tart override with named instance")
	}
}

func TestInstanceHostnamesFitGuestLimits(t *testing.T) {
	vm := VirtualMachineConfig{OS: "windows11", Arch: "amd64", Name: strings.Repeat("a", 24)}
	a := InstanceHostname(vm)
	vm.Name = "b"
	b := InstanceHostname(vm)
	if len(a) > 15 || len(b) > 15 || a == b {
		t.Fatal(a, b)
	}
	vm.OS = "ubuntu"
	vm.UbuntuType = "desktop"
	vm.Name = strings.Repeat("a", 24)
	if name := InstanceHostname(vm); len(name) > 63 {
		t.Fatal(name)
	}
}
