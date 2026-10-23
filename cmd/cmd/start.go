package cmd

import (
	"fmt"
	"os"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_deploy "github.com/csautter/dev-alchemy/pkg/deploy"
	"github.com/spf13/cobra"
)

var inspectStartTarget = alchemy_deploy.InspectStartTarget
var runStartFunc = runStart

func isStartSupported(vm alchemy_build.VirtualMachineConfig) bool {
	return alchemy_deploy.SupportsStart(vm)
}

func availableStartVirtualMachinesForHostOS(hostOs alchemy_build.HostOsType) []alchemy_build.VirtualMachineConfig {
	return availableVirtualMachinesForHostOS(hostOs, isStartSupported)
}

func availableStartVirtualMachines() []alchemy_build.VirtualMachineConfig {
	return availableStartVirtualMachinesForHostOS(alchemy_build.GetCurrentHostOs())
}

func defaultStartVirtualMachinesForHostOS(hostOs alchemy_build.HostOsType) []alchemy_build.VirtualMachineConfig {
	return stableVirtualMachines(availableStartVirtualMachinesForHostOS(hostOs))
}

func printAvailableStartCombinations() error {
	vms := availableStartVirtualMachines()
	return printVirtualMachineCombinationTable(
		os.Stdout,
		fmt.Sprintf("Available start combinations for host OS: %s", alchemy_build.GetCurrentHostOs()),
		"No start combinations are available for the current host OS.",
		vms,
		[]string{"OS", "Type", "Arch", "Status", "State", "Start"},
		func(vm alchemy_build.VirtualMachineConfig) ([]string, error) {
			state, err := inspectStartTarget(vm)
			if err != nil {
				return nil, fmt.Errorf("failed to inspect start target for OS=%s, type=%s, arch=%s: %w", vm.OS, vm.UbuntuType, vm.Arch, err)
			}

			startState := "ready to start"
			displayState := state.State
			switch {
			case !state.Exists:
				displayState = "missing"
				startState = "create required"
			case state.Running:
				startState = "already running"
			case displayState == "":
				displayState = "stopped"
			}

			return []string{vm.OS, displayVirtualMachineType(vm), vm.Arch, virtualMachineTargetStatus(vm), displayState, startState}, nil
		},
	)
}

func runStartAll(vms []alchemy_build.VirtualMachineConfig) error {
	for _, vm := range vms {
		fmt.Printf("➡️ Starting VM for OS: %s, Type: %s, Architecture: %s\n", vm.OS, vm.UbuntuType, vm.Arch)
		if err := runStartFunc(vm); err != nil {
			return fmt.Errorf("failed starting VM for OS=%s, type=%s, arch=%s: %w", vm.OS, vm.UbuntuType, vm.Arch, err)
		}
	}
	return nil
}

var startCmd = &cobra.Command{
	Use:   "start <osname>",
	Short: "Start an existing VM on your system",
	Long: `Starts an existing VM on your system.
Use "all" to start all stable VM configurations for the current host OS.

Examples:
  sailwright start ubuntu --type server --arch amd64
  sailwright start macos --arch arm64
  sailwright start windows11 --arch arm64
  sailwright start all
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		osName := args[0]

		if osName != "ubuntu" {
			osType = ""
		}

		if osName == "all" {
			availableVMs := availableStartVirtualMachines()
			fmt.Println("🔧 Starting all stable VM configurations")
			printSkippedUnstableTargets("start", availableVMs)
			return runStartAll(stableVirtualMachines(availableVMs))
		}

		availableVirtualMachines := availableStartVirtualMachines()
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

		fmt.Printf("🔧 Starting VM for OS: %s, Type: %s, Architecture: %s\n", osName, osType, arch)
		printUnstableTargetWarning(selectedVM)
		if err := runStartFunc(selectedVM); err != nil {
			return fmt.Errorf("failed starting VM for OS=%s, type=%s, arch=%s: %w", osName, osType, arch, err)
		}

		return nil
	},
}

var startListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available start combinations and start readiness",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return printAvailableStartCombinations()
	},
}

func runStart(vm alchemy_build.VirtualMachineConfig) error {
	return alchemy_deploy.RunStart(vm)
}

func init() {
	rootCmd.AddCommand(startCmd)
	startCmd.AddCommand(startListCmd)
	addListJSONFlag(startListCmd)

	startCmd.Flags().StringVarP(&arch, "arch", "a", "amd64", "Target architecture (e.g., amd64, arm64)")
	startCmd.Flags().StringVarP(&osType, "type", "t", "server", "Type of OS (e.g., server, desktop)")
}
