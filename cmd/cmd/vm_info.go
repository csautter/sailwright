package cmd

import (
	"encoding/json"
	"fmt"

	alchemy_provision "github.com/csautter/dev-alchemy/pkg/provision"
	"github.com/spf13/cobra"
)

var inspectVMInfo = alchemy_provision.InspectVM

func newVMCommand() *cobra.Command {
	var typ, arch string
	var outputJSON bool
	vm := &cobra.Command{Use: "vm", Short: "Inspect managed virtual machines"}
	info := &cobra.Command{
		Use: "info <os>", Short: "Show connection details for a running VM", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetType := typ
			if args[0] != "ubuntu" {
				targetType = ""
			}
			for _, target := range availableCreateVirtualMachines() {
				if target.OS == args[0] && target.UbuntuType == targetType && target.Arch == arch {
					value, err := inspectVMInfo(target)
					if err != nil {
						return err
					}
					if outputJSON {
						return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
					}
					_, err = fmt.Fprintf(cmd.OutOrStdout(), "VM: %s/%s/%s\nState: %s\nEngine: %s\nAddress: %s\nNetwork: %s\nProtocol: %s\nPort: %d\nUsername: %s\nPassword: %s\n", value.OS, value.Type, value.Arch, value.State, value.Engine, value.Address, value.NetworkMode, value.Protocol, value.Port, value.Username, value.Password)
					return err
				}
			}
			return fmt.Errorf("unknown managed VM: %s/%s/%s", args[0], targetType, arch)
		},
	}
	info.Flags().StringVar(&typ, "type", "server", "Ubuntu VM type")
	info.Flags().StringVar(&arch, "arch", "amd64", "VM architecture")
	info.Flags().BoolVar(&outputJSON, "json", false, "Print versioned JSON including guest credentials")
	vm.AddCommand(info)
	return vm
}

func init() { rootCmd.AddCommand(newVMCommand()) }
