package deploy

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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
		{"tart run --net-softnet --no-graphics test", "test", "nat", true},
		{"tart run test", "test", "nat", true},
		{"tart run vm with spaces", "vm with spaces", "nat", true},
		{"tart run --net-bridged=en0 vm with spaces", "vm with spaces", "bridged", true},
		{"tart run --net-bridged=en0 other-test", "test", "", false},
		{"sh -c tart run --net-bridged=en0 test", "test", "", false},
		{"tart run --net-fd=3 test", "test", "unknown", true},
		{"tart run --net-fd 3 test", "test", "unknown", true},
		{"tart run --net-future=custom test", "test", "unknown", true},
		{"tart run --disk other test", "test", "unknown", true},
		{"tart run other test", "test", "unknown", true},
		{"tart run other test\ntart run --net-bridged=en0 test", "test", "bridged", true},
		{"tart run --net-bridged=en0 test\ntart run other test", "test", "bridged", true},
		{"tart run other test\ntart run test", "test", "unknown", true},
		{"tart run test\ntart run other test", "test", "unknown", true},
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

func TestUTMEmulatedPortForwardExposure(t *testing.T) {
	forward := func(key, value string) string {
		return `<dict><key>Protocol</key><string>TCP</string><key>HostPort</key><integer>2222</integer><key>GuestPort</key><integer>22</integer><key>` + key + `</key>` + value + `</dict>`
	}
	for _, tc := range []struct {
		name, forwards, want string
	}{
		{"no forwards", "", "nat"},
		{"empty forwards", `<key>PortForward</key><array/>`, "nat"},
		{"IPv4 loopback", `<key>PortForward</key><array>` + forward("HostAddress", `<string>127.0.0.1</string>`) + `</array>`, "nat"},
		{"IPv6 loopback", `<key>PortForward</key><array>` + forward("HostAddress", `<string>::1</string>`) + `</array>`, "nat"},
		{"legacy loopback", `<key>PortForward</key><array>` + forward("hostAddress", `<string>127.0.0.1</string>`) + `</array>`, "nat"},
		{"IPv4 wildcard", `<key>PortForward</key><array>` + forward("HostAddress", `<string>0.0.0.0</string>`) + `</array>`, "unknown"},
		{"IPv6 wildcard", `<key>PortForward</key><array>` + forward("HostAddress", `<string>::</string>`) + `</array>`, "unknown"},
		{"LAN address", `<key>PortForward</key><array>` + forward("HostAddress", `<string>192.168.1.10</string>`) + `</array>`, "unknown"},
		{"legacy LAN address", `<key>PortForward</key><array>` + forward("hostAddress", `<string>192.168.1.10</string>`) + `</array>`, "unknown"},
		{"empty address", `<key>PortForward</key><array>` + forward("HostAddress", `<string/>`) + `</array>`, "unknown"},
		{"missing address", `<key>PortForward</key><array><dict><key>GuestPort</key><integer>22</integer></dict></array>`, "unknown"},
		{"hostname", `<key>PortForward</key><array>` + forward("HostAddress", `<string>localhost</string>`) + `</array>`, "unknown"},
		{"invalid address type", `<key>PortForward</key><array>` + forward("HostAddress", `<integer>127</integer>`) + `</array>`, "unknown"},
		{"invalid forwards type", `<key>PortForward</key><string/>`, "unknown"},
		{"invalid forward entry", `<key>PortForward</key><array><string/></array>`, "unknown"},
		{"mixed bindings", `<key>PortForward</key><array>` + forward("HostAddress", `<string>127.0.0.1</string>`) + forward("HostAddress", `<string>0.0.0.0</string>`) + `</array>`, "unknown"},
		{"conflicting address aliases", `<key>PortForward</key><array><dict><key>HostAddress</key><string>127.0.0.1</string><key>hostAddress</key><string>0.0.0.0</string></dict></array>`, "unknown"},
		{"duplicate addresses", `<key>PortForward</key><array><dict><key>HostAddress</key><string>0.0.0.0</string><key>HostAddress</key><string>127.0.0.1</string></dict></array>`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plist := `<plist><dict><key>Network</key><array><dict><key>Mode</key><string>Emulated</string>` + tc.forwards + `</dict></array></dict></plist>`
			if got := utmNetworkMode([]byte(plist)); got.Mode != tc.want {
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
	t.Setenv(UtmNetworkModeEnvVar, "")
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

func TestLibvirtUserPortForwardExposure(t *testing.T) {
	oldRun := runNetworkCommand
	t.Cleanup(func() { runNetworkCommand = oldRun })
	vm := alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", VirtualizationEngine: alchemy_build.VirtualizationEngineQemu}
	for _, tc := range []struct{ name, forwards, want string }{
		{"no forwarding", "", "nat"},
		{"loopback IPv4", `<portForward proto="tcp" address="127.0.0.1"><range start="2222" to="22"/></portForward>`, "nat"},
		{"loopback IPv6", `<portForward proto="udp" address="::1"/>`, "nat"},
		{"wildcard", `<portForward proto="tcp" address="0.0.0.0"/>`, "unknown"},
		{"missing address", `<portForward proto="tcp"><range start="2222" to="22"/></portForward>`, "unknown"},
		{"LAN", `<portForward proto="tcp" address="192.168.1.10"/>`, "unknown"},
		{"hostname", `<portForward proto="tcp" address="localhost"/>`, "unknown"},
		{"mixed", `<portForward proto="tcp" address="127.0.0.1"/><portForward proto="udp"/>`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runNetworkCommand = func(string, time.Duration, string, []string) (string, error) {
				return `<domain><devices><interface type="user"><backend type="passt"/>` + tc.forwards + `</interface></devices></domain>`, nil
			}
			if got := InspectNetworkMode(vm, false); got.Mode != tc.want {
				t.Fatalf("got %+v, want %s", got, tc.want)
			}
		})
	}
}

func TestShippedUtmTemplatesDefaultToSharedNat(t *testing.T) {
	t.Setenv(UtmNetworkModeEnvVar, "")
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dirs := alchemy_build.GetDirectoriesInstance()
	oldDir := dirs.ProjectDir
	t.Cleanup(func() { dirs.ProjectDir = oldDir })
	dirs.ProjectDir = repoRoot

	for _, template := range []string{"ubuntu-amd64", "ubuntu-arm64", "windows11-amd64", "windows11-arm64"} {
		t.Run("template/"+template, func(t *testing.T) {
			path := filepath.Join(repoRoot, "deployments", "utm", template, "config.plist")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			got := utmNetworkMode(data)
			if got.Mode != "nat" || got.Detail != "UTM Shared" {
				t.Fatalf("%s must default to Shared (nat), got %+v", template, got)
			}
			if strings.Contains(string(data), "<string>Bridged</string>") {
				t.Fatalf("%s must not contain Bridged mode by default", template)
			}
			if !strings.Contains(string(data), "<key>IsolateFromHost</key>\n\t\t\t<false/>") {
				t.Fatalf("%s must keep IsolateFromHost=false so host SSH/WinRM reaches Shared guests", template)
			}
		})
	}

	targets := []alchemy_build.VirtualMachineConfig{
		{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", HostOs: alchemy_build.HostOsDarwin, VirtualizationEngine: alchemy_build.VirtualizationEngineUtm},
		{OS: "ubuntu", UbuntuType: "server", Arch: "arm64", HostOs: alchemy_build.HostOsDarwin, VirtualizationEngine: alchemy_build.VirtualizationEngineUtm},
		{OS: "ubuntu", UbuntuType: "desktop", Arch: "amd64", HostOs: alchemy_build.HostOsDarwin, VirtualizationEngine: alchemy_build.VirtualizationEngineUtm},
		{OS: "ubuntu", UbuntuType: "desktop", Arch: "arm64", HostOs: alchemy_build.HostOsDarwin, VirtualizationEngine: alchemy_build.VirtualizationEngineUtm},
		{OS: "windows11", Arch: "amd64", HostOs: alchemy_build.HostOsDarwin, VirtualizationEngine: alchemy_build.VirtualizationEngineUtm},
		{OS: "windows11", Arch: "arm64", HostOs: alchemy_build.HostOsDarwin, VirtualizationEngine: alchemy_build.VirtualizationEngineUtm},
	}
	for _, vm := range targets {
		got := InspectNetworkMode(vm, true)
		if got.Mode != "nat" {
			t.Errorf("planned network for %s/%s/%s: got %+v, want nat", vm.OS, vm.UbuntuType, vm.Arch, got)
		}
	}
}

func TestUtmPlannedNetworkModeOverride(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dirs := alchemy_build.GetDirectoriesInstance()
	oldDir := dirs.ProjectDir
	t.Cleanup(func() { dirs.ProjectDir = oldDir })
	dirs.ProjectDir = repoRoot

	vm := alchemy_build.VirtualMachineConfig{
		OS:                   "ubuntu",
		UbuntuType:           "server",
		Arch:                 "arm64",
		HostOs:               alchemy_build.HostOsDarwin,
		VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
	}

	for _, tc := range []struct {
		name     string
		envValue string
		wantMode string
	}{
		{"default unset", "", "nat"},
		{"explicit shared lowercase", "shared", "nat"},
		{"explicit shared mixed case", " Shared ", "nat"},
		{"explicit bridged lowercase", "bridged", "bridged"},
		{"explicit bridged mixed case", " Bridged ", "bridged"},
		{"invalid mode fails closed", "host", "unknown"},
		{"arbitrary value fails closed", "lan", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(UtmNetworkModeEnvVar, tc.envValue)
			if got := InspectNetworkMode(vm, true); got.Mode != tc.wantMode {
				t.Fatalf("env=%q: got %+v, want %s", tc.envValue, got, tc.wantMode)
			}
		})
	}

	// Existing VMs must always be inspected from their on-disk config.plist,
	// regardless of SAILWRIGHT_UTM_NETWORK_MODE.
	t.Run("existing VM ignores env override", func(t *testing.T) {
		utmDir := t.TempDir()
		t.Setenv("SAILWRIGHT_UTM_VM_DIR", utmDir)
		bundle, err := utmVirtualMachinePath(vm)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(bundle, 0o700); err != nil {
			t.Fatal(err)
		}
		plistPath := filepath.Join(bundle, "config.plist")

		if err := os.WriteFile(plistPath, []byte(`<plist><dict><key>Network</key><array><dict><key>Mode</key><string>Shared</string></dict></array></dict></plist>`), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(UtmNetworkModeEnvVar, "bridged")
		if got := InspectNetworkMode(vm, false); got.Mode != "nat" {
			t.Fatalf("existing Shared VM must remain nat even when env=bridged: %+v", got)
		}

		if err := os.WriteFile(plistPath, []byte(`<plist><dict><key>Network</key><array><dict><key>Mode</key><string>Bridged</string></dict></array></dict></plist>`), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(UtmNetworkModeEnvVar, "shared")
		if got := InspectNetworkMode(vm, false); got.Mode != "bridged" {
			t.Fatalf("existing Bridged VM must remain bridged even when env=shared: %+v", got)
		}
	})
}

func TestRunUtmDeployOnMacOSNetworkMode(t *testing.T) {
	oldRun := runUtmDeployCommandWithEnv
	t.Cleanup(func() { runUtmDeployCommandWithEnv = oldRun })

	vm := alchemy_build.VirtualMachineConfig{
		OS:                   "ubuntu",
		UbuntuType:           "server",
		Arch:                 "arm64",
		HostOs:               alchemy_build.HostOsDarwin,
		VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
	}
	t.Setenv("SAILWRIGHT_UTM_VM_DIR", t.TempDir())

	t.Run("default omits network flag", func(t *testing.T) {
		t.Setenv(UtmNetworkModeEnvVar, "")
		var gotArgs []string
		runUtmDeployCommandWithEnv = func(_ string, _ time.Duration, _ string, args []string, _ []string, _ string) error {
			gotArgs = append([]string(nil), args...)
			return nil
		}
		if err := RunUtmDeployOnMacOS(vm); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(gotArgs, "--network") {
			t.Fatalf("default UTM deploy should not pass --network, got %v", gotArgs)
		}
	})

	t.Run("bridged passes --network bridged", func(t *testing.T) {
		t.Setenv(UtmNetworkModeEnvVar, "bridged")
		var gotArgs []string
		runUtmDeployCommandWithEnv = func(_ string, _ time.Duration, _ string, args []string, _ []string, _ string) error {
			gotArgs = append([]string(nil), args...)
			return nil
		}
		if err := RunUtmDeployOnMacOS(vm); err != nil {
			t.Fatal(err)
		}
		idx := slices.Index(gotArgs, "--network")
		if idx < 0 || idx+1 >= len(gotArgs) || gotArgs[idx+1] != "bridged" {
			t.Fatalf("expected --network bridged in %v", gotArgs)
		}
	})

	t.Run("shared passes --network shared", func(t *testing.T) {
		t.Setenv(UtmNetworkModeEnvVar, "Shared")
		var gotArgs []string
		runUtmDeployCommandWithEnv = func(_ string, _ time.Duration, _ string, args []string, _ []string, _ string) error {
			gotArgs = append([]string(nil), args...)
			return nil
		}
		if err := RunUtmDeployOnMacOS(vm); err != nil {
			t.Fatal(err)
		}
		idx := slices.Index(gotArgs, "--network")
		if idx < 0 || idx+1 >= len(gotArgs) || gotArgs[idx+1] != "shared" {
			t.Fatalf("expected --network shared in %v", gotArgs)
		}
	})

	t.Run("invalid value fails before running script", func(t *testing.T) {
		t.Setenv(UtmNetworkModeEnvVar, "bogus")
		called := false
		runUtmDeployCommandWithEnv = func(_ string, _ time.Duration, _ string, _ []string, _ []string, _ string) error {
			called = true
			return nil
		}
		err := RunUtmDeployOnMacOS(vm)
		if err == nil || !strings.Contains(err.Error(), UtmNetworkModeEnvVar) {
			t.Fatalf("expected error naming %s, got %v", UtmNetworkModeEnvVar, err)
		}
		if called {
			t.Fatal("invalid network mode must fail before executing create-utm-vm.sh")
		}
	})
}

func TestCreateUtmVmScriptNetworkModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping bash script test on Windows")
	}
	for _, bin := range []string{"bash", "envsubst", "awk", "rsync"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("skipping create-utm-vm.sh execution test: %s not on PATH", bin)
		}
	}
	pathEnv := os.Getenv("PATH")
	if _, err := exec.LookPath("uuidgen"); err != nil {
		stubDir := t.TempDir()
		stubPath := filepath.Join(stubDir, "uuidgen")
		stubScript := "#!/usr/bin/env bash\ncat /proc/sys/kernel/random/uuid 2>/dev/null || echo 11111111-2222-3333-4444-555555555555\n"
		if err := os.WriteFile(stubPath, []byte(stubScript), 0o700); err != nil {
			t.Fatal(err)
		}
		pathEnv = stubDir + string(os.PathListSeparator) + pathEnv
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repoRoot, "deployments", "utm", "create-utm-vm.sh")

	cacheDir := t.TempDir()
	diskPath := filepath.Join(cacheDir, "ubuntu", "qemu-ubuntu-server-packer-arm64.qcow2")
	if err := os.MkdirAll(filepath.Dir(diskPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(diskPath, []byte("fake-qcow2"), 0o600); err != nil {
		t.Fatal(err)
	}

	runScript := func(utmDir string, extraEnv []string, extraArgs ...string) (string, error) {
		args := append([]string{script, "--arch", "arm64", "--os", "ubuntu-server"}, extraArgs...)
		cmd := exec.Command("bash", args...)
		env := append(os.Environ(),
			"PATH="+pathEnv,
			"DEV_ALCHEMY_CACHE_DIR="+cacheDir,
			"SAILWRIGHT_UTM_VM_DIR="+utmDir,
			UtmNetworkModeEnvVar+"=",
		)
		env = append(env, extraEnv...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// Default creates Shared (nat).
	defaultDir := t.TempDir()
	if out, err := runScript(defaultDir, nil); err != nil {
		t.Fatalf("default create failed: %v\n%s", err, out)
	}
	defaultPlist := filepath.Join(defaultDir, "ubuntu-server-arm64-dev-alchemy.utm", "config.plist")
	data, err := os.ReadFile(defaultPlist)
	if err != nil {
		t.Fatal(err)
	}
	if got := utmNetworkMode(data); got.Mode != "nat" || got.Detail != "UTM Shared" {
		t.Fatalf("default bundle must be Shared (nat), got %+v", got)
	}

	// Existing bundle must NOT be modified when invoked again with --network bridged.
	if out, err := runScript(defaultDir, nil, "--network", "bridged"); err != nil {
		t.Fatalf("re-run on existing bundle failed: %v\n%s", err, out)
	}
	afterData, err := os.ReadFile(defaultPlist)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterData) != string(data) {
		t.Fatal("existing UTM config.plist was modified on re-run")
	}

	// Explicit --network bridged produces Bridged.
	bridgedFlagDir := t.TempDir()
	if out, err := runScript(bridgedFlagDir, nil, "--network", "bridged"); err != nil {
		t.Fatalf("--network bridged failed: %v\n%s", err, out)
	}
	bridgedData, err := os.ReadFile(filepath.Join(bridgedFlagDir, "ubuntu-server-arm64-dev-alchemy.utm", "config.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if got := utmNetworkMode(bridgedData); got.Mode != "bridged" || got.Detail != "UTM Bridged" {
		t.Fatalf("--network bridged bundle must be Bridged, got %+v", got)
	}

	// Explicit SAILWRIGHT_UTM_NETWORK_MODE=bridged produces Bridged.
	bridgedEnvDir := t.TempDir()
	if out, err := runScript(bridgedEnvDir, []string{UtmNetworkModeEnvVar + "=bridged"}); err != nil {
		t.Fatalf("env bridged failed: %v\n%s", err, out)
	}
	bridgedEnvData, err := os.ReadFile(filepath.Join(bridgedEnvDir, "ubuntu-server-arm64-dev-alchemy.utm", "config.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if got := utmNetworkMode(bridgedEnvData); got.Mode != "bridged" || got.Detail != "UTM Bridged" {
		t.Fatalf("env bridged bundle must be Bridged, got %+v", got)
	}

	// Invalid network mode fails non-zero.
	invalidDir := t.TempDir()
	if _, err := runScript(invalidDir, nil, "--network", "invalid"); err == nil {
		t.Fatal("expected invalid --network to fail")
	}
}
