package cmd

import (
	"fmt"
	"os"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_deploy "github.com/csautter/dev-alchemy/pkg/deploy"
	"github.com/spf13/cobra"
)

var inspectDestroyTargetExists = alchemy_deploy.DestroyTargetExists

func isDestroySupported(vm alchemy_build.VirtualMachineConfig) bool {
	return alchemy_deploy.SupportsDestroy(vm)
}

func availableDestroyVirtualMachines() []alchemy_build.VirtualMachineConfig {
	var supported []alchemy_build.VirtualMachineConfig
	for _, vm := range alchemy_build.AvailableVirtualMachineConfigsForCurrentHostOS() {
		if isDestroySupported(vm) {
			supported = append(supported, vm)
		}
	}
	return supported
}

func printAvailableDestroyCombinations() error {
	vms := availableDestroyVirtualMachines()
	return printVirtualMachineCombinationTable(
		os.Stdout,
		fmt.Sprintf("Available destroy combinations for host OS: %s", alchemy_build.GetCurrentHostOs()),
		"No destroy combinations are available for the current host OS.",
		vms,
		[]string{"OS", "Type", "Arch", "State", "Destroy"},
		func(vm alchemy_build.VirtualMachineConfig) ([]string, error) {
			exists, err := inspectDestroyTargetExists(vm)
			if err != nil {
				return nil, fmt.Errorf("failed to inspect destroy target for OS=%s, type=%s, arch=%s: %w", vm.OS, vm.UbuntuType, vm.Arch, err)
			}

			state := "missing"
			destroyState := "already absent"
			if exists {
				state = "exists"
				destroyState = "ready to destroy"
			}

			return []string{vm.OS, displayVirtualMachineType(vm), vm.Arch, state, destroyState}, nil
		},
	)
}

var destroyCmd = &cobra.Command{
	Use:   "destroy <osname>",
	Short: "Destroy a VM previously created on your system",
	Long: `Destroys a VM previously created on your system.
Use "all" to destroy all available VM configurations for the current host OS.

Examples:
  sailwright destroy ubuntu --type server --arch amd64
  sailwright destroy macos --arch arm64
  sailwright destroy windows11 --arch arm64
  sailwright destroy all
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		osName := args[0]

		if osName != "ubuntu" {
			osType = ""
		}

		if osName == "all" {
			fmt.Println("🔧 Destroying all available VM configurations")
			for _, vm := range availableDestroyVirtualMachines() {
				fmt.Printf("➡️ Destroying VM for OS: %s, Type: %s, Architecture: %s\n", vm.OS, vm.UbuntuType, vm.Arch)
				if err := runDestroy(vm); err != nil {
					return fmt.Errorf("failed destroying VM for OS=%s, type=%s, arch=%s: %w", vm.OS, vm.UbuntuType, vm.Arch, err)
				}
			}
			return nil
		}

		availableVirtualMachines := availableDestroyVirtualMachines()
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

		fmt.Printf("🔧 Destroying VM for OS: %s, Type: %s, Architecture: %s\n", osName, osType, arch)
		if err := runDestroy(selectedVM); err != nil {
			return fmt.Errorf("failed destroying VM for OS=%s, type=%s, arch=%s: %w", osName, osType, arch, err)
		}

		return nil
	},
}

var destroyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available destroy combinations and destroy readiness",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return printAvailableDestroyCombinations()
	},
}

func runDestroy(vm alchemy_build.VirtualMachineConfig) error {
	return alchemy_deploy.RunDestroy(vm)
}

func init() {
	rootCmd.AddCommand(destroyCmd)
	destroyCmd.AddCommand(destroyListCmd)
	addListJSONFlag(destroyListCmd)

	destroyCmd.Flags().StringVarP(&arch, "arch", "a", "amd64", "Target architecture (e.g., amd64, arm64)")
	destroyCmd.Flags().StringVarP(&osType, "type", "t", "server", "Type of OS (e.g., server, desktop)")
}
