package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_provision "github.com/csautter/dev-alchemy/pkg/provision"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	check               bool
	assumeYes           bool
	forceWinRMUninstall bool
	forceSSHUninstall   bool
	localProvisionProto string
	playbookPath        string
	inventoryPath       string
	ansibleVerbosity    int
	askBecomePass       bool
)

const localProvisionVirtualizationEngine = alchemy_build.VirtualizationEngine("local")

var (
	currentHostLocalProvisionVirtualMachineFunc = currentHostLocalProvisionVirtualMachine
	runProvisionFunc                            = alchemy_provision.RunProvisionWithOptions
	promptForConfirmationFunc                   = promptForConfirmation
)

func isProvisionSupported(vm alchemy_build.VirtualMachineConfig) bool {
	if vm.HostOs == alchemy_build.HostOsWindows &&
		vm.VirtualizationEngine == alchemy_build.VirtualizationEngineHyperv &&
		vm.Arch == "amd64" &&
		(vm.OS == "windows11" || vm.OS == "ubuntu") {
		return true
	}

	if vm.HostOs == alchemy_build.HostOsLinux &&
		vm.VirtualizationEngine == alchemy_build.VirtualizationEngineQemu &&
		(vm.Arch == "amd64" || vm.Arch == "arm64") {
		if vm.OS == "windows11" {
			return true
		}
		if vm.OS == "ubuntu" &&
			(vm.UbuntuType == "server" || vm.UbuntuType == "desktop") {
			return true
		}
	}

	return vm.HostOs == alchemy_build.HostOsDarwin &&
		((vm.VirtualizationEngine == alchemy_build.VirtualizationEngineUtm &&
			(vm.OS == "windows11" || vm.OS == "ubuntu") &&
			(vm.Arch == "amd64" || vm.Arch == "arm64")) ||
			(vm.VirtualizationEngine == alchemy_build.VirtualizationEngineTart &&
				vm.OS == "macos" &&
				vm.Arch == "arm64"))
}

func localProvisionVirtualMachineForHostOS(hostOs alchemy_build.HostOsType) (alchemy_build.VirtualMachineConfig, bool) {
	switch hostOs {
	case alchemy_build.HostOsWindows, alchemy_build.HostOsDarwin, alchemy_build.HostOsLinux:
		return alchemy_build.VirtualMachineConfig{
			OS:                   "local",
			Arch:                 "-",
			HostOs:               hostOs,
			VirtualizationEngine: localProvisionVirtualizationEngine,
		}, true
	default:
		return alchemy_build.VirtualMachineConfig{}, false
	}
}

func currentHostLocalProvisionVirtualMachine() (alchemy_build.VirtualMachineConfig, bool) {
	return localProvisionVirtualMachineForHostOS(alchemy_build.GetCurrentHostOs())
}

func isLocalProvisionUnstable(hostOs alchemy_build.HostOsType) bool {
	return hostOs != alchemy_build.HostOsWindows
}

func provisionStatus(vm alchemy_build.VirtualMachineConfig) string {
	if vm.OS == "local" {
		if isLocalProvisionUnstable(vm.HostOs) {
			return "unstable"
		}
		return "stable"
	}
	if isVirtualMachineTargetUnstable(vm) {
		return "unstable"
	}
	return "stable"
}

func availableProvisionVirtualMachinesForHostOS(hostOs alchemy_build.HostOsType) []alchemy_build.VirtualMachineConfig {
	supported := availableVirtualMachinesForHostOS(hostOs, isProvisionSupported)
	if localVM, ok := localProvisionVirtualMachineForHostOS(hostOs); ok {
		supported = append(supported, localVM)
	}
	return supported
}

func availableProvisionVirtualMachines() []alchemy_build.VirtualMachineConfig {
	return availableProvisionVirtualMachinesForHostOS(alchemy_build.GetCurrentHostOs())
}

func printAvailableProvisionCombinations() error {
	vms := availableProvisionVirtualMachines()
	return printVirtualMachineCombinationTable(
		os.Stdout,
		fmt.Sprintf("Available provision combinations for host OS: %s", alchemy_build.GetCurrentHostOs()),
		"No provision combinations are available for the current host OS.",
		vms,
		[]string{"OS", "Type", "Arch", "Status"},
		func(vm alchemy_build.VirtualMachineConfig) ([]string, error) {
			return []string{vm.OS, displayVirtualMachineType(vm), vm.Arch, provisionStatus(vm)}, nil
		},
	)
}

var provisionCmd = &cobra.Command{
	Use:   "provision <osname|local> [flags] [-- <ansible args...>]",
	Short: "Provision and test Ansible configuration against a VM or the local host",
	Long: `Runs Ansible provisioning against VM targets or the current host.

Important Ansible options exposed directly:
  --check, --dry-run      Run ansible-playbook with --check.
  -K, --ask-become-pass   Ask for the sudo password that tasks with become need. Local macOS and Linux provisioning only.
  --proto PROTO          For local Windows provisioning, select winrm (default) or ssh.
  --force-winrm-uninstall For local Windows WinRM provisioning, force cleanup to disable WinRM.
  --force-ssh-uninstall   For local Windows SSH provisioning, force cleanup to disable sshd, remove SSH firewall rules, and remove the transient user without uninstalling OpenSSH Server.
  --verbosity N           Set Ansible verbosity. The default is 3, equivalent to -vvv.
  --playbook PATH         Override the playbook path. Without this flag, ansible-role-sources.yml can set the effective default; otherwise Sailwright falls back to ./playbooks/role-sources-test.yml.
  --inventory-path PATH   Override the default inventory file for local provisioning.

Pass any other ansible-playbook flags after --.
When --inventory-path is set, Sailwright stops forcing the default local --limit target, so pass one yourself when needed.
Configure layered local or Git-backed role and playbook roots in the OS-specific ansible-role-sources.yml file.

Examples:
  sailwright provision local --check
  sailwright provision local --ask-become-pass
  sailwright provision local --proto ssh --check
  sailwright provision local --proto ssh --check --yes --force-ssh-uninstall
  sailwright provision local --playbook ./playbooks/setup.yml --check
  sailwright provision local -- --diff
  sailwright provision local --inventory-path ./inventory/remote.yml --ask-become-pass -- --limit workstation
  sailwright provision macos --arch arm64 --check
  sailwright provision windows11 --arch amd64 --check
  sailwright provision windows11 --arch arm64 --check
  sailwright provision ubuntu --type server --arch amd64 -- --tags java
`,
	Args: validateProvisionCommandArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		osName, extraAnsibleArgs := splitProvisionArgs(cmd, args)
		options := alchemy_provision.ProvisionOptions{
			Check:                           check,
			AskBecomePass:                   askBecomePass,
			Verbosity:                       ansibleVerbosity,
			PlaybookPath:                    strings.TrimSpace(playbookPath),
			PlaybookPathExplicit:            cmd.Flags().Changed("playbook"),
			InventoryPath:                   strings.TrimSpace(inventoryPath),
			ExtraArgs:                       extraAnsibleArgs,
			LocalWindowsProtocol:            alchemy_provision.LocalWindowsProvisionProtocol(strings.TrimSpace(localProvisionProto)),
			LocalWindowsForceWinRMUninstall: forceWinRMUninstall,
			LocalWindowsForceSSHUninstall:   forceSSHUninstall,
		}
		if err := alchemy_provision.ValidateProvisionVerbosity(options.Verbosity); err != nil {
			return err
		}
		if err := alchemy_provision.ValidateLocalWindowsProvisionProtocol(options.LocalWindowsProtocol); err != nil {
			return err
		}

		if osName == "all" {
			return fmt.Errorf("❌ \"all\" is not supported for provision; provide one target, for example: sailwright provision windows11 --arch amd64 --check")
		}

		if osName == "local" {
			if cmd.Flags().Changed("arch") || cmd.Flags().Changed("type") {
				return fmt.Errorf("❌ local provisioning does not accept --arch or --type; use `sailwright provision local [--check]`")
			}

			selectedVM, ok := currentHostLocalProvisionVirtualMachineFunc()
			if !ok {
				return fmt.Errorf("❌ local provisioning is not available for host OS: %s", alchemy_build.GetCurrentHostOs())
			}
			if forceWinRMUninstall && selectedVM.HostOs != alchemy_build.HostOsWindows {
				return fmt.Errorf("❌ --force-winrm-uninstall is only supported for local Windows provisioning")
			}
			if forceSSHUninstall && selectedVM.HostOs != alchemy_build.HostOsWindows {
				return fmt.Errorf("❌ --force-ssh-uninstall is only supported for local Windows provisioning")
			}
			if cmd.Flags().Changed("proto") && selectedVM.HostOs != alchemy_build.HostOsWindows {
				return fmt.Errorf("❌ --proto is only supported for local Windows provisioning")
			}
			if askBecomePass {
				if selectedVM.HostOs == alchemy_build.HostOsWindows {
					return fmt.Errorf("❌ --ask-become-pass is not supported for local Windows provisioning; Sailwright connects with a temporary administrator account")
				}
				if !isInteractiveInput(cmd.InOrStdin()) {
					return fmt.Errorf("❌ --ask-become-pass needs an interactive terminal; pass `-- --become-password-file <path>` instead")
				}
				if hasBecomePasswordFileArg(extraAnsibleArgs) {
					return fmt.Errorf("❌ --ask-become-pass cannot be combined with --become-password-file; use one of them")
				}
			}
			if selectedVM.HostOs == alchemy_build.HostOsWindows &&
				options.LocalWindowsProtocol == alchemy_provision.LocalWindowsProvisionProtocolSSH &&
				forceWinRMUninstall {
				return fmt.Errorf("❌ --force-winrm-uninstall is only supported with local Windows --proto winrm")
			}
			if selectedVM.HostOs == alchemy_build.HostOsWindows &&
				options.LocalWindowsProtocol == alchemy_provision.LocalWindowsProvisionProtocolWinRM &&
				forceSSHUninstall {
				return fmt.Errorf("❌ --force-ssh-uninstall is only supported with local Windows --proto ssh")
			}

			if isLocalProvisionUnstable(selectedVM.HostOs) {
				fmt.Printf("⚠️ Local provisioning on host OS %s is currently marked unstable and has not been validated end-to-end yet.\n", selectedVM.HostOs)
			}
			if err := confirmProvisionIntent(cmd, selectedVM, options); err != nil {
				return err
			}

			fmt.Printf("🔧 Provisioning local host for OS: %s (proto=%s, check=%t)\n", selectedVM.HostOs, options.LocalWindowsProtocol, check)
			if err := runProvision(selectedVM, options); err != nil {
				return fmt.Errorf("failed provisioning local host for host_os=%s: %w", selectedVM.HostOs, err)
			}

			return nil
		}

		if cmd.Flags().Changed("inventory-path") {
			return fmt.Errorf("❌ --inventory-path is only supported for local provisioning; for VM targets, pass manual ansible inventory flags after `--` if needed")
		}
		if cmd.Flags().Changed("proto") {
			return fmt.Errorf("❌ --proto is only supported for local Windows provisioning")
		}
		if forceWinRMUninstall {
			return fmt.Errorf("❌ --force-winrm-uninstall is only supported for local Windows provisioning")
		}
		if forceSSHUninstall {
			return fmt.Errorf("❌ --force-ssh-uninstall is only supported for local Windows provisioning")
		}
		if askBecomePass {
			return fmt.Errorf("❌ --ask-become-pass is only supported for local provisioning; Sailwright sets the credentials for VM targets")
		}

		if osName != "ubuntu" {
			osType = ""
		}

		availableVirtualMachines := availableProvisionVirtualMachines()
		var selectedVM alchemy_build.VirtualMachineConfig
		valid := false
		for _, vm := range availableVirtualMachines {
			if vm.OS == osName && vm.UbuntuType == osType && vm.Arch == arch {
				selectedVM = vm
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("❌ invalid combination: OS=%s, Type=%s, Arch=%s", osName, osType, arch)
		}

		fmt.Printf("🔧 Provisioning VM for OS: %s, Type: %s, Architecture: %s (check=%t)\n", osName, osType, arch, check)
		printUnstableTargetWarning(selectedVM)
		if err := runProvision(selectedVM, options); err != nil {
			return fmt.Errorf("failed provisioning for OS=%s, type=%s, arch=%s: %w", osName, osType, arch, err)
		}

		return nil
	},
}

var provisionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available provision combinations",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return printAvailableProvisionCombinations()
	},
}

func runProvision(vm alchemy_build.VirtualMachineConfig, options alchemy_provision.ProvisionOptions) error {
	return runProvisionFunc(vm, options)
}

func init() {
	rootCmd.AddCommand(provisionCmd)
	provisionCmd.AddCommand(provisionListCmd)
	addListJSONFlag(provisionListCmd)

	provisionCmd.Flags().StringVarP(&arch, "arch", "a", "amd64", "Target architecture (e.g., amd64, arm64)")
	provisionCmd.Flags().StringVarP(&osType, "type", "t", "server", "Type of OS (e.g., server, desktop)")
	provisionCmd.Flags().BoolVar(&check, "check", false, "Run ansible with --check (dry-run)")
	provisionCmd.Flags().BoolVar(&check, "dry-run", false, "Alias for --check")
	provisionCmd.Flags().BoolVarP(&askBecomePass, "ask-become-pass", "K", false, "Ask for the sudo password that tasks with become need (local macOS and Linux provisioning only)")
	provisionCmd.Flags().StringVar(&localProvisionProto, "proto", string(alchemy_provision.DefaultLocalWindowsProvisionProtocol()), "Local Windows provision transport: winrm or ssh")
	provisionCmd.Flags().IntVar(&ansibleVerbosity, "verbosity", 3, "Ansible verbosity level (0-4). Default 3 is equivalent to -vvv")
	provisionCmd.Flags().StringVar(&playbookPath, "playbook", "", "Override the Ansible playbook path")
	provisionCmd.Flags().StringVar(&inventoryPath, "inventory-path", "", "Override the default inventory file for local provisioning; pass -- --limit <host-pattern> if your custom inventory needs a target")
	provisionCmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "Skip confirmation prompts for operations that change local system state")
	provisionCmd.Flags().BoolVar(&forceWinRMUninstall, "force-winrm-uninstall", false, "For local Windows provisioning, force cleanup to disable WinRM and remove transient setup after the run")
	provisionCmd.Flags().BoolVar(&forceSSHUninstall, "force-ssh-uninstall", false, "For local Windows SSH provisioning, force cleanup to disable sshd, remove SSH firewall rules, and remove the transient Ansible user after the run without uninstalling OpenSSH Server")
	provisionCmd.Flags().Lookup("dry-run").Hidden = true
}

func validateProvisionCommandArgs(cmd *cobra.Command, args []string) error {
	positionalArgCount := len(args)
	if dashIndex := cmd.ArgsLenAtDash(); dashIndex >= 0 {
		positionalArgCount = dashIndex
	}
	if positionalArgCount != 1 {
		return fmt.Errorf("accepts 1 arg(s), received %d", positionalArgCount)
	}

	return nil
}

func splitProvisionArgs(cmd *cobra.Command, args []string) (string, []string) {
	positionalArgCount := len(args)
	if dashIndex := cmd.ArgsLenAtDash(); dashIndex >= 0 {
		positionalArgCount = dashIndex
	}

	extraAnsibleArgs := make([]string, 0, len(args)-positionalArgCount)
	if positionalArgCount < len(args) {
		extraAnsibleArgs = append(extraAnsibleArgs, args[positionalArgCount:]...)
	}

	return args[0], extraAnsibleArgs
}

// hasBecomePasswordFileArg reports whether the passthrough Ansible args already supply
// a become password file, which ansible-playbook rejects together with --ask-become-pass.
func hasBecomePasswordFileArg(args []string) bool {
	for _, arg := range args {
		name, _, _ := strings.Cut(arg, "=")
		if name == "--become-password-file" || name == "--become-pass-file" {
			return true
		}
	}
	return false
}

func confirmProvisionIntent(cmd *cobra.Command, vm alchemy_build.VirtualMachineConfig, options alchemy_provision.ProvisionOptions) error {
	if assumeYes || !requiresProvisionConfirmation(vm) {
		return nil
	}

	message := localWindowsProvisionConfirmationMessage(options)

	if !isInteractiveInput(cmd.InOrStdin()) {
		return fmt.Errorf("%s Re-run with --yes to continue non-interactively", message)
	}

	confirmed, err := promptForConfirmationFunc(
		cmd.InOrStdin(),
		cmd.OutOrStdout(),
		message+" Continue? [y/N]: ",
	)
	if err != nil {
		return fmt.Errorf("failed to read confirmation for local Windows provisioning: %w", err)
	}
	if !confirmed {
		return fmt.Errorf("local Windows provisioning cancelled")
	}

	return nil
}

func localWindowsProvisionConfirmationMessage(options alchemy_provision.ProvisionOptions) string {
	if options.LocalWindowsProtocol == alchemy_provision.LocalWindowsProvisionProtocolSSH {
		message := "Local Windows provisioning over SSH will temporarily install or reconfigure OpenSSH Server, set the default SSH shell to PowerShell, create or update a temporary local administrator account for Ansible, and authorize a temporary SSH key. Cleanup restores the prior SSH service, firewall, authorized_keys, and shell state, but if this run had to install OpenSSH Server it will leave that capability installed and disable sshd so cleanup does not require a reboot. If the existing account is reused, its password will be rotated for the run and cleanup will not restore the previous password. Windows will also show a UAC elevation prompt for the setup and cleanup steps."
		if options.LocalWindowsForceSSHUninstall {
			message += " Because --force-ssh-uninstall is set, cleanup will aggressively disable sshd, remove SSH firewall rules, and remove the transient Ansible user even if the run fails. OpenSSH Server will remain installed so cleanup does not require a reboot."
		}
		return message
	}

	message := "Local Windows provisioning over WinRM will temporarily enable or reconfigure WinRM, create or update a temporary local administrator account for Ansible, and create a self-signed HTTPS listener. Windows will also show a UAC elevation prompt for the setup and cleanup steps."
	if options.LocalWindowsForceWinRMUninstall {
		message += " Because --force-winrm-uninstall is set, cleanup will aggressively disable WinRM and remove transient configuration even if the run fails."
	}

	return message
}

func requiresProvisionConfirmation(vm alchemy_build.VirtualMachineConfig) bool {
	return vm.OS == "local" && vm.HostOs == alchemy_build.HostOsWindows
}

func isInteractiveInput(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return true
	}

	// A character-device check would also accept /dev/null, which is what
	// non-interactive callers such as the agent gateway attach as stdin.
	return term.IsTerminal(int(file.Fd())) // #nosec G115 -- file descriptors fit in int.
}

func promptForConfirmation(input io.Reader, output io.Writer, prompt string) (bool, error) {
	if _, err := fmt.Fprint(output, prompt); err != nil {
		return false, err
	}

	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
