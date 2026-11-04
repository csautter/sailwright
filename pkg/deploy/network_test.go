package deploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestNetworkModeParsers(t *testing.T) {
	for _, value := range []string{"Shared", "Emulated", "Host", "Bridged", "unexpected"} {
		want := "nat"
		if value == "Bridged" {
			want = "bridged"
		}
		if value == "unexpected" {
			want = "unknown"
		}
		plist := `<plist><dict><key>Unrelated</key><dict><key>Mode</key><string>Bridged</string></dict><key>Network</key><array><dict><key>Mode</key><string>` + value + `</string></dict></array></dict></plist>`
		if got := utmNetworkMode([]byte(plist)); got.Mode != want {
			t.Errorf("UTM %s: %+v", value, got)
		}
	}
	for _, value := range []string{"nat", "bridge", "open", "route", "", "unexpected"} {
		want := "bridged"
		if value == "nat" || value == "" {
			want = "nat"
		}
		if value == "unexpected" {
			want = "unknown"
		}
		if got := libvirtNetworkMode(`<network><forward mode="` + value + `"/></network>`); got.Mode != want {
			t.Errorf("libvirt %s: %+v", value, got)
		}
	}
	for _, value := range []string{"External", "Internal", "Private", "unexpected"} {
		want := "nat"
		if value == "External" {
			want = "bridged"
		}
		if value == "unexpected" {
			want = "unknown"
		}
		if got := hypervNetworkMode(`[{"Name":"test","SwitchType":"` + value + `"}]`); got.Mode != want {
			t.Errorf("Hyper-V %s: %+v", value, got)
		}
	}
	for _, got := range []NetworkMode{utmNetworkMode([]byte("invalid")), utmNetworkMode([]byte(`<plist><dict/></plist>`)), hypervNetworkMode(`[]`), hypervNetworkMode(`invalid`), libvirtNetworkMode(`invalid`), libvirtNetworkMode(`<domain/>`)} {
		if got.Mode != "unknown" {
			t.Errorf("invalid or empty input must be unknown: %+v", got)
		}
	}
}

func TestMultipleNetworkAdapters(t *testing.T) {
	for _, modes := range [][]NetworkMode{
		{{"nat", "a"}, {"bridged", "b"}}, {{"bridged", "a"}, {"unknown", "b"}}, {{"unknown", "a"}, {"bridged", "b"}},
	} {
		if got := combineNetworkModes(modes); got.Mode != "bridged" {
			t.Errorf("bridge must dominate: %+v", got)
		}
	}
	if got := combineNetworkModes([]NetworkMode{{"nat", "a"}, {"unknown", "b"}}); got.Mode != "unknown" {
		t.Fatal(got)
	}
	plist := `<plist><dict><key>Network</key><array><dict><key>Mode</key><string>Shared</string></dict><dict><key>Mode</key><string>Bridged</string></dict></array></dict></plist>`
	if got := utmNetworkMode([]byte(plist)); got.Mode != "bridged" {
		t.Fatal(got)
	}
	if got := hypervNetworkMode(`[{"SwitchType":"Internal"},{"SwitchType":"External"}]`); got.Mode != "bridged" {
		t.Fatal(got)
	}
}

func TestTartProcessNetworkMode(t *testing.T) {
	for _, tc := range []struct {
		output, name, want string
		found              bool
	}{
		{"/opt/homebrew/bin/tart run --net-bridged=en0 test", "test", "bridged", true},
		{"tart run --net-bridged en0 test", "test", "bridged", true},
		{"tart run --net-softnet test", "test", "nat", true},
		{"tart run test", "test", "nat", true},
		{"tart run --net-bridged=en0 vm with spaces", "vm with spaces", "bridged", true},
		{"tart run --net-bridged=en0 other-test", "test", "", false},
		{"sh -c tart run --net-bridged=en0 test", "test", "", false},
	} {
		got, found := tartProcessNetworkMode(tc.output, tc.name)
		if found != tc.found || got.Mode != tc.want {
			t.Errorf("%q: %+v, %t", tc.output, got, found)
		}
	}
}

func TestLibvirtNetworkIPv6Exposure(t *testing.T) {
	for _, tc := range []struct {
		name, forward, ips, want string
	}{
		{"IPv4 NAT", `<forward mode="nat"/>`, `<ip address="192.168.122.1"/>`, "nat"},
		{"routed IPv6", `<forward mode="nat"/>`, `<ip family="ipv6" address="2001:db8::1"/>`, "bridged"},
		{"dual stack routed IPv6", `<forward mode="nat"/>`, `<ip address="192.168.122.1"/><ip family="ipv6" address="2001:db8::1"/>`, "bridged"},
		{"explicit IPv6 NAT", `<forward mode="nat"><nat ipv6="yes"/></forward>`, `<ip family="ipv6" address="fd00::1"/>`, "nat"},
		{"explicit IPv6 routing", `<forward mode="nat"><nat ipv6="no"/></forward>`, `<ip family="ipv6" address="fd00::1"/>`, "bridged"},
		{"isolated IPv6", "", `<ip family="ipv6" address="fd00::1"/>`, "nat"},
		{"implicit NAT routed IPv6", `<forward/>`, `<ip family="ipv6" address="2001:db8::1"/>`, "bridged"},
		{"routed network", `<forward mode="route"/>`, `<ip family="ipv6" address="2001:db8::1"/>`, "bridged"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := libvirtNetworkMode("<network>" + tc.forward + tc.ips + "</network>"); got.Mode != tc.want {
				t.Fatalf("got %+v, want %s", got, tc.want)
			}
		})
	}
}

func TestInspectNetworkModeWithFakeCommands(t *testing.T) {
	dirs := alchemy_build.GetDirectoriesInstance()
	oldDir := dirs.ProjectDir
	oldRun := runNetworkCommand
	t.Cleanup(func() { dirs.ProjectDir = oldDir; runNetworkCommand = oldRun })
	dirs.ProjectDir = t.TempDir()
	vm := alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", VirtualizationEngine: alchemy_build.VirtualizationEngineQemu}
	// Use the actual configuration variable, independently of the host's libvirt setup.
	t.Setenv(linuxLibvirtURIEnvVar, "qemu:///system")
	var calls []string
	runNetworkCommand = func(_ string, _ time.Duration, executable string, args []string) (string, error) {
		call := executable + " " + strings.Join(args, " ")
		calls = append(calls, call)
		switch {
		case strings.Contains(call, " net-dumpxml default"):
			return `<network><forward mode="nat"/></network>`, nil
		case strings.Contains(call, " net-dumpxml exposed"):
			return `<network><forward mode="route"/></network>`, nil
		case strings.Contains(call, " dumpxml "):
			return `<domain><devices><interface type="network"><source network="exposed"/></interface></devices></domain>`, nil
		case executable == "powershell":
			return `[{"Name":"External ' test","SwitchType":"External"}]`, nil
		case executable == "ps":
			return "tart run --net-bridged=en0 " + tartMacOSVMName(vm), nil
		}
		return "", fmt.Errorf("unexpected fake command: %s", call)
	}
	if got := InspectNetworkMode(vm, true); got.Mode != "nat" {
		t.Fatal(got)
	}
	if got := InspectNetworkMode(vm, false); got.Mode != "bridged" {
		t.Fatal(got)
	}
	t.Setenv(linuxLibvirtURIEnvVar, "qemu:///session")
	before := len(calls)
	if got := InspectNetworkMode(vm, true); got.Mode != "nat" || len(calls) != before {
		t.Fatal(got)
	}
	vm.VirtualizationEngine = alchemy_build.VirtualizationEngineHyperv
	t.Setenv("VAGRANT_HYPERV_SWITCH", "External ' test")
	if got := InspectNetworkMode(vm, true); got.Mode != "bridged" {
		t.Fatal(got)
	}
	if !strings.Contains(calls[len(calls)-1], "'External '' test'") {
		t.Fatal("switch name must be quoted")
	}
	if got := InspectNetworkMode(vm, false); got.Mode != "bridged" {
		t.Fatal(got)
	}
	if !strings.Contains(calls[len(calls)-1], "Get-VMNetworkAdapter -VMName") {
		t.Fatal(calls)
	}
	vm.VirtualizationEngine = alchemy_build.VirtualizationEngineTart
	t.Setenv(tartMacOSBridgedInterfaceEnvVar, "")
	if got := InspectNetworkMode(vm, true); got.Mode != "nat" {
		t.Fatal(got)
	}
	if got := InspectNetworkMode(vm, false); got.Mode != "bridged" {
		t.Fatal(got)
	}
	t.Setenv(tartMacOSBridgedInterfaceEnvVar, "en0")
	if got := InspectNetworkMode(vm, true); got.Mode != "bridged" {
		t.Fatal(got)
	}
	vm.VirtualizationEngine = alchemy_build.VirtualizationEngineUtm
	file := filepath.Join(dirs.ProjectDir, "deployments", "utm", "ubuntu-amd64", "config.plist")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`<plist><dict><key>Network</key><array><dict><key>Mode</key><string>Bridged</string></dict></array></dict></plist>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := InspectNetworkMode(vm, true); got.Mode != "bridged" {
		t.Fatal(got)
	}
	// Failures must never silently become NAT.
	runNetworkCommand = func(string, time.Duration, string, []string) (string, error) { return "", errors.New("unavailable") }
	for _, engine := range []alchemy_build.VirtualizationEngine{alchemy_build.VirtualizationEngineQemu, alchemy_build.VirtualizationEngineHyperv, alchemy_build.VirtualizationEngineTart, alchemy_build.VirtualizationEngineUtm} {
		vm.VirtualizationEngine = engine
		if got := InspectNetworkMode(vm, false); got.Mode != "unknown" {
			t.Fatalf("%s: %+v", engine, got)
		}
	}
}

func TestUtmNetworkReadsExistingAndBinaryPlists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	vm := alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "desktop", Arch: "arm64", VirtualizationEngine: alchemy_build.VirtualizationEngineUtm}
	bundle, err := utmVirtualMachinePath(vm)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(bundle, "config.plist")
	plist := `<plist><dict><key>Network</key><array><dict><key>Mode</key><string>Shared</string></dict></array></dict></plist>`
	if err := os.WriteFile(file, []byte(plist), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := InspectNetworkMode(vm, false); got.Mode != "nat" {
		t.Fatal(got)
	}
	oldRun := runNetworkCommand
	t.Cleanup(func() { runNetworkCommand = oldRun })
	if err := os.WriteFile(file, []byte("bplist00-fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	runNetworkCommand = func(_ string, _ time.Duration, executable string, args []string) (string, error) {
		if executable != "plutil" || args[len(args)-1] != file {
			t.Fatalf("unexpected command: %s %v", executable, args)
		}
		return plist, nil
	}
	if got := InspectNetworkMode(vm, false); got.Mode != "nat" {
		t.Fatal(got)
	}
}

func TestTartStoppedAndUnobservableRunningNetwork(t *testing.T) {
	oldRun := runNetworkCommand
	t.Cleanup(func() { runNetworkCommand = oldRun })
	t.Setenv(tartMacOSBridgedInterfaceEnvVar, "")
	vm := alchemy_build.VirtualMachineConfig{OS: "macos", Arch: "arm64", VirtualizationEngine: alchemy_build.VirtualizationEngineTart}
	for _, running := range []bool{false, true} {
		runNetworkCommand = func(_ string, _ time.Duration, executable string, _ []string) (string, error) {
			if executable == "ps" {
				return "", nil
			}
			return fmt.Sprintf(`[{"Source":"local","Name":%q,"Running":%t}]`, tartMacOSVMName(vm), running), nil
		}
		want := "nat"
		if running {
			want = "unknown"
		}
		if got := InspectNetworkMode(vm, false); got.Mode != want {
			t.Fatalf("running=%t: %+v", running, got)
		}
	}
}
