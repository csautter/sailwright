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
		return "fe80::1\n169.254.1.2\n192.0.2.5\n", nil
	}
	vm := alchemy_build.VirtualMachineConfig{OS: "windows11", Arch: "amd64", VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv}
	ip, err := DiscoverHyperVAddress(vm)
	if err != nil || ip != "192.0.2.5" {
		t.Fatalf("address %s: %v", ip, err)
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
