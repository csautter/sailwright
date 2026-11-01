package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	"github.com/csautter/dev-alchemy/pkg/provision"
)

func TestVMInfoOutputsAndFailures(t *testing.T) {
	old := inspectVMInfo
	defer func() { inspectVMInfo = old }()
	inspectVMInfo = func(vm alchemy_build.VirtualMachineConfig) (provision.VMInfo, error) {
		return provision.VMInfo{SchemaVersion: 1, OS: vm.OS, Type: vm.UbuntuType, Arch: vm.Arch, Address: "192.0.2.10", Protocol: "ssh", Port: 22, Username: "packer", Password: "guest-secret", State: "running"}, nil
	}
	for _, jsonOutput := range []bool{false, true} {
		command := newVMCommand()
		var out bytes.Buffer
		command.SetOut(&out)
		args := []string{"info", "ubuntu", "--type", "server", "--arch", "amd64"}
		if jsonOutput {
			args = append(args, "--json")
		}
		command.SetArgs(args)
		if err := command.Execute(); err != nil {
			t.Fatal(err)
		}
		if jsonOutput {
			var value provision.VMInfo
			if err := json.Unmarshal(out.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			if value.SchemaVersion != 1 || value.Password != "guest-secret" || value.Address != "192.0.2.10" {
				t.Fatalf("unexpected info: %+v", value)
			}
		} else if strings.Contains(out.String(), "guest-secret") || !strings.Contains(out.String(), "Password: ********") {
			t.Fatal(out.String())
		}
	}
	command := newVMCommand()
	var shown bytes.Buffer
	command.SetOut(&shown)
	command.SetArgs([]string{"info", "ubuntu", "--show-password"})
	if err := command.Execute(); err != nil || !strings.Contains(shown.String(), "Password: guest-secret") {
		t.Fatalf("--show-password: %v %s", err, shown.String())
	}
	inspectVMInfo = func(alchemy_build.VirtualMachineConfig) (provision.VMInfo, error) {
		return provision.VMInfo{}, errors.New("VM is not running")
	}
	command = newVMCommand()
	command.SetArgs([]string{"info", "ubuntu", "--json"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("stopped VM: %v", err)
	}
	command = newVMCommand()
	command.SetArgs([]string{"info", "unknown"})
	if err := command.Execute(); err == nil {
		t.Fatal("unknown VM accepted")
	}
}
