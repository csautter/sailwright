package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

func TestVMIdentityReportsStoppedAndMissingVMs(t *testing.T) {
	old := inspectVMIdentity
	defer func() { inspectVMIdentity = old }()
	inspectVMIdentity = func(vm alchemy_build.VirtualMachineConfig) (provision.VMIdentity, error) {
		return provision.VMIdentity{SchemaVersion: 1, OS: vm.OS, Type: vm.UbuntuType, Arch: vm.Arch, Exists: true, InstanceID: "8f0c2a1e-3b4d-4c5e-9f60-718293a4b5c6"}, nil
	}
	command := newVMCommand()
	var out bytes.Buffer
	command.SetOut(&out)
	// A type given for an untyped OS is dropped, as for every other command.
	command.SetArgs([]string{"identity", "windows11", "--type", "server"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var value provision.VMIdentity
	if err := json.Unmarshal(out.Bytes(), &value); err != nil || value.Type != "" || !value.Exists || value.InstanceID == "" {
		t.Fatalf("identity: %+v %v (%s)", value, err, out.String())
	}
}

func TestMarkCreateStarted(t *testing.T) {
	vm := alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64"}
	if err := markCreateStarted("", vm); err != nil {
		t.Fatalf("no marker requested: %v", err)
	}
	path := filepath.Join(t.TempDir(), "started")
	if err := markCreateStarted(path, vm); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "ubuntu/server/amd64\n" {
		t.Fatalf("marker: %q %v", data, err)
	}
	if err := markCreateStarted(filepath.Join(t.TempDir(), "missing", "started"), vm); err == nil {
		t.Fatal("an unwritable marker must stop create before deployment")
	}
}
