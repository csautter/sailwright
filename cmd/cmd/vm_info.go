package cmd

import (
	"encoding/json"
	"fmt"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_provision "github.com/csautter/dev-alchemy/pkg/provision"
	"github.com/spf13/cobra"
)

var (
	inspectVMInfo     = alchemy_provision.InspectVM
	inspectVMIdentity = alchemy_provision.InspectVMIdentity
)

const maskedPassword = "******** (use --show-password or --json)"

func newVMCommand() *cobra.Command {
	var typ, arch string
	var outputJSON, showPassword bool
	vm := &cobra.Command{Use: "vm", Short: "Inspect managed virtual machines"}
	info := &cobra.Command{
		Use: "info <os>", Short: "Show connection details for a running VM", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateInstanceSelector(cmd, args); err != nil {
				return err
			}
			targetType := alchemy_build.TargetType(args[0], typ)
			for _, target := range availableCreateVirtualMachines() {
				if target.OS == args[0] && target.UbuntuType == targetType && target.Arch == arch {
					target.Name = commandInstanceName(cmd)
					if err := alchemy_build.ValidateInstance(target); err != nil {
						return err
					}
					value, err := inspectVMInfo(target)
					if err != nil {
						return err
					}
					if outputJSON {
						// #nosec G117 -- this explicit guest-info command intentionally returns guest credentials.
						return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
					}
					// Text output lands in scrollback and screen shares, so the
					// password is only shown on request.
					password := maskedPassword
					if showPassword {
						password = value.Password
					}
					_, err = fmt.Fprintf(cmd.OutOrStdout(), "VM: %s/%s/%s\nState: %s\nEngine: %s\nAddress: %s\nNetwork: %s\nProtocol: %s\nPort: %d\nUsername: %s\nPassword: %s\n", value.OS, value.Type, value.Arch, value.State, value.Engine, value.Address, value.NetworkMode, value.Protocol, value.Port, value.Username, password)
					return err
				}
			}
			return fmt.Errorf("unknown managed VM: %s/%s/%s", args[0], targetType, arch)
		},
	}
	addInstanceFlag(info)
	info.Flags().StringVar(&typ, "type", "server", "Ubuntu VM type")
	info.Flags().StringVar(&arch, "arch", "amd64", "VM architecture")
	info.Flags().BoolVar(&outputJSON, "json", false, "Print versioned JSON including guest credentials")
	info.Flags().BoolVar(&showPassword, "show-password", false, "Show the guest password in text output (JSON always includes it)")
	vm.AddCommand(info, newVMIdentityCommand())
	return vm
}

// newVMIdentityCommand reports which VM instance exists for a target, also when it
// is stopped. sailwright-gateway uses it to notice a VM recreated under a known name.
func newVMIdentityCommand() *cobra.Command {
	var typ, arch string
	identity := &cobra.Command{
		Use: "identity <os>", Short: "Print whether a VM exists and its hypervisor instance id as JSON", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateInstanceSelector(cmd, args); err != nil {
				return err
			}
			targetType := alchemy_build.TargetType(args[0], typ)
			for _, target := range availableCreateVirtualMachines() {
				if target.OS == args[0] && target.UbuntuType == targetType && target.Arch == arch {
					target.Name = commandInstanceName(cmd)
					if err := alchemy_build.ValidateInstance(target); err != nil {
						return err
					}
					value, err := inspectVMIdentity(target)
					if err != nil {
						return err
					}
					return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
				}
			}
			return fmt.Errorf("unknown managed VM: %s/%s/%s", args[0], targetType, arch)
		},
	}
	addInstanceFlag(identity)
	identity.Flags().StringVar(&typ, "type", "server", "Ubuntu VM type")
	identity.Flags().StringVar(&arch, "arch", "amd64", "VM architecture")
	return identity
}

func init() { rootCmd.AddCommand(newVMCommand()) }
