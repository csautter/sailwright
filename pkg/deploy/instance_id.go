package deploy

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

// instanceIDPattern accepts the identifiers hypervisors report (UUIDs/GUIDs).
var instanceIDPattern = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

var tartFilesystemIDPattern = regexp.MustCompile(`^[0-9]+:[0-9]+:[1-9][0-9]*$`)

const instanceIDQueryTimeout = 30 * time.Second

// InspectInstanceID returns the hypervisor's identifier of the VM for config, which
// changes when the VM is destroyed and created again under the same name. It works
// for stopped VMs. exists is false when no VM exists. UTM uses its bundle UUID;
// Tart uses the local VM directory identity because clones can share hardware IDs.
func InspectInstanceID(config alchemy_build.VirtualMachineConfig) (id string, exists bool, err error) {
	switch {
	case isUtmDeployTarget(config):
		return inspectUtmInstanceID(config)
	case isTartMacOSDeployTarget(config):
		return inspectTartInstanceID(config)
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

func inspectUtmInstanceID(config alchemy_build.VirtualMachineConfig) (string, bool, error) {
	vmPath, err := utmVirtualMachinePath(config)
	if err != nil {
		return "", false, err
	}
	configPath := filepath.Join(vmPath, "config.plist")
	if _, err := os.Stat(vmPath); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	output, err := runCommandWithCombinedOutput(alchemy_build.GetDirectoriesInstance().ProjectDir,
		instanceIDQueryTimeout, "plutil", []string{"-extract", "Information.UUID", "raw", "-o", "-", configPath})
	if err != nil {
		return "", false, fmt.Errorf("read UTM bundle UUID: %w; output: %s", err, strings.TrimSpace(output))
	}
	// Unlike a hypervisor query, a present bundle must always contain a UUID.
	if !instanceIDPattern.MatchString(strings.TrimSpace(output)) {
		return "", false, fmt.Errorf("invalid UTM bundle UUID %q", truncateForError(output, 64))
	}
	return strings.ToLower(strings.TrimSpace(output)), true, nil
}

func inspectTartInstanceID(config alchemy_build.VirtualMachineConfig) (string, bool, error) {
	tartHome := os.Getenv("TART_HOME")
	if tartHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false, err
		}
		tartHome = filepath.Join(home, ".tart")
	}
	name := tartMacOSVMName(config)
	if name == "." || name == ".." || filepath.Base(name) != name {
		return "", false, fmt.Errorf("invalid local Tart VM name %q", name)
	}
	vmPath := filepath.Join(tartHome, "vms", name)
	info, err := os.Stat(vmPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("Tart VM path %q is not a directory", vmPath)
	}
	// macOS stat: device, inode, birth time. Config/ECID and MAC addresses can
	// survive cloning, so they cannot identify a local Tart instance. Directory
	// birth time also protects against inode reuse after destroy/recreate.
	output, err := runCommandWithCombinedOutput(alchemy_build.GetDirectoriesInstance().ProjectDir,
		instanceIDQueryTimeout, "stat", []string{"-L", "-f", "%d:%i:%B", vmPath})
	if err != nil {
		return "", false, fmt.Errorf("read Tart instance identity: %w", err)
	}
	value := strings.TrimSpace(output)
	if !tartFilesystemIDPattern.MatchString(value) {
		return "", false, fmt.Errorf("invalid Tart filesystem identity %q", truncateForError(value, 64))
	}
	return fmt.Sprintf("tart:%x", sha256.Sum256([]byte(value))), true, nil
}
