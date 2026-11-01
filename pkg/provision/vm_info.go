package provision

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	alchemy_deploy "github.com/csautter/dev-alchemy/pkg/deploy"
)

// VMInfo is the versioned connection description for a running managed VM.
type VMInfo struct {
	SchemaVersion int    `json:"schema_version"`
	OS            string `json:"os"`
	Type          string `json:"type"`
	Arch          string `json:"arch"`
	Engine        string `json:"engine"`
	State         string `json:"state"`
	Address       string `json:"address"`
	NetworkMode   string `json:"network_mode"`
	Protocol      string `json:"protocol"`
	Port          int    `json:"port"`
	Username      string `json:"username"`
	Password      string `json:"password"`
}

// DiscoverLibvirtAddress uses the same lease/agent discovery as provisioning.
func DiscoverLibvirtAddress(project string, vm alchemy_build.VirtualMachineConfig) (string, error) {
	return discoverLinuxLibvirtVMIPv4(project, vm)
}

// DiscoverUTMAddress finds the managed guest by its configured MAC address.
func DiscoverUTMAddress(project string, vm alchemy_build.VirtualMachineConfig) (string, error) {
	return discoverUtmVMIPv4(project, vm)
}

// DiscoverTartAddress queries Tart without starting a stopped guest.
func DiscoverTartAddress(project string, vm alchemy_build.VirtualMachineConfig) (string, error) {
	return ensureTartVMReadyForProvision(project, tartMacOSVMName(vm), tartProvisionAvailabilityOptions{})
}

// DiscoverHyperVAddress queries the hypervisor first, so discovery also works
// when Vagrant state belongs to a different data directory.
func DiscoverHyperVAddress(vm alchemy_build.VirtualMachineConfig) (string, error) {
	settings, err := alchemy_deploy.ResolveHypervVagrantExecutionSettings(vm)
	if err != nil {
		return "", err
	}
	name := ""
	for _, value := range settings.VagrantEnv {
		if strings.HasPrefix(value, "VAGRANT_VM_NAME=") {
			name = strings.TrimPrefix(value, "VAGRANT_VM_NAME=")
		}
	}
	if name == "" {
		return "", fmt.Errorf("managed Hyper-V VM name unavailable")
	}
	script := "Get-VMNetworkAdapter -VMName '" + strings.ReplaceAll(name, "'", "''") + "' -ErrorAction Stop | ForEach-Object { $_.IPAddresses }"
	output, queryErr := runProvisionCommandWithCombinedOutputWithEnv(alchemy_build.GetDirectoriesInstance().ProjectDir, 30*time.Second, "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-Command", script}, nil)
	if queryErr == nil {
		for _, field := range strings.Fields(output) {
			ip, err := netip.ParseAddr(field)
			if err == nil && ip.Is4() && ip.IsGlobalUnicast() && !ip.IsLinkLocalUnicast() {
				return ip.String(), nil
			}
		}
	}
	if vm.OS == "ubuntu" {
		return discoverLinuxVagrantIPv4(settings.VagrantDir, settings.VagrantEnv)
	}
	return discoverWindowsVagrantIPv4(settings.VagrantDir, settings.VagrantEnv)
}

// InspectVM returns guest connection details without writing them to logs.
func InspectVM(vm alchemy_build.VirtualMachineConfig) (VMInfo, error) {
	if err := ensureProvisionTargetRunning(vm); err != nil {
		return VMInfo{}, err
	}
	project := alchemy_build.GetDirectoriesInstance().ProjectDir
	info := VMInfo{SchemaVersion: 1, OS: vm.OS, Type: vm.UbuntuType, Arch: vm.Arch, Engine: string(vm.VirtualizationEngine), State: "running", NetworkMode: "unknown"}
	var err error
	switch info.Engine {
	case "hyperv":
		info.Address, err = DiscoverHyperVAddress(vm)
	case "utm":
		info.Address, err = DiscoverUTMAddress(project, vm)
	case "tart":
		info.Address, err = DiscoverTartAddress(project, vm)
	case "qemu":
		info.Address, err = DiscoverLibvirtAddress(project, vm)
	default:
		return info, fmt.Errorf("VM info is unavailable for engine %s", info.Engine)
	}
	if err != nil {
		return VMInfo{}, err
	}
	if vm.OS == "windows11" {
		var connection windowsAnsibleConnectionConfig
		switch info.Engine {
		case "hyperv":
			connection, err = loadWindowsHypervAnsibleConnectionConfig(project)
		case "utm":
			connection, err = loadWindowsUtmAnsibleConnectionConfig(project)
		default:
			connection, err = loadWindowsLibvirtAnsibleConnectionConfig(project)
		}
		info.Protocol, info.Username, info.Password = connection.Connection, connection.User, connection.Password
		if err == nil {
			info.Port, err = parseGuestPort(connection.Port, 0)
		}
	} else {
		var connection sshAnsibleConnectionConfig
		switch info.Engine {
		case "hyperv":
			connection, err = loadUbuntuHypervAnsibleConnectionConfig(project)
		case "utm":
			connection, err = loadUbuntuUtmAnsibleConnectionConfig(project)
		case "tart":
			connection, err = loadMacOSTartAnsibleConnectionConfig(project)
		default:
			connection, err = loadUbuntuLibvirtAnsibleConnectionConfig(project)
		}
		info.Protocol, info.Username, info.Password = "ssh", connection.User, connection.Password
		if err == nil {
			info.Port, err = parseGuestPort(connection.Port, 22)
		}
	}
	if err != nil {
		return VMInfo{}, err
	}
	return info, nil
}

// parseGuestPort parses an inventory port; an empty value uses fallback when it is set.
func parseGuestPort(value string, fallback int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" && fallback != 0 {
		return fallback, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid guest connection port %q", value)
	}
	return port, nil
}
