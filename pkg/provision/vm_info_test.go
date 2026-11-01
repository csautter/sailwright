package provision

import (
	"strings"
	"testing"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestHyperVAddressUsesNativeAdapterQuery(t *testing.T) {
	old := runProvisionCommandWithCombinedOutputWithEnv
	defer func() { runProvisionCommandWithCombinedOutputWithEnv = old }()
	runProvisionCommandWithCombinedOutputWithEnv = func(_ string, _ time.Duration, executable string, args []string, _ []string) (string, error) {
		if executable != "powershell.exe" || !strings.Contains(strings.Join(args, " "), "Get-VMNetworkAdapter -VMName 'win11-packer'") {
			t.Fatalf("unexpected native query: %s %v", executable, args)
		}
		return "WARNING: something unrelated\n" + `[{"switch":"Default Switch","switch_type":"Internal","addresses":["fe80::1","169.254.1.2","192.0.2.5"],"host_prefixes":["192.0.2.1/24"]}]` + "\n", nil
	}
	t.Setenv(hypervSwitchEnvVar, "")
	vm := alchemy_build.VirtualMachineConfig{OS: "windows11", Arch: "amd64", VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv}
	ip, mode, err := discoverHyperVNetwork(vm)
	if err != nil || ip != "192.0.2.5" || mode != "nat" {
		t.Fatalf("address %s mode %s: %v", ip, mode, err)
	}
}

func TestSelectHyperVAddress(t *testing.T) {
	desktopDefault := hypervAdapter{Switch: "Default Switch", SwitchType: "Internal", HostPrefixes: []string{"172.20.16.1/20"},
		// Docker inside the guest reports 172.17.0.1, outside the switch subnet.
		Addresses: []string{"fe80::5", "172.17.0.1", "169.254.3.4", "172.20.20.5"}}
	ciDefault := hypervAdapter{Switch: "Default Switch", SwitchType: "Internal", HostPrefixes: []string{"192.168.0.1/24"}, Addresses: []string{"192.168.0.150"}}
	external := hypervAdapter{Switch: "LAN", SwitchType: "External", HostPrefixes: []string{"192.168.1.10/24"}, Addresses: []string{"192.168.1.50"}}
	for _, tc := range []struct {
		name      string
		adapters  []hypervAdapter
		preferred string
		want      string
		mode      string
		fail      string
	}{
		{"desktop Default Switch skips guest-internal addresses", []hypervAdapter{desktopDefault}, "Default Switch", "172.20.20.5", "nat", ""},
		{"CI internal switch with NAT", []hypervAdapter{ciDefault}, "Default Switch", "192.168.0.150", "nat", ""},
		{"configured switch wins over adapter order", []hypervAdapter{external, ciDefault}, "Default Switch", "192.168.0.150", "nat", ""},
		{"external switch is bridged", []hypervAdapter{external}, "LAN", "192.168.1.50", "bridged", ""},
		{"external switch without host adapter", []hypervAdapter{{Switch: "LAN", SwitchType: "External", Addresses: []string{"198.51.100.7"}}}, "Default Switch", "198.51.100.7", "bridged", ""},
		{"private switch is unreachable", []hypervAdapter{{Switch: "lab", SwitchType: "Private", Addresses: []string{"10.0.0.5"}}}, "Default Switch", "", "", "not reachable from the host"},
		{"spoofed address outside the switch subnet", []hypervAdapter{{Switch: "Default Switch", SwitchType: "Internal", HostPrefixes: []string{"172.20.16.1/20"}, Addresses: []string{"192.168.1.1"}}}, "Default Switch", "", "", "192.168.1.1 outside the host subnet 172.20.16.0/20"},
		{"guest has no address yet", []hypervAdapter{{Switch: "Default Switch", SwitchType: "Internal", Addresses: []string{"fe80::1"}}}, "Default Switch", "", "", "no IPv4 address reported yet"},
		{"no adapters", nil, "Default Switch", "", "", "no network adapter"},
	} {
		got, mode, err := selectHyperVAddress(tc.adapters, tc.preferred)
		if tc.fail != "" {
			if err == nil || !strings.Contains(err.Error(), tc.fail) {
				t.Errorf("%s: expected error with %q, got %v (%s)", tc.name, tc.fail, err, got)
			}
			continue
		}
		if err != nil || got.String() != tc.want || mode != tc.mode {
			t.Errorf("%s: got %s %s %v", tc.name, got, mode, err)
		}
	}
}

func TestParseHypervAdaptersHandlesEmptyList(t *testing.T) {
	adapters, err := parseHypervAdapters("[]\n")
	if err != nil || len(adapters) != 0 {
		t.Fatal(adapters, err)
	}
	if _, err := parseHypervAdapters("Get-VMNetworkAdapter : not found"); err == nil {
		t.Fatal("error output accepted")
	}
}

func TestNetworkModes(t *testing.T) {
	if libvirtNetworkMode("qemu:///system") != "nat" || libvirtNetworkMode("qemu:///session") != "user" {
		t.Fatal("libvirt modes")
	}
	env := map[string]string{}
	getenv := func(name string) string { return env[name] }
	if tartNetworkMode(getenv) != "nat" {
		t.Fatal("tart default")
	}
	env["TART_MACOS_BRIDGED_INTERFACE"] = "en0"
	if tartNetworkMode(getenv) != "bridged" {
		t.Fatal("tart bridged")
	}
}

func TestParseGuestPort(t *testing.T) {
	for _, tc := range []struct {
		value    string
		fallback int
		want     int
		fail     bool
	}{
		{"", 22, 22, false},
		{" 2222 ", 22, 2222, false},
		{"5986", 0, 5986, false},
		{"", 0, 0, true},
		{"winrm", 0, 0, true},
		{"70000", 22, 0, true},
	} {
		got, err := parseGuestPort(tc.value, tc.fallback)
		if (err != nil) != tc.fail || got != tc.want {
			t.Fatalf("parseGuestPort(%q, %d) = %d, %v", tc.value, tc.fallback, got, err)
		}
		if tc.fail && !strings.Contains(err.Error(), tc.value) {
			t.Fatalf("error does not name the value: %v", err)
		}
	}
}

func TestHyperVVagrantFallbackPassesSwitchChecks(t *testing.T) {
	old := runProvisionCommandWithCombinedOutputWithEnv
	defer func() { runProvisionCommandWithCombinedOutputWithEnv = old }()
	vagrantHost := ""
	runProvisionCommandWithCombinedOutputWithEnv = func(_ string, _ time.Duration, executable string, _ []string, _ []string) (string, error) {
		if executable == "powershell.exe" {
			// Integration services have not reported an IPv4 address yet.
			return `[{"switch":"Default Switch","switch_type":"Internal","addresses":["fe80::1"],"host_prefixes":["172.20.16.1/20"]}]`, nil
		}
		return "Host default\n  HostName " + vagrantHost + "\n", nil
	}
	t.Setenv(hypervSwitchEnvVar, "")
	vm := alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv}
	vagrantHost = "172.20.20.9"
	if ip, mode, err := discoverHyperVNetwork(vm); err != nil || ip != "172.20.20.9" || mode != "nat" {
		t.Fatalf("fallback: %s %s %v", ip, mode, err)
	}
	vagrantHost = "192.168.1.1"
	if ip, _, err := discoverHyperVNetwork(vm); err == nil {
		t.Fatalf("address outside the switch subnet accepted: %s", ip)
	}
}
