package deploy

import (
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

const (
	utmAutomationCommandTimeout = time.Minute
	utmGracefulStopTimeout      = 45 * time.Second
	utmGracefulStopPollInterval = 2 * time.Second
)

func RunUtmDeployOnMacOS(config alchemy_build.VirtualMachineConfig) error {
	if !isUtmDeployTarget(config) {
		return fmt.Errorf("UTM deploy is not implemented for OS=%s type=%s arch=%s", config.OS, config.UbuntuType, config.Arch)
	}

	// Creating the bundle is an offline operation. Defer registration repair to
	// start, which preserves this bundle while removing any stale registration.
	// Querying UTM here would launch its GUI and require Automation consent even
	// when the caller only wants to prepare a disk (including CI deploy smoke tests).
	scriptPath := path.Join(alchemy_build.GetDirectoriesInstance().ProjectDir, "deployments/utm/create-utm-vm.sh")
	projectDir := alchemy_build.GetDirectoriesInstance().ProjectDir

	vmName := alchemy_build.GetVirtualMachineNameWithType(config)
	args := []string{"--arch", config.Arch, "--os", vmName}

	if err := runCommandWithStreamingLogs(
		projectDir,
		20*time.Minute,
		"bash",
		append([]string{scriptPath}, args...),
		fmt.Sprintf("%s:%s", vmName, config.Arch),
	); err != nil {
		return fmt.Errorf("UTM deploy failed for %s:%s: %w", vmName, config.Arch, err)
	}
	log.Printf("UTM deploy completed for %s:%s", vmName, config.Arch)
	return nil
}

func RunUtmDestroyOnMacOS(config alchemy_build.VirtualMachineConfig) error {
	if !isUtmDeployTarget(config) {
		return fmt.Errorf("UTM destroy is not implemented for OS=%s type=%s arch=%s", config.OS, config.UbuntuType, config.Arch)
	}

	vmPath, err := utmVirtualMachinePath(config)
	if err != nil {
		return err
	}

	// UTM must delete its registration before we remove any remaining bundle.
	// This also cleans registrations left behind by older Sailwright versions.
	if err := removeUtmRegistrations(config, ""); err != nil {
		return err
	}

	if err := os.RemoveAll(vmPath); err != nil {
		return fmt.Errorf("failed to remove UTM VM bundle %q: %w", vmPath, err)
	}

	log.Printf("UTM VM bundle removed: %s", vmPath)
	return nil
}

func RunUtmStartOnMacOS(config alchemy_build.VirtualMachineConfig) error {
	if !isUtmDeployTarget(config) {
		return fmt.Errorf("UTM start is not implemented for OS=%s type=%s arch=%s", config.OS, config.UbuntuType, config.Arch)
	}

	if err := ensureUtmRegistration(config); err != nil {
		return err
	}
	state, err := inspectUtmStartTarget(config)
	if err != nil {
		return err
	}
	if !state.Exists {
		return fmt.Errorf("UTM VM %q does not exist. Run `sailwright create %s` first", utmVirtualMachineName(config), startCommandArguments(config))
	}
	if state.Running {
		log.Printf("UTM VM %q is already %s", utmVirtualMachineName(config), state.State)
		return nil
	}

	if _, err := runUtmBundleAppleScript(config,
		[]string{
			`tell application "UTM"`,
			`set targetVM to virtual machine id "__SAILWRIGHT_UUID__"`,
			`start targetVM`,
			`end tell`,
		},
	); err != nil {
		return fmt.Errorf("failed to start UTM VM %q: %w", utmVirtualMachineName(config), err)
	}

	log.Printf("UTM VM %q start requested", utmVirtualMachineName(config))
	return nil
}

func RunUtmStopOnMacOS(config alchemy_build.VirtualMachineConfig) error {
	if !isUtmDeployTarget(config) {
		return fmt.Errorf("UTM stop is not implemented for OS=%s type=%s arch=%s", config.OS, config.UbuntuType, config.Arch)
	}

	state, err := inspectUtmStartTarget(config)
	if err != nil {
		return err
	}
	if !state.Exists {
		log.Printf("UTM VM %q is already absent", utmVirtualMachineName(config))
		return nil
	}
	if !state.Running {
		log.Printf("UTM VM %q is already %s", utmVirtualMachineName(config), state.State)
		return nil
	}

	if _, err := runUtmBundleAppleScript(config,
		[]string{
			`tell application "UTM"`,
			`set targetVM to virtual machine id "__SAILWRIGHT_UUID__"`,
			`stop targetVM by request`,
			`end tell`,
		},
	); err != nil {
		return fmt.Errorf("failed to request graceful shutdown for UTM VM %q: %w", utmVirtualMachineName(config), err)
	}

	if err := waitForUtmStop(config, utmGracefulStopTimeout); err == nil {
		log.Printf("UTM VM %q shut down cleanly", utmVirtualMachineName(config))
		return nil
	}

	log.Printf("UTM VM %q did not stop after graceful shutdown request; forcing stop", utmVirtualMachineName(config))

	if _, err := runUtmBundleAppleScript(config,
		[]string{
			`tell application "UTM"`,
			`set targetVM to virtual machine id "__SAILWRIGHT_UUID__"`,
			`stop targetVM by force`,
			`end tell`,
		},
	); err != nil {
		return fmt.Errorf("failed to force stop UTM VM %q after graceful shutdown attempt: %w", utmVirtualMachineName(config), err)
	}

	log.Printf("UTM VM %q force stop requested", utmVirtualMachineName(config))
	return nil
}

func inspectUtmStartTarget(config alchemy_build.VirtualMachineConfig) (StartTargetState, error) {
	vmPath, err := utmVirtualMachinePath(config)
	if err != nil {
		return StartTargetState{}, err
	}

	if _, err := os.Stat(vmPath); err != nil {
		if os.IsNotExist(err) {
			return StartTargetState{State: "missing"}, nil
		}
		return StartTargetState{}, fmt.Errorf("failed to stat UTM VM bundle %q: %w", vmPath, err)
	}

	id, _, err := inspectUtmInstanceID(config)
	if err != nil {
		return StartTargetState{}, err
	}
	ids, err := utmRegistrationIDs(config)
	if err != nil {
		return StartTargetState{}, err
	}
	if !slices.Contains(ids, id) {
		return StartTargetState{Exists: true, State: "unregistered"}, nil
	}
	status, err := utmVirtualMachineStatus(config)
	if err != nil {
		return StartTargetState{}, err
	}

	return StartTargetState{
		Exists:  true,
		Running: utmStatusIndicatesRunning(status),
		State:   status,
	}, nil
}

func isUtmDeployTarget(vm alchemy_build.VirtualMachineConfig) bool {
	_, ok := resolveUtmDeployTarget(vm)
	return ok
}

func resolveUtmDeployTarget(vm alchemy_build.VirtualMachineConfig) (alchemy_build.VirtualMachineConfig, bool) {
	for _, candidate := range alchemy_build.AvailableVirtualMachineConfigs() {
		if candidate.HostOs != alchemy_build.HostOsDarwin {
			continue
		}
		if candidate.VirtualizationEngine != alchemy_build.VirtualizationEngineUtm {
			continue
		}
		if candidate.OS != vm.OS || candidate.UbuntuType != vm.UbuntuType || candidate.Arch != vm.Arch {
			continue
		}
		if vm.HostOs != "" && vm.HostOs != candidate.HostOs {
			continue
		}
		if vm.VirtualizationEngine != "" && vm.VirtualizationEngine != candidate.VirtualizationEngine {
			continue
		}
		return candidate, true
	}

	return alchemy_build.VirtualMachineConfig{}, false
}

func utmVirtualMachineName(config alchemy_build.VirtualMachineConfig) string {
	return fmt.Sprintf("%s-%s-dev-alchemy", alchemy_build.GetVirtualMachineNameWithType(config), config.Arch)
}

func utmVirtualMachinePath(config alchemy_build.VirtualMachineConfig) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home directory for UTM VM path: %w", err)
	}

	return filepath.Join(
		homeDir,
		"Library",
		"Containers",
		"com.utmapp.UTM",
		"Data",
		"Documents",
		utmVirtualMachineName(config)+".utm",
	), nil
}

func utmVirtualMachineStatus(config alchemy_build.VirtualMachineConfig) (string, error) {
	output, err := runUtmBundleAppleScript(config,
		[]string{
			`tell application "UTM"`,
			`set targetVM to virtual machine id "__SAILWRIGHT_UUID__"`,
			`return (status of targetVM) as text`,
			`end tell`,
		},
	)
	if err != nil {
		return "", fmt.Errorf("failed to query status of UTM VM %q: %w", utmVirtualMachineName(config), err)
	}

	return strings.ToLower(strings.TrimSpace(output)), nil
}

func utmStatusIndicatesRunning(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "started", "starting", "resuming":
		return true
	default:
		return false
	}
}

func waitForUtmStop(config alchemy_build.VirtualMachineConfig, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastState string

	for time.Now().Before(deadline) {
		status, err := utmVirtualMachineStatus(config)
		if err != nil {
			return err
		}
		lastState = status
		if !utmStatusIndicatesRunning(status) {
			return nil
		}

		time.Sleep(utmGracefulStopPollInterval)
	}

	return fmt.Errorf("UTM VM %q remained running after %s (last state=%s)", utmVirtualMachineName(config), timeout, lastState)
}

func runUtmAppleScript(lines []string) (string, error) {
	args := make([]string, 0, len(lines)*2)
	for _, line := range lines {
		args = append(args, "-e", line)
	}

	output, err := runCommandWithCombinedOutput(
		alchemy_build.GetDirectoriesInstance().ProjectDir,
		utmAutomationCommandTimeout,
		"osascript",
		args,
	)
	if err != nil {
		return "", fmt.Errorf("%w; output: %s", err, strings.TrimSpace(output))
	}

	return output, nil
}
