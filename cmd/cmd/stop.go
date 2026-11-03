package cmd

import (
	"fmt"
	"os"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_deploy "github.com/csautter/dev-alchemy/pkg/deploy"
	"github.com/spf13/cobra"
)

var runStopFunc = runStop

var inspectStopTarget = alchemy_deploy.InspectStopTarget

func isStopSupported(vm alchemy_build.VirtualMachineConfig) bool {
	return alchemy_deploy.SupportsStop(vm)
}

func availableStopVirtualMachines() []alchemy_build.VirtualMachineConfig {
	var supported []alchemy_build.VirtualMachineConfig
	for _, vm := range alchemy_build.AvailableVirtualMachineConfigsForCurrentHostOS() {
		if isStopSupported(vm) {
			supported = append(supported, vm)
		}
	}
	return supported
}

func printAvailableStopCombinations() error {
	vms := listingInstances(availableStopVirtualMachines())
	return printVirtualMachineCombinationTable(
		os.Stdout,
		fmt.Sprintf("Available stop combinations for host OS: %s", alchemy_build.GetCurrentHostOs()),
		"No stop combinations are available for the current host OS.",
		vms,
		[]string{"OS", "Type", "Arch", "State", "Stop"},
		func(vm alchemy_build.VirtualMachineConfig) ([]string, error) {
			state, err := inspectStopTarget(vm)
			if err != nil {
				return nil, fmt.Errorf("failed to inspect stop target for OS=%s, type=%s, arch=%s: %w", vm.OS, vm.UbuntuType, vm.Arch, err)
			}

			stopState := "ready to stop"
			displayState := state.State
			switch {
			case !state.Exists:
				displayState = "missing"
				stopState = "already absent"
			case state.Running:
				if displayState == "" {
					displayState = "running"
				}
			default:
				if displayState == "" {
					displayState = "stopped"
				}
				stopState = "already stopped"
			}

			return []string{vm.OS, displayVirtualMachineType(vm), vm.Arch, displayState, stopState}, nil
		},
	)
}

var stopCmd = &cobra.Command{
	Use:   "stop <osname>",
	Short: "Stop an existing VM on your system",
	Long: `Stops an existing VM on your system without deleting it.
Use "all" to stop default VM instances for the current host OS.
Add --all-instances to include named instances.

Examples:
  sailwright stop ubuntu --type server --arch amd64
  sailwright stop macos --arch arm64
  sailwright stop windows11 --arch arm64
  sailwright stop all
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateInstanceSelector(cmd, args); err != nil {
			return err
		}
		osName := args[0]

		osType = alchemy_build.TargetType(osName, osType)

		if osName == "all" {
			fmt.Println("🔧 Stopping all available VM configurations")
			vms, err := includeAllInstances(cmd, availableStopVirtualMachines())
			if err != nil {
				return err
			}
			for _, vm := range vms {
				fmt.Printf("➡️ Stopping VM for OS: %s, Type: %s, Architecture: %s\n", vm.OS, vm.UbuntuType, vm.Arch)
				if err := runStopFunc(vm); err != nil {
					return fmt.Errorf("failed stopping VM for OS=%s, type=%s, arch=%s: %w", vm.OS, vm.UbuntuType, vm.Arch, err)
				}
			}
			return nil
		}

		availableVirtualMachines := availableStopVirtualMachines()
		var selectedVM alchemy_build.VirtualMachineConfig
		valid := false
		for _, vm := range availableVirtualMachines {
			if vm.OS == osName && vm.UbuntuType == osType && vm.Arch == arch {
				selectedVM = vm
				selectedVM.Name = commandInstanceName(cmd)
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("❌ invalid combination: OS=%s, Type=%s, Arch=%s", osName, osType, arch)
		}

		if err := alchemy_build.ValidateInstance(selectedVM); err != nil {
			return err
		}

		fmt.Printf("🔧 Stopping VM for OS: %s, Type: %s, Architecture: %s\n", osName, osType, arch)
		if err := runStopFunc(selectedVM); err != nil {
			return fmt.Errorf("failed stopping VM for OS=%s, type=%s, arch=%s: %w", osName, osType, arch, err)
		}

		return nil
	},
}

var stopListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available stop combinations and stop readiness",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return printAvailableStopCombinations()
	},
}

func runStop(vm alchemy_build.VirtualMachineConfig) error {
	return alchemy_deploy.RunStop(vm)
}

func init() {
	rootCmd.AddCommand(stopCmd)
	addInstanceFlag(stopCmd)
	stopCmd.Flags().Bool("all-instances", false, "Include named instances when stopping or destroying all VMs")
	stopCmd.AddCommand(stopListCmd)
	addListJSONFlag(stopListCmd)

	stopCmd.Flags().StringVarP(&arch, "arch", "a", "amd64", "Target architecture (e.g., amd64, arm64)")
	stopCmd.Flags().StringVarP(&osType, "type", "t", "server", "Type of OS (e.g., server, desktop)")
}
