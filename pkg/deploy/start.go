package deploy

import (
	"fmt"
	"strings"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

type VirtualMachineState struct {
	Exists  bool
	Running bool
	State   string
}

type StartTargetState = VirtualMachineState

func SupportsStart(config alchemy_build.VirtualMachineConfig) bool {
	switch {
	case isUtmDeployTarget(config):
		return true
	case isTartMacOSDeployTarget(config):
		return true
	case isHypervVagrantTarget(config):
		return true
	case isLinuxLibvirtTarget(config):
		return true
	default:
		return false
	}
}

func InspectStartTarget(config alchemy_build.VirtualMachineConfig) (VirtualMachineState, error) {
	if err := alchemy_build.ValidateInstanceName(config.Name); err != nil {
		return VirtualMachineState{}, err
	}
	switch {
	case isUtmDeployTarget(config):
		return inspectUtmStartTarget(config)
	case isTartMacOSDeployTarget(config):
		return inspectTartStartTarget(config)
	case isHypervVagrantTarget(config):
		return inspectHypervVagrantStartTarget(config)
	case isLinuxLibvirtTarget(config):
		return inspectLinuxLibvirtStartTarget(config)
	default:
		return VirtualMachineState{}, fmt.Errorf(
			"start target inspection is not implemented for OS=%s type=%s arch=%s host=%s engine=%s",
			config.OS,
			config.UbuntuType,
			config.Arch,
			config.HostOs,
			config.VirtualizationEngine,
		)
	}
}

func RunStart(config alchemy_build.VirtualMachineConfig) error {
	if err := alchemy_build.ValidateInstance(config); err != nil {
		return err
	}
	switch {
	case isUtmDeployTarget(config):
		return RunUtmStartOnMacOS(config)
	case isTartMacOSDeployTarget(config):
		return RunTartStartOnMacOS(config)
	case isHypervVagrantTarget(config):
		return RunHypervVagrantStartOnWindows(config)
	case isLinuxLibvirtTarget(config):
		return RunLinuxQemuStartOnLinux(config)
	default:
		return fmt.Errorf(
			"start is not implemented for OS=%s type=%s arch=%s host=%s engine=%s",
			config.OS,
			config.UbuntuType,
			config.Arch,
			config.HostOs,
			config.VirtualizationEngine,
		)
	}
}

func startCommandArguments(config alchemy_build.VirtualMachineConfig) string {
	args := []string{config.OS}
	if config.UbuntuType != "" {
		args = append(args, "--type", config.UbuntuType)
	}
	if config.Arch != "" {
		args = append(args, "--arch", config.Arch)
	}
	if config.Name != "" {
		args = append(args, "--name", config.Name)
	}
	return strings.Join(args, " ")
}
