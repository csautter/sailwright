package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_deploy "github.com/csautter/dev-alchemy/pkg/deploy"
	alchemy_provision "github.com/csautter/dev-alchemy/pkg/provision"
	"github.com/csautter/dev-alchemy/pkg/security"
	"github.com/spf13/cobra"
)

var inspectGuestNetwork = alchemy_deploy.InspectNetworkMode
var inspectGuestCredentials = alchemy_provision.GuestCredentialSecurity
var readGuestSecurityOverride = security.AllowInsecureCredentials
var createAllowInsecureCredentials, startAllowInsecureCredentials, provisionAllowInsecureCredentials bool

func addGuestSecurityFlag(command *cobra.Command, value *bool) {
	command.Flags().BoolVar(value, "allow-insecure-credentials", false, "Allow non-private guest credentials on bridged or unknown networks")
}

func guestSecurityInfo(vm alchemy_build.VirtualMachineConfig, creating bool) (alchemy_deploy.NetworkMode, string, bool, error) {
	if vm.OS == "local" {
		return alchemy_deploy.NetworkMode{Mode: "unknown", Detail: "local host"}, "none", true, nil
	}
	network := inspectGuestNetwork(vm, creating)
	// Images still contain the documented defaults. Provisioning environment overrides
	// cannot change the credentials baked into a newly created guest.
	if creating {
		return network, "legacy", false, nil
	}
	origin, private, err := inspectGuestCredentials(vm)
	return network, origin, private, err
}

func warnGuestSecurity(writer io.Writer, vm alchemy_build.VirtualMachineConfig, network alchemy_deploy.NetworkMode, origin string, private bool) {
	target := createCommandArguments(vm)
	if network.Mode != "nat" && vm.OS != "local" {
		fmt.Fprintf(writer, "Warning: %s has %s networking (%s); the guest may be reachable from the LAN.\n", target, network.Mode, network.Detail)
	}
	if origin == "unknown" {
		fmt.Fprintf(writer, "Warning: %s guest credential security is unknown; check the engine-specific credential configuration and project .env file.\n", target)
	} else if !private {
		fmt.Fprintf(writer, "Warning: %s uses non-private guest credentials (origin: %s). Change the guest password and configure the engine-specific ANSIBLE_USER/ANSIBLE_PASSWORD environment variables before using a bridged network.\n", target, origin)
	}
}

func checkGuestSecurity(vm alchemy_build.VirtualMachineConfig, creating, allow bool, writer io.Writer) error {
	if vm.OS == "local" {
		return nil
	}
	network, origin, private, err := guestSecurityInfo(vm, creating)
	if err != nil {
		return err
	}
	warnGuestSecurity(writer, vm, network, origin, private)
	configPath := filepath.Join(alchemy_build.GetDirectoriesInstance().ConfigDir, "config.yaml")
	configAllow, err := readGuestSecurityOverride(configPath)
	if err != nil {
		return err
	}
	if network.Mode != "nat" && !private && !allow && !configAllow {
		return fmt.Errorf("refusing %s networking with non-private guest credentials for %s: use NAT, change the guest password and configure its credentials, or rebuild with private credentials; to accept the risk explicitly, use --allow-insecure-credentials or security.allow_insecure_credentials: true in %s", network.Mode, createCommandArguments(vm), configPath)
	}
	return nil
}

func guardGuestSecurity(vm alchemy_build.VirtualMachineConfig, creating, allow bool) error {
	return checkGuestSecurity(vm, creating, allow, os.Stderr)
}
