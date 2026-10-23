package cmd

import (
	"fmt"
	"os"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_deploy "github.com/csautter/dev-alchemy/pkg/deploy"
	"github.com/spf13/cobra"
)

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
	vms := availableStopVirtualMachines()
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
Use "all" to stop all available VM configurations for the current host OS.

Examples:
  sailwright stop ubuntu --type server --arch amd64
  sailwright stop macos --arch arm64
  sailwright stop windows11 --arch arm64
  sailwright stop all
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		osName := args[0]

		if osName != "ubuntu" {
			osType = ""
		}

		if osName == "all" {
			fmt.Println("🔧 Stopping all available VM configurations")
			for _, vm := range availableStopVirtualMachines() {
				fmt.Printf("➡️ Stopping VM for OS: %s, Type: %s, Architecture: %s\n", vm.OS, vm.UbuntuType, vm.Arch)
				if err := runStop(vm); err != nil {
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
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("❌ invalid combination: OS=%s, Type=%s, Arch=%s", osName, osType, arch)
		}

		fmt.Printf("🔧 Stopping VM for OS: %s, Type: %s, Architecture: %s\n", osName, osType, arch)
		if err := runStop(selectedVM); err != nil {
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
	stopCmd.AddCommand(stopListCmd)
	addListJSONFlag(stopListCmd)

	stopCmd.Flags().StringVarP(&arch, "arch", "a", "amd64", "Target architecture (e.g., amd64, arm64)")
	stopCmd.Flags().StringVarP(&osType, "type", "t", "server", "Type of OS (e.g., server, desktop)")
}
