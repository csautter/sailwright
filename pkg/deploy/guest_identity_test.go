package deploy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestNamedGuestHostnameUsesProviderIdentity(t *testing.T) {
	old := runGuestIdentityCommand
	defer func() { runGuestIdentityCommand = old }()
	vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", Name: "work", VirtualizationEngine: build.VirtualizationEngineQemu}
	calls := 0
	runGuestIdentityCommand = func(_ string, _ time.Duration, executable string, args []string) (string, error) {
		calls++
		if executable != "virsh" || args[3] != build.InstanceName(vm) {
			t.Fatal(executable, args)
		}
		var payload struct {
			Execute   string         `json:"execute"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(args[4]), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Execute == "guest-exec" {
			if !strings.Contains(args[4], "hostnamectl set-hostname ubuntu-server-amd64-work-dev-alchemy") {
				t.Fatal(args[4])
			}
			return `{"return":{"pid":12}}`, nil
		}
		return `{"return":{"exited":true,"exitcode":0}}`, nil
	}
	if err := ConfigureNamedGuestHostname(vm); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	vm.Name = ""
	if err := ConfigureNamedGuestHostname(vm); err != nil || calls != 2 {
		t.Fatal("default guest was changed", err)
	}
}

func TestNamedUTMGuestHostnameCommand(t *testing.T) {
	old := runGuestIdentityCommand
	defer func() { runGuestIdentityCommand = old }()
	vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "desktop", Arch: "arm64", Name: "work", VirtualizationEngine: build.VirtualizationEngineUtm}
	runGuestIdentityCommand = func(_ string, _ time.Duration, executable string, args []string) (string, error) {
		if !strings.HasSuffix(executable, "utmctl") || args[1] != build.InstanceName(vm) || !strings.Contains(strings.Join(args, " "), build.InstanceHostname(vm)) {
			t.Fatal(executable, args)
		}
		return "", nil
	}
	if err := ConfigureNamedGuestHostname(vm); err != nil {
		t.Fatal(err)
	}
}
