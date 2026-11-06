package provision

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestDiscoverLibvirtNetworkMode(t *testing.T) {
	for _, tc := range []struct {
		name, uri, interfaces, network, want string
		fail                                 bool
	}{
		{"system default NAT", "qemu:///system", `<interface type="network"><source network="default"/></interface>`, `<network><forward mode="nat"/></network>`, "nat", false},
		{"default network bridged", "qemu:///system", `<interface type="network"><source network="default"/></interface>`, `<network><forward mode="bridge"/></network>`, "bridged", false},
		{"default network routed", "qemu:///system", `<interface type="network"><source network="default"/></interface>`, `<network><forward mode="route"/></network>`, "unknown", false},
		{"session network is not necessarily user", "qemu:///session", `<interface type="bridge"/>`, "", "bridged", false},
		{"actual user interface", "qemu:///session", `<interface type="user"/>`, "", "user", false},
		{"mixed NICs", "qemu:///system", `<interface type="network"><source network="default"/></interface><interface type="bridge"/>`, `<network><forward mode="nat"/></network>`, "unknown", false},
		{"isolated network", "qemu:///system", `<interface type="network"><source network="default"/></interface>`, `<network/>`, "unknown", false},
		{"network query fails", "qemu:///system", `<interface type="network"><source network="default"/></interface>`, "", "unknown", true},
		{"malformed network XML", "qemu:///system", `<interface type="network"><source network="default"/></interface>`, `<network`, "unknown", false},
		{"no interfaces", "qemu:///system", "", "", "unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(project string, timeout time.Duration, executable string, args []string) (string, error) {
				if project != "/project" || timeout <= 0 || executable != "virsh" {
					t.Fatalf("unexpected command: %s %s", project, executable)
				}
				if reflect.DeepEqual(args, []string{"--connect", tc.uri, "dumpxml", "guest"}) {
					return "<domain><devices>" + tc.interfaces + "</devices></domain>", nil
				}
				if !reflect.DeepEqual(args, []string{"--connect", tc.uri, "net-dumpxml", "default"}) {
					t.Fatalf("unexpected args: %v", args)
				}
				if tc.fail {
					return "", errors.New("network unavailable")
				}
				return tc.network, nil
			}
			if got := discoverLibvirtNetworkMode("/project", "guest", tc.uri, run); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
	for _, output := range []string{"", "<domain", "<network/>"} {
		run := func(string, time.Duration, string, []string) (string, error) { return output, nil }
		if got := discoverLibvirtNetworkMode("", "guest", "qemu:///system", run); got != "unknown" {
			t.Fatalf("invalid domain %q: %s", output, got)
		}
	}
	run := func(string, time.Duration, string, []string) (string, error) {
		return "", errors.New("domain unavailable")
	}
	if got := discoverLibvirtNetworkMode("", "guest", "qemu:///system", run); got != "unknown" {
		t.Fatal(got)
	}
}
