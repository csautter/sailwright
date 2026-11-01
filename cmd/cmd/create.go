package cmd

import (
	"fmt"
	"os"
	"strings"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_deploy "github.com/csautter/dev-alchemy/pkg/deploy"
	"github.com/spf13/cobra"
)

var (
	osType string
)

var inspectCreateTargetExists = alchemy_deploy.CreateTargetExists
var inspectCreateArtifactExists = alchemy_build.BuildArtifactsExistQuiet
var runDeployFunc = runDeploy

func isCreateSupported(vm alchemy_build.VirtualMachineConfig) bool {
	return alchemy_deploy.SupportsCreate(vm)
}

func availableCreateVirtualMachinesForHostOS(hostOs alchemy_build.HostOsType) []alchemy_build.VirtualMachineConfig {
	return availableVirtualMachinesForHostOS(hostOs, isCreateSupported)
}

func availableCreateVirtualMachines() []alchemy_build.VirtualMachineConfig {
	return availableCreateVirtualMachinesForHostOS(alchemy_build.GetCurrentHostOs())
}

func defaultCreateVirtualMachinesForHostOS(hostOs alchemy_build.HostOsType) []alchemy_build.VirtualMachineConfig {
	return stableVirtualMachines(availableCreateVirtualMachinesForHostOS(hostOs))
}

func printAvailableCreateCombinations() error {
	vms := availableCreateVirtualMachines()
	return printVirtualMachineCombinationTable(
		os.Stdout,
		fmt.Sprintf("Available create combinations for host OS: %s", alchemy_build.GetCurrentHostOs()),
		"No create combinations are available for the current host OS.",
		vms,
		[]string{"OS", "Type", "Arch", "Status", "Artifact", "Create"},
		func(vm alchemy_build.VirtualMachineConfig) ([]string, error) {
			targetExists, err := inspectCreateTargetExists(vm)
			if err != nil {
				return nil, fmt.Errorf("failed to inspect create target for OS=%s, type=%s, arch=%s: %w", vm.OS, vm.UbuntuType, vm.Arch, err)
			}

			if vm.VirtualizationEngine == alchemy_build.VirtualizationEngineTart {
				createState := "ready to create"
				if targetExists {
					createState = "already created"
				}
				return []string{vm.OS, displayVirtualMachineType(vm), vm.Arch, virtualMachineTargetStatus(vm), "public image", createState}, nil
			}

			artifactsExist, err := inspectCreateArtifactExists(vm)
			if err != nil {
				return nil, fmt.Errorf("failed to check build artifacts for OS=%s, type=%s, arch=%s: %w", vm.OS, vm.UbuntuType, vm.Arch, err)
			}

			artifactState := "missing"
			createState := "build required"
			if targetExists {
				createState = "already created"
			}
			if artifactsExist {
				artifactState = "exists"
				if !targetExists {
					createState = "ready to create"
				}
			}

			return []string{vm.OS, displayVirtualMachineType(vm), vm.Arch, virtualMachineTargetStatus(vm), artifactState, createState}, nil
		},
	)
}

func runCreateAll(vms []alchemy_build.VirtualMachineConfig) error {
	for _, vm := range vms {
		fmt.Printf("➡️ Creating VM for OS: %s, Type: %s, Architecture: %s\n", vm.OS, vm.UbuntuType, vm.Arch)
		if err := runDeployFunc(vm); err != nil {
			return fmt.Errorf("failed creating VM for OS=%s, type=%s, arch=%s: %w", vm.OS, vm.UbuntuType, vm.Arch, err)
		}
	}
	return nil
}

// createCmd represents the create command
var createCmd = &cobra.Command{
	Use:   "create <osname>",
	Short: "Creates a new VM on your system with the defined OS",
	Long: `Creates a new VM on your system with the defined OS.
Use "all" to create all stable VM configurations.

Example:
  sailwright create ubuntu --type server --arch amd64
  sailwright create macos --arch arm64
  sailwright create windows11 --arch arm64
  sailwright create all
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		osName := args[0]

		osType = alchemy_build.TargetType(osName, osType)

		if osName == "all" {
			availableVMs := availableCreateVirtualMachines()
			fmt.Println("🔧 Creating all stable VM configurations")
			printSkippedUnstableTargets("create", availableVMs)
			return runCreateAll(stableVirtualMachines(availableVMs))
		}

		available_virtual_machines := availableCreateVirtualMachines()
		var VirtualMachineConfig alchemy_build.VirtualMachineConfig
		valid := false
		for _, vm := range available_virtual_machines {
			if vm.OS == osName && vm.UbuntuType == osType && vm.Arch == arch {
				valid = true
				VirtualMachineConfig = vm
				break
			}
		}
		if !valid {
			return fmt.Errorf("❌ Invalid combination: OS=%s, Type=%s, Arch=%s", osName, osType, arch)
		}

		fmt.Printf("🔧 Creating VM for OS: %s, Architecture: %s, Type: %s\n", osName, arch, osType)
		printUnstableTargetWarning(VirtualMachineConfig)
		if err := runDeployFunc(VirtualMachineConfig); err != nil {
			return fmt.Errorf("failed creating VM for OS=%s, type=%s, arch=%s: %w", osName, osType, arch, err)
		}
		return nil
	},
}

var createListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available create combinations and create readiness",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return printAvailableCreateCombinations()
	},
}

func runDeploy(vm alchemy_build.VirtualMachineConfig) error {
	targetExists, err := inspectCreateTargetExists(vm)
	if err != nil {
		return fmt.Errorf("failed to inspect create target for OS=%s, type=%s, arch=%s: %w", vm.OS, vm.UbuntuType, vm.Arch, err)
	}
	if targetExists {
		return fmt.Errorf(
			"VM for %s already exists. Use `sailwright start %s` to reuse it or `sailwright destroy %s` first",
			createCommandArguments(vm),
			createCommandArguments(vm),
			createCommandArguments(vm),
		)
	}
	if err := markCreateStarted(os.Getenv(createStartedFileEnvVar), vm); err != nil {
		return err
	}

	switch vm.VirtualizationEngine {
	case alchemy_build.VirtualizationEngineUtm:
		return alchemy_deploy.RunUtmDeployOnMacOS(vm)
	case alchemy_build.VirtualizationEngineTart:
		return alchemy_deploy.RunTartDeployOnMacOS(vm)
	case alchemy_build.VirtualizationEngineHyperv:
		return alchemy_deploy.RunHypervVagrantDeployOnWindows(vm)
	case alchemy_build.VirtualizationEngineQemu:
		return alchemy_deploy.RunLinuxQemuDeployOnLinux(vm)
	default:
		return fmt.Errorf("❌ deploy is not implemented for virtualization engine: %s", vm.VirtualizationEngine)
	}
}

// createStartedFileEnvVar names a file that create writes once it has confirmed,
// under the target's operation lock, that no VM exists and deployment begins.
// sailwright-gateway sets it so a failed create still counts as the gateway's VM.
const createStartedFileEnvVar = "SAILWRIGHT_CREATE_STARTED_FILE"

func markCreateStarted(path string, vm alchemy_build.VirtualMachineConfig) error {
	if path == "" {
		return nil
	}
	target := vm.OS + "/" + vm.UbuntuType + "/" + vm.Arch + "\n"
	// #nosec G306 G703 -- the path is chosen by the gateway that started this process.
	if err := os.WriteFile(path, []byte(target), 0o600); err != nil {
		return fmt.Errorf("record create start for %s: %w", createStartedFileEnvVar, err)
	}
	return nil
}

func createCommandArguments(vm alchemy_build.VirtualMachineConfig) string {
	args := []string{vm.OS}
	if vm.UbuntuType != "" {
		args = append(args, "--type", vm.UbuntuType)
	}
	if vm.Arch != "" {
		args = append(args, "--arch", vm.Arch)
	}
	return strings.Join(args, " ")
}

func init() {
	rootCmd.AddCommand(createCmd)
	createCmd.AddCommand(createListCmd)
	addListJSONFlag(createListCmd)

	createCmd.Flags().StringVarP(&arch, "arch", "a", "amd64", "Target architecture (e.g., amd64, arm64)")
	createCmd.Flags().StringVarP(&osType, "type", "t", "server", "Type of OS (e.g., server, desktop)")
}
