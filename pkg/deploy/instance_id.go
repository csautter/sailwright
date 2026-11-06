package deploy

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

// instanceIDPattern accepts the identifiers hypervisors report (UUIDs/GUIDs).
var instanceIDPattern = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

const instanceIDQueryTimeout = 30 * time.Second

// InspectInstanceID returns the hypervisor's identifier of the VM for config, which
// changes when the VM is destroyed and created again under the same name. It works
// for stopped VMs. exists is false when no VM exists. The identifier is empty for
// engines without a stable identifier sailwright reads (UTM and Tart).
func InspectInstanceID(config alchemy_build.VirtualMachineConfig) (id string, exists bool, err error) {
	switch {
	case isLinuxLibvirtTarget(config):
		return inspectLibvirtInstanceID(config)
	case isHypervVagrantTarget(config):
		return inspectHypervInstanceID(config)
	default:
		state, err := InspectStartTarget(config)
		if err != nil {
			return "", false, err
		}
		return "", state.Exists, nil
	}
}

func inspectLibvirtInstanceID(config alchemy_build.VirtualMachineConfig) (string, bool, error) {
	if err := ensureLinuxLibvirtCommandsAvailable("virsh"); err != nil {
		return "", false, err
	}
	domain := linuxLibvirtDomainName(config)
	output, err := runLinuxLibvirtCommandWithCombinedOut(
		alchemy_build.GetDirectoriesInstance().ProjectDir,
		instanceIDQueryTimeout,
		"virsh",
		[]string{"--connect", linuxLibvirtURI(), "domuuid", domain},
	)
	if err != nil {
		if linuxLibvirtOutputIndicatesMissingDomain(output) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to read libvirt VM %q UUID: %w; output: %s", domain, err, strings.TrimSpace(output))
	}
	return parseInstanceID(output)
}

func inspectHypervInstanceID(config alchemy_build.VirtualMachineConfig) (string, bool, error) {
	settings, err := resolveHypervVagrantDeploySettings(config, alchemy_build.GetDirectoriesInstance().ProjectDir)
	if err != nil {
		return "", false, err
	}
	vmName, err := hypervVagrantVMName(settings.VagrantEnv)
	if err != nil {
		return "", false, err
	}
	output, err := runHypervCommandWithCombinedOutput(
		settings.VagrantDir,
		instanceIDQueryTimeout,
		"powershell",
		[]string{
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			fmt.Sprintf(
				"$vm = Get-VM -Name %s -ErrorAction SilentlyContinue; if ($null -eq $vm) { Write-Output missing } else { Write-Output $vm.Id.Guid }",
				powershellSingleQuote(vmName),
			),
		},
	)
	if err != nil {
		return "", false, fmt.Errorf("failed to read Hyper-V VM %q id: %w; output: %s", vmName, err, strings.TrimSpace(output))
	}
	return parseInstanceID(output)
}

// parseInstanceID reads the last non-empty output line: "missing" or an identifier.
func parseInstanceID(output string) (string, bool, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if strings.EqualFold(last, "missing") {
		return "", false, nil
	}
	if !instanceIDPattern.MatchString(last) {
		return "", false, fmt.Errorf("unexpected VM identifier %q", truncateForError(last, 64))
	}
	return strings.ToLower(last), true, nil
}

func truncateForError(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
