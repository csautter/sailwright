package deploy

import (
	"fmt"
	"os"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func SupportsCreate(config alchemy_build.VirtualMachineConfig) bool {
	switch config.VirtualizationEngine {
	case alchemy_build.VirtualizationEngineUtm,
		alchemy_build.VirtualizationEngineTart,
		alchemy_build.VirtualizationEngineHyperv:
		return true
	case alchemy_build.VirtualizationEngineQemu:
		return isLinuxLibvirtTarget(config)
	default:
		return false
	}
}

func CreateTargetExists(config alchemy_build.VirtualMachineConfig) (bool, error) {
	if err := alchemy_build.ValidateInstanceName(config.Name); err != nil {
		return false, err
	}
	switch {
	case isUtmDeployTarget(config):
		vmPath, err := utmVirtualMachinePath(config)
		if err != nil {
			return false, err
		}

		_, err = os.Stat(vmPath)
		if err == nil {
			return true, nil
		}
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, fmt.Errorf("failed to stat UTM VM bundle %q: %w", vmPath, err)
	case isTartMacOSDeployTarget(config):
		state, err := localTartVMState(alchemy_build.GetDirectoriesInstance().ProjectDir, tartMacOSVMName(config))
		if err != nil {
			return false, err
		}
		return state.exists, nil
	case isHypervVagrantTarget(config):
		projectDir := alchemy_build.GetDirectoriesInstance().ProjectDir
		settings, err := resolveHypervVagrantDeploySettings(config, projectDir)
		if err != nil {
			return false, err
		}

		exists, err := hypervVagrantMachineExistsChecker(settings.VagrantDir, settings.VagrantEnv)
		if err != nil {
			return false, err
		}

		return exists, nil
	case isLinuxLibvirtTarget(config):
		state, err := inspectLinuxLibvirtStartTarget(config)
		if err != nil {
			return false, err
		}
		return state.Exists, nil
	default:
		return false, fmt.Errorf(
			"create target inspection is not implemented for OS=%s type=%s arch=%s host=%s engine=%s",
			config.OS,
			config.UbuntuType,
			config.Arch,
			config.HostOs,
			config.VirtualizationEngine,
		)
	}
}
