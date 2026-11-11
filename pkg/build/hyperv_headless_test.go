package build

import (
	"slices"
	"testing"
)

// The hyperv-iso Packer plugin opens a vmconnect console unless its headless
// option is set, so --headless must reach both Hyper-V templates.
func TestHypervPackerArgsPassHeadless(t *testing.T) {
	for _, headless := range []bool{false, true} {
		config := VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", Headless: headless}
		ubuntu := hypervUbuntuPackerArgs(config)
		windows := buildPackerArgs(VirtualMachineConfig{OS: "windows11", Arch: "amd64", Headless: headless}, hypervPackerFile)
		for name, args := range map[string][]string{"ubuntu": ubuntu, "windows11": windows} {
			if got := slices.Contains(args, "headless=true"); got != headless {
				t.Errorf("%s headless=%v: args %v", name, headless, args)
			}
		}
		if ubuntu[len(ubuntu)-1] != ubuntuHypervPackerFile {
			t.Errorf("template must stay the last argument: %v", ubuntu)
		}
	}
	// The VirtualBox template declares no headless variable; packer would reject it.
	if args := buildPackerArgs(VirtualMachineConfig{OS: "windows11", Arch: "amd64", Headless: true}, virtualBoxPackerFile); slices.Contains(args, "headless=true") {
		t.Errorf("VirtualBox args: %v", args)
	}
}
