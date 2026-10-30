package cmd

import (
	"bytes"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_provision "github.com/csautter/dev-alchemy/pkg/provision"
	"github.com/spf13/cobra"
)

func TestRunProvisionReturnsErrorForUnsupportedConfig(t *testing.T) {
	vm := alchemy_build.VirtualMachineConfig{
		OS:                   "windows11",
		Arch:                 "amd64",
		HostOs:               alchemy_build.HostOsWindows,
		VirtualizationEngine: alchemy_build.VirtualizationEngineVirtualBox,
	}

	err := runProvision(vm, alchemy_provision.ProvisionOptions{Verbosity: 3})
	if err == nil {
		t.Fatal("expected runProvision to return an error for unsupported vm configuration")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected error to mention not implemented, got: %v", err)
	}
}

func TestProvisionCommandRejectsAllTarget(t *testing.T) {
	previousArch := arch
	previousOsType := osType
	previousCheck := check
	previousPlaybookPath := playbookPath
	previousLocalProvisionProto := localProvisionProto
	t.Cleanup(func() {
		arch = previousArch
		osType = previousOsType
		check = previousCheck
		playbookPath = previousPlaybookPath
		localProvisionProto = previousLocalProvisionProto
	})

	arch = "amd64"
	osType = "server"
	check = false

	err := provisionCmd.RunE(provisionCmd, []string{"all"})
	if err == nil {
		t.Fatal("expected an error when using provision all")
	}
	if !strings.Contains(err.Error(), "\"all\" is not supported for provision") {
		t.Fatalf("expected explicit unsupported-all error, got: %v", err)
	}
}

func TestProvisionCommandRejectsArchAndTypeForLocalTarget(t *testing.T) {
	previousArch := arch
	previousOsType := osType
	previousCheck := check
	previousPlaybookPath := playbookPath
	previousLocalProvisionProto := localProvisionProto
	t.Cleanup(func() {
		arch = previousArch
		osType = previousOsType
		check = previousCheck
		playbookPath = previousPlaybookPath
		localProvisionProto = previousLocalProvisionProto
	})

	arch = "arm64"
	osType = "server"
	check = false

	if err := provisionCmd.Flags().Set("arch", "arm64"); err != nil {
		t.Fatalf("failed to mark arch flag as changed: %v", err)
	}
	if err := provisionCmd.Flags().Set("type", "server"); err != nil {
		t.Fatalf("failed to mark type flag as changed: %v", err)
	}
	t.Cleanup(func() {
		provisionCmd.Flags().Lookup("arch").Changed = false
		provisionCmd.Flags().Lookup("type").Changed = false
	})

	err := provisionCmd.RunE(provisionCmd, []string{"local"})
	if err == nil {
		t.Fatal("expected an error when local target is combined with arch/type flags")
	}
	if !strings.Contains(err.Error(), "does not accept --arch or --type") {
		t.Fatalf("expected explicit local flag validation error, got: %v", err)
	}
}

func TestProvisionCommandRejectsVerbosityOutsideSupportedRange(t *testing.T) {
	previousAnsibleVerbosity := ansibleVerbosity
	t.Cleanup(func() {
		ansibleVerbosity = previousAnsibleVerbosity
	})

	ansibleVerbosity = 5

	err := provisionCmd.RunE(provisionCmd, []string{"local"})
	if err == nil {
		t.Fatal("expected an error when verbosity exceeds the documented range")
	}
	if !strings.Contains(err.Error(), "ansible verbosity must be between 0 and 4") {
		t.Fatalf("expected explicit verbosity validation error, got: %v", err)
	}
}

func TestProvisionCommandPassesThroughAnsibleArgsAfterDash(t *testing.T) {
	previousArch := arch
	previousOsType := osType
	previousCheck := check
	previousPlaybookPath := playbookPath
	previousInventoryPath := inventoryPath
	previousAnsibleVerbosity := ansibleVerbosity
	previousLocalProvisionProto := localProvisionProto
	previousCurrentHostLocalProvisionVirtualMachineFunc := currentHostLocalProvisionVirtualMachineFunc
	previousRunProvisionFunc := runProvisionFunc
	previousRootOut := rootCmd.OutOrStdout()
	previousRootErr := rootCmd.ErrOrStderr()
	t.Cleanup(func() {
		arch = previousArch
		osType = previousOsType
		check = previousCheck
		playbookPath = previousPlaybookPath
		inventoryPath = previousInventoryPath
		ansibleVerbosity = previousAnsibleVerbosity
		localProvisionProto = previousLocalProvisionProto
		currentHostLocalProvisionVirtualMachineFunc = previousCurrentHostLocalProvisionVirtualMachineFunc
		runProvisionFunc = previousRunProvisionFunc
		provisionCmd.SetArgs(nil)
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(previousRootOut)
		rootCmd.SetErr(previousRootErr)
		for _, flagName := range []string{"arch", "type", "check", "dry-run", "playbook", "inventory-path", "verbosity", "proto"} {
			provisionCmd.Flags().Lookup(flagName).Changed = false
		}
	})

	currentHostLocalProvisionVirtualMachineFunc = func() (alchemy_build.VirtualMachineConfig, bool) {
		return alchemy_build.VirtualMachineConfig{
			OS:                   "local",
			Arch:                 "-",
			HostOs:               alchemy_build.HostOsLinux,
			VirtualizationEngine: localProvisionVirtualizationEngine,
		}, true
	}

	var capturedOptions alchemy_provision.ProvisionOptions
	runProvisionFunc = func(vm alchemy_build.VirtualMachineConfig, options alchemy_provision.ProvisionOptions) error {
		capturedOptions = options
		return nil
	}

	rootCmd.SetArgs([]string{
		"provision",
		"local",
		"--dry-run",
		"--playbook", "./playbooks/bootstrap.yml",
		"--inventory-path", "./inventory/custom.yml",
		"--verbosity", "1",
		"--",
		"--diff",
		"--tags", "java",
	})

	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected provision command to execute successfully, got: %v", err)
	}
	if !capturedOptions.Check {
		t.Fatal("expected --dry-run to propagate into provision options as check mode")
	}
	if capturedOptions.Verbosity != 1 {
		t.Fatalf("expected verbosity 1, got %d", capturedOptions.Verbosity)
	}
	if capturedOptions.PlaybookPath != "./playbooks/bootstrap.yml" {
		t.Fatalf("expected custom playbook path, got %q", capturedOptions.PlaybookPath)
	}
	if !capturedOptions.PlaybookPathExplicit {
		t.Fatal("expected explicit playbook flag to be tracked in provision options")
	}
	if capturedOptions.InventoryPath != "./inventory/custom.yml" {
		t.Fatalf("expected custom inventory path, got %q", capturedOptions.InventoryPath)
	}
	if got := strings.Join(capturedOptions.ExtraArgs, " "); got != "--diff --tags java" {
		t.Fatalf("expected ansible args after -- to be preserved, got %q", got)
	}
}

func TestProvisionCommandRejectsInventoryPathForVMTargets(t *testing.T) {
	previousArch := arch
	previousOsType := osType
	previousCheck := check
	previousPlaybookPath := playbookPath
	previousInventoryPath := inventoryPath
	previousLocalProvisionProto := localProvisionProto
	t.Cleanup(func() {
		arch = previousArch
		osType = previousOsType
		check = previousCheck
		playbookPath = previousPlaybookPath
		inventoryPath = previousInventoryPath
		localProvisionProto = previousLocalProvisionProto
		provisionCmd.Flags().Lookup("inventory-path").Changed = false
	})

	arch = "amd64"
	osType = "server"
	check = false
	inventoryPath = "./inventory/custom.yml"
	provisionCmd.Flags().Lookup("inventory-path").Changed = true

	err := provisionCmd.RunE(provisionCmd, []string{"windows11"})
	if err == nil {
		t.Fatal("expected VM provision to reject --inventory-path")
	}
	if !strings.Contains(err.Error(), "--inventory-path is only supported for local provisioning") {
		t.Fatalf("expected explicit inventory-path validation error, got: %v", err)
	}
}

func TestIsProvisionSupported(t *testing.T) {
	tests := []struct {
		name string
		vm   alchemy_build.VirtualMachineConfig
		want bool
	}{
		{
			name: "windows hyperv windows11 amd64 supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "windows11",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsWindows,
				VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
			},
			want: true,
		},
		{
			name: "windows hyperv ubuntu amd64 supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "ubuntu",
				Arch:                 "amd64",
				UbuntuType:           "server",
				HostOs:               alchemy_build.HostOsWindows,
				VirtualizationEngine: alchemy_build.VirtualizationEngineHyperv,
			},
			want: true,
		},
		{
			name: "windows virtualbox unsupported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "windows11",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsWindows,
				VirtualizationEngine: alchemy_build.VirtualizationEngineVirtualBox,
			},
			want: false,
		},
		{
			name: "darwin utm ubuntu arm64 supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "ubuntu",
				Arch:                 "arm64",
				UbuntuType:           "server",
				HostOs:               alchemy_build.HostOsDarwin,
				VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
			},
			want: true,
		},
		{
			name: "darwin utm ubuntu amd64 supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "ubuntu",
				Arch:                 "amd64",
				UbuntuType:           "desktop",
				HostOs:               alchemy_build.HostOsDarwin,
				VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
			},
			want: true,
		},
		{
			name: "darwin utm windows11 arm64 supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "windows11",
				Arch:                 "arm64",
				HostOs:               alchemy_build.HostOsDarwin,
				VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
			},
			want: true,
		},
		{
			name: "darwin utm windows11 amd64 supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "windows11",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsDarwin,
				VirtualizationEngine: alchemy_build.VirtualizationEngineUtm,
			},
			want: true,
		},
		{
			name: "linux qemu ubuntu amd64 supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "ubuntu",
				Arch:                 "amd64",
				UbuntuType:           "server",
				HostOs:               alchemy_build.HostOsLinux,
				VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
			},
			want: true,
		},
		{
			name: "linux qemu ubuntu arm64 supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "ubuntu",
				Arch:                 "arm64",
				UbuntuType:           "desktop",
				HostOs:               alchemy_build.HostOsLinux,
				VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
			},
			want: true,
		},
		{
			name: "linux qemu windows11 amd64 supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "windows11",
				Arch:                 "amd64",
				HostOs:               alchemy_build.HostOsLinux,
				VirtualizationEngine: alchemy_build.VirtualizationEngineQemu,
			},
			want: true,
		},
		{
			name: "darwin tart macos arm64 supported",
			vm: alchemy_build.VirtualMachineConfig{
				OS:                   "macos",
				Arch:                 "arm64",
				HostOs:               alchemy_build.HostOsDarwin,
				VirtualizationEngine: alchemy_build.VirtualizationEngineTart,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		if got := isProvisionSupported(tt.vm); got != tt.want {
			t.Fatalf("%s: expected %v, got %v", tt.name, tt.want, got)
		}
	}
}

func TestAvailableProvisionVirtualMachinesOnlyReturnsSupportedConfigs(t *testing.T) {
	var foundLocal bool
	for _, vm := range availableProvisionVirtualMachines() {
		if vm.OS == "local" {
			foundLocal = true
			continue
		}
		if !isProvisionSupported(vm) {
			t.Fatalf("expected only supported provision configs, got engine %q", vm.VirtualizationEngine)
		}
	}
	if !foundLocal {
		t.Fatal("expected local provision target to be included for the current host")
	}
}

func TestAvailableProvisionVirtualMachinesMarksLinuxCrossArchitectureTargetsUnstable(t *testing.T) {
	withCurrentHostArchitecture(t, "amd64")

	vms := availableProvisionVirtualMachinesForHostOS(alchemy_build.HostOsLinux)
	requireProvisionVMStatus(t, vms, "server", "amd64", "stable")
	requireProvisionVMStatus(t, vms, "server", "arm64", "unstable")
}

func TestAvailableProvisionVirtualMachinesMarksLinuxCrossArchitectureTargetsUnstableOnArm64(t *testing.T) {
	withCurrentHostArchitecture(t, "arm64")

	vms := availableProvisionVirtualMachinesForHostOS(alchemy_build.HostOsLinux)
	requireProvisionVMStatus(t, vms, "desktop", "arm64", "stable")
	requireProvisionVMStatus(t, vms, "desktop", "amd64", "unstable")
}

func TestCurrentHostLocalProvisionVirtualMachineUsesLocalEngine(t *testing.T) {
	vm, ok := currentHostLocalProvisionVirtualMachine()
	if !ok {
		t.Fatal("expected current host local provision target to be available")
	}
	if vm.OS != "local" {
		t.Fatalf("expected local provision OS, got %q", vm.OS)
	}
	if vm.VirtualizationEngine != localProvisionVirtualizationEngine {
		t.Fatalf("expected local virtualization engine, got %q", vm.VirtualizationEngine)
	}
	if vm.Arch != "-" {
		t.Fatalf("expected local provision arch placeholder, got %q", vm.Arch)
	}
}

func TestProvisionStatusMarksLocalNonWindowsHostsUnstable(t *testing.T) {
	if got := provisionStatus(alchemy_build.VirtualMachineConfig{
		OS:     "local",
		HostOs: alchemy_build.HostOsWindows,
	}); got != "stable" {
		t.Fatalf("expected windows local provision to be stable, got %q", got)
	}

	if got := provisionStatus(alchemy_build.VirtualMachineConfig{
		OS:     "local",
		HostOs: alchemy_build.HostOsDarwin,
	}); got != "unstable" {
		t.Fatalf("expected darwin local provision to be unstable, got %q", got)
	}

	if got := provisionStatus(alchemy_build.VirtualMachineConfig{
		OS:     "local",
		HostOs: alchemy_build.HostOsLinux,
	}); got != "unstable" {
		t.Fatalf("expected linux local provision to be unstable, got %q", got)
	}
}

func TestIsInteractiveInputRejectsNonTerminalFiles(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("failed to open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()
	if isInteractiveInput(devNull) {
		t.Fatalf("expected %s not to count as interactive input", os.DevNull)
	}
}

func TestConfirmProvisionIntentRequiresYesForNonInteractiveWindowsLocal(t *testing.T) {
	previousAssumeYes := assumeYes
	t.Cleanup(func() {
		assumeYes = previousAssumeYes
	})
	assumeYes = false

	inputFile, err := os.CreateTemp(t.TempDir(), "confirmation-input-*.txt")
	if err != nil {
		t.Fatalf("failed to create temp input file: %v", err)
	}
	defer inputFile.Close()

	command := &cobra.Command{}
	command.SetIn(inputFile)
	command.SetOut(&bytes.Buffer{})

	err = confirmProvisionIntent(command, alchemy_build.VirtualMachineConfig{
		OS:     "local",
		HostOs: alchemy_build.HostOsWindows,
	}, alchemy_provision.ProvisionOptions{LocalWindowsProtocol: alchemy_provision.LocalWindowsProvisionProtocolWinRM})
	if err == nil {
		t.Fatal("expected non-interactive windows local provisioning to require --yes")
	}
	if !strings.Contains(err.Error(), "Re-run with --yes") {
		t.Fatalf("expected --yes guidance in error, got: %v", err)
	}
}

func TestConfirmProvisionIntentPromptsForInteractiveWindowsLocal(t *testing.T) {
	previousAssumeYes := assumeYes
	t.Cleanup(func() {
		assumeYes = previousAssumeYes
	})
	assumeYes = false

	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetIn(bytes.NewBufferString("yes\n"))
	command.SetOut(&output)

	if err := confirmProvisionIntent(command, alchemy_build.VirtualMachineConfig{
		OS:     "local",
		HostOs: alchemy_build.HostOsWindows,
	}, alchemy_provision.ProvisionOptions{LocalWindowsProtocol: alchemy_provision.LocalWindowsProvisionProtocolWinRM}); err != nil {
		t.Fatalf("expected interactive confirmation to succeed, got: %v", err)
	}
	if !strings.Contains(output.String(), "Continue? [y/N]:") {
		t.Fatalf("expected prompt output, got %q", output.String())
	}
}

func TestConfirmProvisionIntentSkipsPromptWhenYesFlagIsSet(t *testing.T) {
	previousAssumeYes := assumeYes
	previousPrompt := promptForConfirmationFunc
	t.Cleanup(func() {
		assumeYes = previousAssumeYes
		promptForConfirmationFunc = previousPrompt
	})
	assumeYes = true

	promptForConfirmationFunc = func(input io.Reader, output io.Writer, prompt string) (bool, error) {
		t.Fatal("did not expect confirmation prompt when --yes is set")
		return false, nil
	}

	command := &cobra.Command{}
	command.SetIn(bytes.NewBufferString(""))
	command.SetOut(&bytes.Buffer{})

	if err := confirmProvisionIntent(command, alchemy_build.VirtualMachineConfig{
		OS:     "local",
		HostOs: alchemy_build.HostOsWindows,
	}, alchemy_provision.ProvisionOptions{LocalWindowsProtocol: alchemy_provision.LocalWindowsProvisionProtocolWinRM}); err != nil {
		t.Fatalf("expected --yes to skip confirmation, got: %v", err)
	}
}

func TestConfirmProvisionIntentMentionsForceWinRMUninstall(t *testing.T) {
	previousAssumeYes := assumeYes
	t.Cleanup(func() {
		assumeYes = previousAssumeYes
	})
	assumeYes = false

	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetIn(bytes.NewBufferString("yes\n"))
	command.SetOut(&output)

	if err := confirmProvisionIntent(command, alchemy_build.VirtualMachineConfig{
		OS:     "local",
		HostOs: alchemy_build.HostOsWindows,
	}, alchemy_provision.ProvisionOptions{
		LocalWindowsProtocol:            alchemy_provision.LocalWindowsProvisionProtocolWinRM,
		LocalWindowsForceWinRMUninstall: true,
	}); err != nil {
		t.Fatalf("expected interactive confirmation to succeed, got: %v", err)
	}
	if !strings.Contains(output.String(), "--force-winrm-uninstall") {
		t.Fatalf("expected prompt to mention force winrm uninstall, got %q", output.String())
	}
}

func TestConfirmProvisionIntentMentionsSSHBootstrap(t *testing.T) {
	previousAssumeYes := assumeYes
	t.Cleanup(func() {
		assumeYes = previousAssumeYes
	})
	assumeYes = false

	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetIn(bytes.NewBufferString("yes\n"))
	command.SetOut(&output)

	if err := confirmProvisionIntent(command, alchemy_build.VirtualMachineConfig{
		OS:     "local",
		HostOs: alchemy_build.HostOsWindows,
	}, alchemy_provision.ProvisionOptions{
		LocalWindowsProtocol: alchemy_provision.LocalWindowsProvisionProtocolSSH,
	}); err != nil {
		t.Fatalf("expected interactive ssh confirmation to succeed, got: %v", err)
	}
	if !strings.Contains(output.String(), "OpenSSH Server") {
		t.Fatalf("expected ssh prompt to mention OpenSSH Server, got %q", output.String())
	}
	if !strings.Contains(output.String(), "leave that capability installed") {
		t.Fatalf("expected ssh prompt to explain that cleanup leaves a newly installed OpenSSH capability in place, got %q", output.String())
	}
}

func TestConfirmProvisionIntentMentionsForceSSHUninstall(t *testing.T) {
	previousAssumeYes := assumeYes
	t.Cleanup(func() {
		assumeYes = previousAssumeYes
	})
	assumeYes = false

	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetIn(bytes.NewBufferString("yes\n"))
	command.SetOut(&output)

	if err := confirmProvisionIntent(command, alchemy_build.VirtualMachineConfig{
		OS:     "local",
		HostOs: alchemy_build.HostOsWindows,
	}, alchemy_provision.ProvisionOptions{
		LocalWindowsProtocol:          alchemy_provision.LocalWindowsProvisionProtocolSSH,
		LocalWindowsForceSSHUninstall: true,
	}); err != nil {
		t.Fatalf("expected interactive ssh confirmation to succeed, got: %v", err)
	}
	if !strings.Contains(output.String(), "--force-ssh-uninstall") {
		t.Fatalf("expected prompt to mention force ssh uninstall, got %q", output.String())
	}
	if !strings.Contains(output.String(), "remain installed") {
		t.Fatalf("expected prompt to explain that OpenSSH Server remains installed to avoid a reboot, got %q", output.String())
	}
}

func TestRunProvisionPassesOptionsThrough(t *testing.T) {
	previousRunProvisionFunc := runProvisionFunc
	t.Cleanup(func() {
		runProvisionFunc = previousRunProvisionFunc
	})

	expectedOptions := alchemy_provision.ProvisionOptions{
		Check:                           true,
		Verbosity:                       3,
		LocalWindowsProtocol:            alchemy_provision.LocalWindowsProvisionProtocolSSH,
		LocalWindowsForceWinRMUninstall: false,
	}
	var capturedOptions alchemy_provision.ProvisionOptions
	runProvisionFunc = func(vm alchemy_build.VirtualMachineConfig, options alchemy_provision.ProvisionOptions) error {
		capturedOptions = options
		return nil
	}

	if err := runProvision(alchemy_build.VirtualMachineConfig{OS: "local", HostOs: alchemy_build.HostOsWindows}, expectedOptions); err != nil {
		t.Fatalf("expected runProvision to succeed, got: %v", err)
	}
	if !reflect.DeepEqual(capturedOptions, expectedOptions) {
		t.Fatalf("expected runProvision to pass options through unchanged, got %+v", capturedOptions)
	}
}

func TestProvisionCommandRejectsForceWinRMUninstallForNonWindowsLocal(t *testing.T) {
	previousForceWinRMUninstall := forceWinRMUninstall
	previousCurrentHostLocalProvisionVirtualMachineFunc := currentHostLocalProvisionVirtualMachineFunc
	t.Cleanup(func() {
		forceWinRMUninstall = previousForceWinRMUninstall
		currentHostLocalProvisionVirtualMachineFunc = previousCurrentHostLocalProvisionVirtualMachineFunc
	})

	forceWinRMUninstall = true
	currentHostLocalProvisionVirtualMachineFunc = func() (alchemy_build.VirtualMachineConfig, bool) {
		return alchemy_build.VirtualMachineConfig{
			OS:                   "local",
			Arch:                 "-",
			HostOs:               alchemy_build.HostOsLinux,
			VirtualizationEngine: localProvisionVirtualizationEngine,
		}, true
	}

	err := provisionCmd.RunE(provisionCmd, []string{"local"})
	if err == nil {
		t.Fatal("expected non-windows local run with force winrm uninstall to fail")
	}
	if !strings.Contains(err.Error(), "--force-winrm-uninstall is only supported") {
		t.Fatalf("expected explicit force-winrm-uninstall validation error, got: %v", err)
	}
}

func TestProvisionCommandRejectsForceSSHUninstallForNonWindowsLocal(t *testing.T) {
	previousForceSSHUninstall := forceSSHUninstall
	previousCurrentHostLocalProvisionVirtualMachineFunc := currentHostLocalProvisionVirtualMachineFunc
	t.Cleanup(func() {
		forceSSHUninstall = previousForceSSHUninstall
		currentHostLocalProvisionVirtualMachineFunc = previousCurrentHostLocalProvisionVirtualMachineFunc
	})

	forceSSHUninstall = true
	currentHostLocalProvisionVirtualMachineFunc = func() (alchemy_build.VirtualMachineConfig, bool) {
		return alchemy_build.VirtualMachineConfig{
			OS:                   "local",
			Arch:                 "-",
			HostOs:               alchemy_build.HostOsLinux,
			VirtualizationEngine: localProvisionVirtualizationEngine,
		}, true
	}

	err := provisionCmd.RunE(provisionCmd, []string{"local"})
	if err == nil {
		t.Fatal("expected non-windows local run with force ssh uninstall to fail")
	}
	if !strings.Contains(err.Error(), "--force-ssh-uninstall is only supported") {
		t.Fatalf("expected explicit force-ssh-uninstall validation error, got: %v", err)
	}
}

func TestProvisionCommandRejectsForceSSHUninstallForWinRMProtocol(t *testing.T) {
	previousForceSSHUninstall := forceSSHUninstall
	previousLocalProvisionProto := localProvisionProto
	previousCurrentHostLocalProvisionVirtualMachineFunc := currentHostLocalProvisionVirtualMachineFunc
	t.Cleanup(func() {
		forceSSHUninstall = previousForceSSHUninstall
		localProvisionProto = previousLocalProvisionProto
		currentHostLocalProvisionVirtualMachineFunc = previousCurrentHostLocalProvisionVirtualMachineFunc
	})

	forceSSHUninstall = true
	localProvisionProto = "winrm"
	currentHostLocalProvisionVirtualMachineFunc = func() (alchemy_build.VirtualMachineConfig, bool) {
		return alchemy_build.VirtualMachineConfig{
			OS:                   "local",
			Arch:                 "-",
			HostOs:               alchemy_build.HostOsWindows,
			VirtualizationEngine: localProvisionVirtualizationEngine,
		}, true
	}

	err := provisionCmd.RunE(provisionCmd, []string{"local"})
	if err == nil {
		t.Fatal("expected winrm local run with force ssh uninstall to fail")
	}
	if !strings.Contains(err.Error(), "--force-ssh-uninstall is only supported with local Windows --proto ssh") {
		t.Fatalf("expected explicit force-ssh-uninstall protocol validation error, got: %v", err)
	}
}

func TestProvisionCommandRejectsProtoForVMTargets(t *testing.T) {
	previousLocalProvisionProto := localProvisionProto
	t.Cleanup(func() {
		localProvisionProto = previousLocalProvisionProto
		provisionCmd.Flags().Lookup("proto").Changed = false
	})

	localProvisionProto = "ssh"
	provisionCmd.Flags().Lookup("proto").Changed = true

	err := provisionCmd.RunE(provisionCmd, []string{"windows11"})
	if err == nil {
		t.Fatal("expected VM provision to reject --proto")
	}
	if !strings.Contains(err.Error(), "--proto is only supported for local Windows provisioning") {
		t.Fatalf("expected explicit proto validation error, got: %v", err)
	}
}

func linuxLocalProvisionVirtualMachine() (alchemy_build.VirtualMachineConfig, bool) {
	return alchemy_build.VirtualMachineConfig{
		OS:                   "local",
		Arch:                 "-",
		HostOs:               alchemy_build.HostOsLinux,
		VirtualizationEngine: localProvisionVirtualizationEngine,
	}, true
}

// restoreAskBecomePassState resets the globals the --ask-become-pass tests change.
func restoreAskBecomePassState(t *testing.T) {
	t.Helper()
	previousAskBecomePass := askBecomePass
	previousCurrentHostLocalProvisionVirtualMachineFunc := currentHostLocalProvisionVirtualMachineFunc
	previousRunProvisionFunc := runProvisionFunc
	t.Cleanup(func() {
		askBecomePass = previousAskBecomePass
		currentHostLocalProvisionVirtualMachineFunc = previousCurrentHostLocalProvisionVirtualMachineFunc
		runProvisionFunc = previousRunProvisionFunc
		rootCmd.SetArgs(nil)
		rootCmd.SetIn(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		provisionCmd.SetIn(nil)
		provisionCmd.Flags().Lookup("ask-become-pass").Changed = false
	})
}

// executeLocalLinuxProvision runs the provision command through cobra, so `--` is
// parsed, against a stubbed local Linux host and returns the options it would run with.
func executeLocalLinuxProvision(t *testing.T, args ...string) (alchemy_provision.ProvisionOptions, error) {
	t.Helper()
	restoreAskBecomePassState(t)

	currentHostLocalProvisionVirtualMachineFunc = linuxLocalProvisionVirtualMachine
	var capturedOptions alchemy_provision.ProvisionOptions
	runProvisionFunc = func(vm alchemy_build.VirtualMachineConfig, options alchemy_provision.ProvisionOptions) error {
		capturedOptions = options
		return nil
	}

	rootCmd.SetArgs(append([]string{"provision"}, args...))
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)

	err := rootCmd.Execute()
	return capturedOptions, err
}

func TestProvisionCommandPassesAskBecomePassForLocalLinux(t *testing.T) {
	options, err := executeLocalLinuxProvision(t, "local", "-K")
	if err != nil {
		t.Fatalf("expected provision command to execute successfully, got: %v", err)
	}
	if !options.AskBecomePass {
		t.Fatal("expected -K to propagate into provision options")
	}
}

func TestProvisionCommandRejectsAskBecomePassWithBecomePasswordFile(t *testing.T) {
	for _, passthrough := range [][]string{
		{"--become-password-file", "/tmp/pw"},
		{"--become-password-file=/tmp/pw"},
		{"--diff", "--become-pass-file", "/tmp/pw"},
	} {
		_, err := executeLocalLinuxProvision(t, append([]string{"local", "-K", "--"}, passthrough...)...)
		if err == nil || !strings.Contains(err.Error(), "cannot be combined with --become-password-file") {
			t.Fatalf("expected conflict error for passthrough %q, got: %v", passthrough, err)
		}
	}
}

func TestProvisionCommandAllowsBecomePasswordFileWithoutAskBecomePass(t *testing.T) {
	options, err := executeLocalLinuxProvision(t, "local", "--", "--become-password-file", "/tmp/pw")
	if err != nil {
		t.Fatalf("expected provision command to execute successfully, got: %v", err)
	}
	if options.AskBecomePass || !reflect.DeepEqual(options.ExtraArgs, []string{"--become-password-file", "/tmp/pw"}) {
		t.Fatalf("expected the password file to pass through unchanged, got: %+v", options)
	}
}

func TestProvisionCommandRejectsAskBecomePassWithoutTerminal(t *testing.T) {
	restoreAskBecomePassState(t)

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("failed to open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()

	askBecomePass = true
	currentHostLocalProvisionVirtualMachineFunc = linuxLocalProvisionVirtualMachine
	provisionCmd.SetIn(devNull)

	err = provisionCmd.RunE(provisionCmd, []string{"local"})
	if err == nil {
		t.Fatal("expected non-interactive run with --ask-become-pass to fail")
	}
	if !strings.Contains(err.Error(), "--become-password-file") {
		t.Fatalf("expected hint to --become-password-file, got: %v", err)
	}
}

func TestProvisionCommandRejectsAskBecomePassForWindowsLocal(t *testing.T) {
	restoreAskBecomePassState(t)

	askBecomePass = true
	currentHostLocalProvisionVirtualMachineFunc = func() (alchemy_build.VirtualMachineConfig, bool) {
		return alchemy_build.VirtualMachineConfig{
			OS:                   "local",
			Arch:                 "-",
			HostOs:               alchemy_build.HostOsWindows,
			VirtualizationEngine: localProvisionVirtualizationEngine,
		}, true
	}

	err := provisionCmd.RunE(provisionCmd, []string{"local"})
	if err == nil {
		t.Fatal("expected Windows local run with --ask-become-pass to fail")
	}
	if !strings.Contains(err.Error(), "--ask-become-pass is not supported for local Windows provisioning") {
		t.Fatalf("expected explicit ask-become-pass validation error, got: %v", err)
	}
}

func TestProvisionCommandRejectsAskBecomePassForVMTargets(t *testing.T) {
	restoreAskBecomePassState(t)
	askBecomePass = true

	err := provisionCmd.RunE(provisionCmd, []string{"windows11"})
	if err == nil {
		t.Fatal("expected VM provision to reject --ask-become-pass")
	}
	if !strings.Contains(err.Error(), "--ask-become-pass is only supported for local provisioning") {
		t.Fatalf("expected explicit ask-become-pass validation error, got: %v", err)
	}
}
