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

func TestCreateListingUsesPlannedSecurityOnlyForMissingTargets(t *testing.T) {
	fakeGuestSecurity(t, "unknown", false, false)
	inspectGuestNetwork = alchemy_deploy.InspectNetworkMode
	inspectGuestCredentials = alchemy_provision.GuestCredentialSecurity
	dirs := alchemy_build.GetDirectoriesInstance()
	oldProject := dirs.ProjectDir
	oldJSON := listOutputJSON
	oldExists, oldArtifact := inspectCreateTargetExists, inspectCreateArtifactExists
	t.Cleanup(func() {
		dirs.ProjectDir, listOutputJSON = oldProject, oldJSON
		inspectCreateTargetExists, inspectCreateArtifactExists = oldExists, oldArtifact
	})
	dirs.ProjectDir = t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("UTM_UBUNTU_ANSIBLE_PASSWORD", "Unique-Guest-Passphrase")
	write := func(path, value string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dirs.ProjectDir, "deployments", "utm", "ubuntu-arm64", "config.plist"),
		`<plist><dict><key>Network</key><array><dict><key>Mode</key><string>Bridged</string></dict></array></dict></plist>`)
	write(filepath.Join(home, "Library", "Containers", "com.utmapp.UTM", "Data", "Documents", "ubuntu-desktop-arm64-dev-alchemy.utm", "config.plist"),
		`<plist><dict><key>Network</key><array><dict><key>Mode</key><string>Shared</string></dict></array></dict></plist>`)
	inspectCreateTargetExists = alchemy_deploy.CreateTargetExists
	inspectCreateArtifactExists = func(alchemy_build.VirtualMachineConfig) (bool, error) { return true, nil }
	vms := []alchemy_build.VirtualMachineConfig{
		{OS: "ubuntu", UbuntuType: "server", Arch: "arm64", HostOs: alchemy_build.HostOsDarwin, VirtualizationEngine: alchemy_build.VirtualizationEngineUtm},
		{OS: "ubuntu", UbuntuType: "desktop", Arch: "arm64", HostOs: alchemy_build.HostOsDarwin, VirtualizationEngine: alchemy_build.VirtualizationEngineUtm},
	}
	for _, jsonOutput := range []bool{false, true} {
		listOutputJSON = jsonOutput
		var out bytes.Buffer
		if err := printCreateCombinations(&out, vms); err != nil {
			t.Fatal(err)
		}
		if jsonOutput {
			var listing vmListingJSON
			if err := json.Unmarshal(out.Bytes(), &listing); err != nil {
				t.Fatal(err)
			}
			if len(listing.Targets) != 2 {
				t.Fatalf("unexpected targets: %+v", listing.Targets)
			}
			missing, existing := listing.Targets[0], listing.Targets[1]
			if missing.NetworkMode != "bridged" || missing.CredentialsOrigin != "legacy" || missing.Fields["create"] != "ready to create" {
				t.Fatalf("missing VM must show the template and baked-in credentials: %+v", missing)
			}
			if existing.NetworkMode != "nat" || existing.CredentialsOrigin != "user" || existing.Fields["create"] != "already created" {
				t.Fatalf("existing VM must show current configuration: %+v", existing)
			}
		} else {
			rows := strings.Split(out.String(), "\n")
			for _, row := range rows {
				if strings.Contains(row, "server") && (!strings.Contains(row, "bridged") || !strings.Contains(row, "legacy")) {
					t.Fatalf("incorrect planned metadata: %s", row)
				}
				if strings.Contains(row, "desktop") && (!strings.Contains(row, "nat") || !strings.Contains(row, "user")) {
					t.Fatalf("incorrect existing metadata: %s", row)
				}
			}
		}
	}
}

func TestGuestSecurityListingsSurviveCredentialConfigurationErrors(t *testing.T) {
	for _, unreadable := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid dotenv", true: "dotenv is a directory"}[unreadable], func(t *testing.T) {
			fakeGuestSecurity(t, "nat", false, false)
			inspectGuestCredentials = alchemy_provision.GuestCredentialSecurity
			dirs := alchemy_build.GetDirectoriesInstance()
			oldProject, oldJSON, oldStderr := dirs.ProjectDir, listOutputJSON, os.Stderr
			t.Cleanup(func() { dirs.ProjectDir, listOutputJSON, os.Stderr = oldProject, oldJSON, oldStderr })
			dirs.ProjectDir = t.TempDir()
			dotenv := filepath.Join(dirs.ProjectDir, ".env")
			if unreadable {
				if err := os.Mkdir(dotenv, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(dotenv, []byte("sensitive-invalid-dotenv-line"), 0o600); err != nil {
				t.Fatal(err)
			}
			warningsPath := filepath.Join(t.TempDir(), "warnings")
			warnings, err := os.Create(warningsPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = warnings.Close() })
			os.Stderr = warnings
			vm := alchemy_build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", VirtualizationEngine: alchemy_build.VirtualizationEngineQemu}
			for _, jsonOutput := range []bool{false, true} {
				listOutputJSON = jsonOutput
				var out bytes.Buffer
				err := printVirtualMachineCombinationTable(&out, "title", "empty", []alchemy_build.VirtualMachineConfig{vm}, []string{"OS", "Type", "Arch"}, func(vm alchemy_build.VirtualMachineConfig) ([]string, error) {
					return []string{vm.OS, vm.UbuntuType, vm.Arch}, nil
				})
				if err != nil {
					t.Fatalf("credential metadata must not hide targets: %v", err)
				}
				if jsonOutput {
					var listing vmListingJSON
					if err := json.Unmarshal(out.Bytes(), &listing); err != nil {
						t.Fatal(err)
					}
					if len(listing.Targets) != 1 || listing.Targets[0].CredentialsOrigin != "unknown" || listing.Targets[0].NetworkMode != "nat" {
						t.Fatalf("incorrect metadata: %+v", listing.Targets)
					}
				} else if !strings.Contains(out.String(), "unknown") || !strings.Contains(out.String(), "ubuntu") {
					t.Fatalf("missing target or unknown credentials: %s", out.String())
				}
				if strings.Contains(out.String(), "sensitive-invalid-dotenv-line") {
					t.Fatal("listing leaked configuration contents")
				}
			}
			var guardWarnings bytes.Buffer
			if err := checkGuestSecurity(vm, false, true, &guardWarnings); err == nil {
				t.Fatal("lifecycle guard must still reject invalid credentials, even with an override")
			}
			if err := warnings.Close(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(warningsPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(data, []byte("guest credential security is unknown")) || bytes.Contains(data, []byte("sensitive-invalid-dotenv-line")) {
				t.Fatalf("missing or unsafe diagnostic: %s", data)
			}
		})
	}
}
