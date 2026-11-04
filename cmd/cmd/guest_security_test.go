package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_deploy "github.com/csautter/dev-alchemy/pkg/deploy"
	alchemy_provision "github.com/csautter/dev-alchemy/pkg/provision"
	"github.com/spf13/cobra"
)

func fakeGuestSecurity(t *testing.T, mode string, private bool, configOverride bool) {
	t.Helper()
	oldNetwork, oldCredentials, oldConfig := inspectGuestNetwork, inspectGuestCredentials, readGuestSecurityOverride
	t.Cleanup(func() {
		inspectGuestNetwork = oldNetwork
		inspectGuestCredentials = oldCredentials
		readGuestSecurityOverride = oldConfig
	})
	inspectGuestNetwork = func(alchemy_build.VirtualMachineConfig, bool) alchemy_deploy.NetworkMode {
		return alchemy_deploy.NetworkMode{Mode: mode, Detail: "fake network"}
	}
	inspectGuestCredentials = func(alchemy_build.VirtualMachineConfig) (string, bool, error) {
		if private {
			return "user", true, nil
		}
		return "legacy", false, nil
	}
	readGuestSecurityOverride = func(string) (bool, error) { return configOverride, nil }
}

func TestGuestSecurityPolicy(t *testing.T) {
	vm := alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", VirtualizationEngine: alchemy_build.VirtualizationEngineQemu}
	for _, mode := range []string{"nat", "bridged", "unknown"} {
		for _, private := range []bool{false, true} {
			for _, creating := range []bool{false, true} {
				for _, override := range []string{"none", "flag", "config"} {
					t.Run(strings.Join([]string{mode, override}, "/"), func(t *testing.T) {
						fakeGuestSecurity(t, mode, private, override == "config")
						var warnings bytes.Buffer
						err := checkGuestSecurity(vm, creating, override == "flag", &warnings)
						nonPrivate := creating || !private
						blocked := mode != "nat" && nonPrivate && override == "none"
						if (err != nil) != blocked {
							t.Fatalf("mode=%s private=%t creating=%t override=%s: %v", mode, private, creating, override, err)
						}
						if nonPrivate && !strings.Contains(warnings.String(), "non-private guest credentials") {
							t.Fatal("missing credential warning")
						}
						if mode != "nat" && !strings.Contains(warnings.String(), "may be reachable from the LAN") {
							t.Fatal("missing network warning")
						}
					})
				}
			}
		}
	}
}

func TestGuestSecurityGuardsLifecycleBeforeMutation(t *testing.T) {
	fakeGuestSecurity(t, "bridged", false, false)
	oldInspector := inspectCreateTargetExists
	t.Cleanup(func() { inspectCreateTargetExists = oldInspector })
	inspectCreateTargetExists = func(alchemy_build.VirtualMachineConfig) (bool, error) { return false, nil }
	vm := alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", HostOs: alchemy_build.HostOsLinux, VirtualizationEngine: alchemy_build.VirtualizationEngineQemu}
	for _, run := range []func() error{
		func() error { return runDeploy(vm) },
		func() error { return runStart(vm) },
		func() error { return runProvision(vm, nil, alchemy_provision.ProvisionOptions{}) },
	} {
		if err := run(); err == nil || !strings.Contains(err.Error(), "refusing bridged networking") {
			t.Fatalf("expected security refusal before executing host tools: %v", err)
		}
	}
}

func TestGuestSecurityLocalAndConfigFailure(t *testing.T) {
	fakeGuestSecurity(t, "unknown", false, false)
	readGuestSecurityOverride = func(string) (bool, error) { return false, errors.New("invalid config") }
	if err := checkGuestSecurity(alchemy_build.VirtualMachineConfig{OS: "local"}, false, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := checkGuestSecurity(alchemy_build.VirtualMachineConfig{OS: "ubuntu"}, false, true, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid config must fail even with override")
	}
}

func TestGuestSecurityListing(t *testing.T) {
	fakeGuestSecurity(t, "bridged", false, false)
	oldJSON := listOutputJSON
	t.Cleanup(func() { listOutputJSON = oldJSON })
	vm := alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", VirtualizationEngine: alchemy_build.VirtualizationEngineQemu}
	rows := func(vm alchemy_build.VirtualMachineConfig) ([]string, error) {
		return []string{vm.OS, vm.UbuntuType, vm.Arch}, nil
	}
	var table bytes.Buffer
	listOutputJSON = false
	if err := printVirtualMachineCombinationTable(&table, "title", "empty", []alchemy_build.VirtualMachineConfig{vm}, []string{"OS", "Type", "Arch"}, rows); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NET", "CREDS", "bridged", "legacy"} {
		if !strings.Contains(table.String(), want) {
			t.Fatalf("missing %s: %s", want, table.String())
		}
	}
	var output bytes.Buffer
	listOutputJSON = true
	if err := printVirtualMachineCombinationTable(&output, "title", "empty", []alchemy_build.VirtualMachineConfig{vm}, []string{"OS", "Type", "Arch"}, rows); err != nil {
		t.Fatal(err)
	}
	var listing vmListingJSON
	if err := json.Unmarshal(output.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	target := listing.Targets[0]
	if target.NetworkMode != "bridged" || target.NetworkDetail != "fake network" || target.CredentialsOrigin != "legacy" {
		t.Fatalf("unexpected metadata: %+v", target)
	}
	if strings.Contains(output.String(), "password") {
		t.Fatal("listing must not contain credentials")
	}
}

func TestGuestSecurityOverrideFlags(t *testing.T) {
	for _, command := range []*cobra.Command{createCmd, startCmd, provisionCmd} {
		flag := command.Flags().Lookup("allow-insecure-credentials")
		if flag == nil || flag.DefValue != "false" {
			t.Fatalf("missing safe default on %s", command.Name())
		}
	}
}
