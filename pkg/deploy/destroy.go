package deploy

import (
	"fmt"
	"os"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func SupportsDestroy(config alchemy_build.VirtualMachineConfig) bool {
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

func RunDestroy(config alchemy_build.VirtualMachineConfig) error {
	if err := alchemy_build.ValidateInstance(config); err != nil {
		return err
	}
	switch {
	case isUtmDeployTarget(config):
		return RunUtmDestroyOnMacOS(config)
	case isTartMacOSDeployTarget(config):
		return RunTartDestroyOnMacOS(config)
	case isHypervVagrantTarget(config):
		return RunHypervVagrantDestroyOnWindows(config)
	case isLinuxLibvirtTarget(config):
		return RunLinuxQemuDestroyOnLinux(config)
	default:
		return fmt.Errorf(
			"destroy is not implemented for OS=%s type=%s arch=%s host=%s engine=%s",
			config.OS,
			config.UbuntuType,
			config.Arch,
			config.HostOs,
			config.VirtualizationEngine,
		)
	}
}

func DestroyTargetExists(config alchemy_build.VirtualMachineConfig) (bool, error) {
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
			ids, err := utmRegistrationIDs(config)
			return len(ids) > 0, err
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

		machineExists, err := hypervVagrantMachineExistsChecker(settings.VagrantDir, settings.VagrantEnv)
		if err != nil {
			return false, err
		}

		boxInstalled, err := hypervVagrantBoxInstalledChecker(projectDir, settings.BoxName)
		if err != nil {
			return false, err
		}

		return machineExists || boxInstalled, nil
	case isLinuxLibvirtTarget(config):
		state, err := inspectLinuxLibvirtStartTarget(config)
		if err != nil {
			return false, err
		}
		if state.Exists {
			return true, nil
		}

		_, err = os.Stat(linuxLibvirtDiskPath(config))
		if err == nil {
			return true, nil
		}
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, fmt.Errorf("failed to stat libvirt disk %q: %w", linuxLibvirtDiskPath(config), err)
	default:
		return false, fmt.Errorf(
			"destroy target inspection is not implemented for OS=%s type=%s arch=%s host=%s engine=%s",
			config.OS,
			config.UbuntuType,
			config.Arch,
			config.HostOs,
			config.VirtualizationEngine,
		)
	}
}
