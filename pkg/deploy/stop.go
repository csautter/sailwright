package deploy

import (
	"fmt"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func SupportsStop(config alchemy_build.VirtualMachineConfig) bool {
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

func InspectStopTarget(config alchemy_build.VirtualMachineConfig) (VirtualMachineState, error) {
	return InspectStartTarget(config)
}

func RunStop(config alchemy_build.VirtualMachineConfig) error {
	if err := alchemy_build.ValidateInstance(config); err != nil {
		return err
	}
	switch {
	case isUtmDeployTarget(config):
		return RunUtmStopOnMacOS(config)
	case isTartMacOSDeployTarget(config):
		return RunTartStopOnMacOS(config)
	case isHypervVagrantTarget(config):
		return RunHypervVagrantStopOnWindows(config)
	case isLinuxLibvirtTarget(config):
		return RunLinuxQemuStopOnLinux(config)
	default:
		return fmt.Errorf(
			"stop is not implemented for OS=%s type=%s arch=%s host=%s engine=%s",
			config.OS,
			config.UbuntuType,
			config.Arch,
			config.HostOs,
			config.VirtualizationEngine,
		)
	}
}
