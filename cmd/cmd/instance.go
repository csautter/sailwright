package cmd

import (
	"fmt"
	provision "github.com/csautter/dev-alchemy/pkg/provision"
	"os"

	build "github.com/csautter/dev-alchemy/pkg/build"
	deploy "github.com/csautter/dev-alchemy/pkg/deploy"
	"github.com/spf13/cobra"
)

var discoverNamedInstances = deploy.DiscoverInstances

func addInstanceFlag(cmd *cobra.Command) {
	cmd.Flags().String("name", "", "VM instance name (omit for the existing default instance)")
}

func commandInstanceName(cmd *cobra.Command) string {
	name, _ := cmd.Flags().GetString("name")
	return name
}

func validateInstanceSelector(cmd *cobra.Command, args []string) error {
	name := commandInstanceName(cmd)
	if cmd.Flags().Changed("name") && name == "" {
		return fmt.Errorf("--name requires a nonempty instance name")
	}
	if err := build.ValidateInstanceName(name); err != nil {
		return err
	}
	all, _ := cmd.Flags().GetBool("all-instances")
	if len(args) > 0 && (args[0] == "all" || args[0] == "local") && name != "" {
		return fmt.Errorf("--name cannot be combined with %s", args[0])
	}
	if all && (len(args) == 0 || args[0] != "all") {
		return fmt.Errorf("--all-instances requires the all target")
	}
	return nil
}

func includeAllInstances(cmd *cobra.Command, bases []build.VirtualMachineConfig) ([]build.VirtualMachineConfig, error) {
	all, _ := cmd.Flags().GetBool("all-instances")
	if !all {
		return bases, nil
	}
	named, err := discoverNamedInstances(bases)
	if err != nil {
		return nil, err
	}
	return append(bases, named...), nil
}

func listingInstances(bases []build.VirtualMachineConfig) []build.VirtualMachineConfig {
	named, err := discoverNamedInstances(bases)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot discover named VM instances: %v\n", err)
		return bases
	}
	return append(bases, named...)
}

func configureNamedGuest(vm build.VirtualMachineConfig) error {
	if err := deploy.ConfigureNamedGuestHostname(vm); err != nil {
		return err
	}
	if vm.Name != "" && vm.VirtualizationEngine == build.VirtualizationEngineTart {
		return provision.ConfigureTartInstanceHostname(vm)
	}
	return nil
}
