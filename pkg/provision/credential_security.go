package provision

import (
	"fmt"
	"path/filepath"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	"github.com/csautter/dev-alchemy/pkg/credentials"
)

// GuestCredentialSecurity reports only the origin and classification of the existing
// provisioning credentials. It uses the same environment/.env fallbacks as provisioning.
// It neither creates credentials nor changes a guest's password.
func GuestCredentialSecurity(vm alchemy_build.VirtualMachineConfig) (origin string, private bool, err error) {
	var prefix string
	switch vm.VirtualizationEngine {
	case alchemy_build.VirtualizationEngineUtm:
		prefix = "UTM_"
	case alchemy_build.VirtualizationEngineHyperv:
		prefix = "HYPERV_"
	case alchemy_build.VirtualizationEngineQemu:
		prefix = "LIBVIRT_"
	case alchemy_build.VirtualizationEngineTart:
		prefix = "TART_"
	default:
		return "legacy", false, nil
	}
	var user, password string
	switch vm.OS {
	case "windows11":
		prefix += "WINDOWS_"
		user, password = defaultWindowsGuestUser, defaultWindowsGuestPassword
	case "ubuntu":
		prefix += "UBUNTU_"
		user, password = "packer", defaultUbuntuGuestPassword
	case "macos":
		prefix += "MACOS_"
		user, password = defaultTartGuestUser, defaultTartGuestPassword
	default:
		return "legacy", false, nil
	}
	values, err := parseDotEnvFile(filepath.Join(alchemy_build.GetDirectoriesInstance().ProjectDir, ".env"))
	if err != nil {
		return "", false, fmt.Errorf("cannot inspect guest credential configuration")
	}
	user = defaultIfEmpty(resolveEnvValue(prefix+"ANSIBLE_USER", values), user)
	origin = "legacy"
	if override := resolveEnvValue(prefix+"ANSIBLE_PASSWORD", values); override != "" {
		password = override
		origin = "user"
	}
	return origin, origin == "user" && !credentials.IsKnownInsecure(user, password), nil
}
