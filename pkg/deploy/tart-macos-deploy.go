package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

const (
	tartMacOSImageEnvVar                = "TART_MACOS_IMAGE"
	tartMacOSVMNameEnvVar               = "TART_MACOS_VM_NAME"
	tartMacOSBridgedInterfaceEnvVar     = "TART_MACOS_BRIDGED_INTERFACE"
	tartMacOSDefaultImageReference      = "ghcr.io/cirruslabs/macos-tahoe-base:latest"
	tartMacOSDefaultVMName              = "tahoe-base-alchemy"
	tartMacOSCloneTimeout               = 45 * time.Minute
	tartMacOSStartTimeout               = time.Minute
	tartMacOSIPv4DiscoveryRetryWindow   = 5 * time.Minute
	tartMacOSIPv4DiscoveryRetryInterval = 2 * time.Second
	tartMacOSCommandTimeout             = time.Minute
)

type tartIPv4DiscoveryOptions struct {
	runCommand     func(string, time.Duration, string, []string) (string, error)
	sleep          func(time.Duration)
	retryInterval  time.Duration
	maxAttempts    int
	commandTimeout time.Duration
}

type tartDetachedRun struct {
	logDir      string
	logFileName string
	pid         int
}

type tartLocalVMState struct {
	exists  bool
	running bool
}

type tartListJSONEntry struct {
	Source  string `json:"Source"`
	Name    string `json:"Name"`
	Running bool   `json:"Running"`
	State   string `json:"State"`
}

type tartReachabilityWaitOptions struct {
	detectIPv4       func() (string, error)
	isProcessRunning func(int) (bool, error)
	readLogSummary   func(string, string) string
	sleep            func(time.Duration)
	retryInterval    time.Duration
	maxAttempts      int
}

func RunTartDeployOnMacOS(config alchemy_build.VirtualMachineConfig) error {
	if !isTartMacOSDeployTarget(config) {
		return fmt.Errorf("Tart deploy is not implemented for OS=%s type=%s arch=%s", config.OS, config.UbuntuType, config.Arch)
	}

	projectDir := alchemy_build.GetDirectoriesInstance().ProjectDir
	vmName := tartMacOSVMName(config)

	vmState, err := localTartVMState(projectDir, vmName)
	if err != nil {
		return err
	}

	if !vmState.exists {
		if err := ensureLocalTartVM(projectDir, vmName); err != nil {
			return err
		}
	} else {
		log.Printf("Tart VM %q already exists", vmName)
	}

	if ip, err := discoverTartVMIPv4WithOptions(projectDir, vmName, tartIPv4DiscoveryOptions{maxAttempts: 1}); err == nil && ip != "" {
		if vmState.exists {
			log.Printf("Tart VM %q is already reachable at %s", vmName, ip)
		}
		return nil
	}

	if vmState.running {
		ip, err := discoverTartVMIPv4(projectDir, vmName)
		if err != nil {
			return fmt.Errorf("Tart VM %q is already running but no IPv4 address became available: %w", vmName, err)
		}
		log.Printf("Tart VM %q is already running and reachable at %s", vmName, ip)
		return nil
	}

	runState, err := startTartVMDetached(projectDir, vmName)
	if err != nil {
		return err
	}

	ip, err := waitForTartVMToBecomeReachable(projectDir, vmName, runState)
	if err != nil {
		return err
	}
	log.Printf("Tart VM %q is reachable at %s", vmName, ip)

	return nil
}

func RunTartDestroyOnMacOS(config alchemy_build.VirtualMachineConfig) error {
	if !isTartMacOSDeployTarget(config) {
		return fmt.Errorf("Tart destroy is not implemented for OS=%s type=%s arch=%s", config.OS, config.UbuntuType, config.Arch)
	}

	projectDir := alchemy_build.GetDirectoriesInstance().ProjectDir
	vmName := tartMacOSVMName(config)

	vmState, err := localTartVMState(projectDir, vmName)
	if err != nil {
		return err
	}
	if !vmState.exists {
		log.Printf("Tart VM %q is already absent", vmName)
		return nil
	}

	if vmState.running {
		if err := runTartCommandAllowingMissingVM(
			projectDir,
			tartMacOSCommandTimeout,
			"stop",
			vmName,
			false,
		); err != nil {
			return fmt.Errorf("failed to stop Tart VM %q: %w", vmName, err)
		}
	}

	if err := runTartCommandAllowingMissingVM(
		projectDir,
		tartMacOSCommandTimeout,
		"delete",
		vmName,
		true,
	); err != nil {
		return fmt.Errorf("failed to delete Tart VM %q: %w", vmName, err)
	}

	log.Printf("Tart VM %q deleted", vmName)
	return nil
}

func RunTartStartOnMacOS(config alchemy_build.VirtualMachineConfig) error {
	if !isTartMacOSDeployTarget(config) {
		return fmt.Errorf("Tart start is not implemented for OS=%s type=%s arch=%s", config.OS, config.UbuntuType, config.Arch)
	}

	projectDir := alchemy_build.GetDirectoriesInstance().ProjectDir
	vmName := tartMacOSVMName(config)

	vmState, err := localTartVMState(projectDir, vmName)
	if err != nil {
		return err
	}
	if !vmState.exists {
		return fmt.Errorf("Tart VM %q does not exist. Run `sailwright create %s` first", vmName, startCommandArguments(config))
	}
	if vmState.running {
		log.Printf("Tart VM %q is already running", vmName)
		return nil
	}

	runState, err := startTartVMDetached(projectDir, vmName)
	if err != nil {
		return err
	}

	ip, err := waitForTartVMToBecomeReachable(projectDir, vmName, runState)
	if err != nil {
		return err
	}
	log.Printf("Tart VM %q is reachable at %s", vmName, ip)
	return nil
}

func RunTartStopOnMacOS(config alchemy_build.VirtualMachineConfig) error {
	if !isTartMacOSDeployTarget(config) {
		return fmt.Errorf("Tart stop is not implemented for OS=%s type=%s arch=%s", config.OS, config.UbuntuType, config.Arch)
	}

	projectDir := alchemy_build.GetDirectoriesInstance().ProjectDir
	vmName := tartMacOSVMName(config)

	vmState, err := localTartVMState(projectDir, vmName)
	if err != nil {
		return err
	}
	if !vmState.exists {
		log.Printf("Tart VM %q is already absent", vmName)
		return nil
	}
	if !vmState.running {
		log.Printf("Tart VM %q is already stopped", vmName)
		return nil
	}

	if err := runTartCommandAllowingMissingVM(
		projectDir,
		tartMacOSCommandTimeout,
		"stop",
		vmName,
		false,
	); err != nil {
		return fmt.Errorf("failed to stop Tart VM %q: %w", vmName, err)
	}

	log.Printf("Tart VM %q stopped", vmName)
	return nil
}

func inspectTartStartTarget(config alchemy_build.VirtualMachineConfig) (StartTargetState, error) {
	if !isTartMacOSDeployTarget(config) {
		return StartTargetState{}, fmt.Errorf("Tart start target inspection is not implemented for OS=%s type=%s arch=%s", config.OS, config.UbuntuType, config.Arch)
	}

	state, err := localTartVMState(alchemy_build.GetDirectoriesInstance().ProjectDir, tartMacOSVMName(config))
	if err != nil {
		return StartTargetState{}, err
	}

	if !state.exists {
		return StartTargetState{State: "missing"}, nil
	}
	if state.running {
		return StartTargetState{Exists: true, Running: true, State: "running"}, nil
	}

	return StartTargetState{Exists: true, State: "stopped"}, nil
}

func isTartMacOSDeployTarget(vm alchemy_build.VirtualMachineConfig) bool {
	return vm.OS == "macos" &&
		vm.Arch == "arm64" &&
		vm.HostOs == alchemy_build.HostOsDarwin &&
		vm.VirtualizationEngine == alchemy_build.VirtualizationEngineTart
}

func resolveTartMacOSImageReference() string {
	return defaultIfEmpty(strings.TrimSpace(os.Getenv(tartMacOSImageEnvVar)), tartMacOSDefaultImageReference)
}

func tartMacOSVMName(_ alchemy_build.VirtualMachineConfig) string {
	return defaultIfEmpty(strings.TrimSpace(os.Getenv(tartMacOSVMNameEnvVar)), tartMacOSDefaultVMName)
}

func ensureLocalTartVM(projectDir string, vmName string) error {
	exists, err := localTartVMExists(projectDir, vmName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	imageReference := resolveTartMacOSImageReference()
	if err := runCommandWithStreamingLogs(
		projectDir,
		tartMacOSCloneTimeout,
		"tart",
		[]string{"clone", imageReference, vmName},
		fmt.Sprintf("%s:clone", vmName),
	); err != nil {
		return fmt.Errorf("failed to clone Tart image %q into %q: %w", imageReference, vmName, err)
	}

	return nil
}

func localTartVMExists(projectDir string, vmName string) (bool, error) {
	state, err := localTartVMState(projectDir, vmName)
	if err != nil {
		return false, err
	}

	return state.exists, nil
}

func localTartVMState(projectDir string, vmName string) (tartLocalVMState, error) {
	output, err := runCommandWithCombinedOutput(projectDir, tartMacOSCommandTimeout, "tart", []string{"list", "--format", "json"})
	if err == nil {
		if state, ok := tartListLocalVMStateFromJSON(output, vmName); ok {
			return state, nil
		}
	}

	output, err = runCommandWithCombinedOutput(projectDir, tartMacOSCommandTimeout, "tart", []string{"list"})
	if err != nil {
		return tartLocalVMState{}, fmt.Errorf("failed to list Tart VMs: %w; output: %s", err, strings.TrimSpace(output))
	}

	return tartListLocalVMState(output, vmName), nil
}

func runTartCommandAllowingMissingVM(projectDir string, timeout time.Duration, subcommand string, vmName string, allowMissing bool) error {
	output, err := runCommandWithCombinedOutput(projectDir, timeout, "tart", []string{subcommand, vmName})
	if err == nil {
		return nil
	}

	trimmedOutput := strings.TrimSpace(output)
	switch {
	case allowMissing && strings.Contains(trimmedOutput, "does not exist"):
		log.Printf("Tart VM %q is already absent during %s", vmName, subcommand)
		return nil
	case !allowMissing && strings.Contains(trimmedOutput, "is not running"):
		log.Printf("Tart VM %q is already stopped", vmName)
		return nil
	default:
		return fmt.Errorf("%w; output: %s", err, trimmedOutput)
	}
}

func tartListIncludesLocalVM(output string, vmName string) bool {
	return tartListLocalVMState(output, vmName).exists
}

func tartListLocalVMStateFromJSON(output string, vmName string) (tartLocalVMState, bool) {
	var entries []tartListJSONEntry
	if err := json.Unmarshal([]byte(output), &entries); err != nil {
		return tartLocalVMState{}, false
	}

	for _, entry := range entries {
		if !strings.EqualFold(entry.Source, "local") || entry.Name != vmName {
			continue
		}

		return tartLocalVMState{
			exists:  true,
			running: entry.Running || strings.EqualFold(entry.State, "running"),
		}, true
	}

	return tartLocalVMState{}, true
}

func tartListLocalVMState(output string, vmName string) tartLocalVMState {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return tartLocalVMState{}
	}

	if nameColumn, statusColumn, runningColumn, sourceColumn, ok := tartListColumnIndexes(lines); ok {
		for _, line := range lines[1:] {
			fields := strings.Fields(line)
			if len(fields) <= nameColumn || len(fields) <= sourceColumn {
				continue
			}
			if fields[sourceColumn] == "local" && fields[nameColumn] == vmName {
				isRunning := statusColumn >= 0 && len(fields) > statusColumn && strings.EqualFold(fields[statusColumn], "running")
				if !isRunning && runningColumn >= 0 && len(fields) > runningColumn {
					isRunning = strings.EqualFold(fields[runningColumn], "true") || strings.EqualFold(fields[runningColumn], "running")
				}

				return tartLocalVMState{
					exists:  true,
					running: isRunning,
				}
			}
		}

		return tartLocalVMState{}
	}

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[0] == "local" && fields[1] == vmName {
			return tartLocalVMState{
				exists:  true,
				running: len(fields) > 2 && strings.EqualFold(fields[2], "running"),
			}
		}
	}

	return tartLocalVMState{}
}

func tartListColumnIndexes(lines []string) (int, int, int, int, bool) {
	for _, line := range lines {
		header := strings.Fields(strings.ToLower(strings.TrimSpace(line)))
		if len(header) == 0 {
			continue
		}

		nameColumn := -1
		statusColumn := -1
		runningColumn := -1
		sourceColumn := -1
		for index, field := range header {
			switch field {
			case "name":
				nameColumn = index
			case "status":
				statusColumn = index
			case "running", "state":
				runningColumn = index
			case "source":
				sourceColumn = index
			}
		}

		if nameColumn >= 0 && sourceColumn >= 0 {
			return nameColumn, statusColumn, runningColumn, sourceColumn, true
		}

		break
	}

	return 0, 0, 0, 0, false
}

func startTartVMDetached(projectDir string, vmName string) (tartDetachedRun, error) {
	logDir := filepath.Join(projectDir, ".dev-alchemy", "tart")
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return tartDetachedRun{}, fmt.Errorf("failed to create Tart log directory %q: %w", logDir, err)
	}

	logFileName := tartRunLogFileName(vmName)
	logPath := filepath.Join(logDir, logFileName)
	commandParts := []string{"tart", "run"}
	if bridgedInterface := strings.TrimSpace(os.Getenv(tartMacOSBridgedInterfaceEnvVar)); bridgedInterface != "" {
		commandParts = append(commandParts, fmt.Sprintf("--net-bridged=%s", bridgedInterface))
	}
	commandParts = append(commandParts, vmName)

	quotedArgs := make([]string, 0, len(commandParts)-1)
	for _, arg := range commandParts[1:] {
		quotedArgs = append(quotedArgs, bashSingleQuote(arg))
	}

	shellCommand := "nohup " + commandParts[0] + " " + strings.Join(quotedArgs, " ") + " >" + bashSingleQuote(logPath) + " 2>&1 </dev/null & echo $!"
	output, err := runCommandWithCombinedOutput(projectDir, tartMacOSStartTimeout, "bash", []string{"-lc", shellCommand})
	if err != nil {
		return tartDetachedRun{}, fmt.Errorf("failed to start Tart VM %q: %w; output: %s", vmName, err, strings.TrimSpace(output))
	}

	pid, err := parseBackgroundPID(output)
	if err != nil {
		return tartDetachedRun{}, fmt.Errorf("failed to determine Tart background PID for %q: %w; output: %s", vmName, err, strings.TrimSpace(output))
	}

	return tartDetachedRun{
		logDir:      logDir,
		logFileName: logFileName,
		pid:         pid,
	}, nil
}

func tartRunLogFileName(vmName string) string {
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, strings.TrimSpace(vmName))
	sanitized = strings.Trim(sanitized, "._")
	if sanitized == "" {
		sanitized = "tart-vm"
	}

	return sanitized + ".log"
}

func discoverTartVMIPv4(projectDir string, vmName string) (string, error) {
	return discoverTartVMIPv4WithOptions(projectDir, vmName, tartIPv4DiscoveryOptions{})
}

func discoverTartVMIPv4WithOptions(projectDir string, vmName string, options tartIPv4DiscoveryOptions) (string, error) {
	options = withDefaultTartIPv4DiscoveryOptions(options)

	var lastErr error
	for attempt := 1; attempt <= options.maxAttempts; attempt++ {
		ip, err := detectTartVMIPv4(projectDir, vmName, options.runCommand, options.commandTimeout)
		if err == nil {
			return ip, nil
		}
		lastErr = err

		if attempt < options.maxAttempts {
			options.sleep(options.retryInterval)
		}
	}

	return "", fmt.Errorf(
		"could not determine IPv4 address for Tart VM %q after %d attempts over %s: %w",
		vmName,
		options.maxAttempts,
		time.Duration(options.maxAttempts-1)*options.retryInterval,
		lastErr,
	)
}

func withDefaultTartIPv4DiscoveryOptions(options tartIPv4DiscoveryOptions) tartIPv4DiscoveryOptions {
	if options.runCommand == nil {
		options.runCommand = runCommandWithCombinedOutput
	}
	if options.sleep == nil {
		options.sleep = time.Sleep
	}
	if options.retryInterval <= 0 {
		options.retryInterval = tartMacOSIPv4DiscoveryRetryInterval
	}
	if options.maxAttempts <= 0 {
		options.maxAttempts = int(tartMacOSIPv4DiscoveryRetryWindow/options.retryInterval) + 1
	}
	if options.commandTimeout <= 0 {
		options.commandTimeout = tartMacOSCommandTimeout
	}

	return options
}

func detectTartVMIPv4(
	projectDir string,
	vmName string,
	runCommand func(string, time.Duration, string, []string) (string, error),
	commandTimeout time.Duration,
) (string, error) {
	type ipCommand struct {
		args  []string
		label string
	}

	commands := []ipCommand{
		{args: []string{"ip", vmName}, label: "default resolver"},
		{args: []string{"ip", "--resolver=arp", vmName}, label: "arp resolver"},
	}

	var failures []string
	for _, command := range commands {
		output, err := runCommand(projectDir, commandTimeout, "tart", command.args)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s failed: %v; output: %s", command.label, err, strings.TrimSpace(output)))
			continue
		}

		ip, parseErr := extractLinuxIPv4FromHostOutput(output)
		if parseErr == nil {
			return ip, nil
		}

		failures = append(failures, fmt.Sprintf("%s returned no IPv4 address: %v; output: %s", command.label, parseErr, strings.TrimSpace(output)))
	}

	return "", errors.New(strings.Join(failures, "; "))
}

func waitForTartVMToBecomeReachable(projectDir string, vmName string, runState tartDetachedRun) (string, error) {
	return waitForTartVMToBecomeReachableWithOptions(projectDir, vmName, runState, tartReachabilityWaitOptions{})
}

func waitForTartVMToBecomeReachableWithOptions(
	projectDir string,
	vmName string,
	runState tartDetachedRun,
	options tartReachabilityWaitOptions,
) (string, error) {
	waitOptions := withDefaultTartReachabilityWaitOptions(projectDir, vmName, options)

	var lastErr error
	for attempt := 1; attempt <= waitOptions.maxAttempts; attempt++ {
		ip, err := waitOptions.detectIPv4()
		if err == nil {
			return ip, nil
		}
		lastErr = err

		if runState.pid > 0 {
			running, runErr := waitOptions.isProcessRunning(runState.pid)
			if runErr != nil {
				return "", fmt.Errorf("failed to inspect Tart process %d for %q: %w", runState.pid, vmName, runErr)
			}
			if !running {
				logSummary := waitOptions.readLogSummary(runState.logDir, runState.logFileName)
				if logSummary != "" {
					return "", fmt.Errorf("failed to start Tart VM %q: tart run exited early: %s", vmName, logSummary)
				}
				return "", fmt.Errorf("failed to start Tart VM %q: tart run exited early before an IPv4 address was assigned", vmName)
			}
		}

		if attempt < waitOptions.maxAttempts {
			waitOptions.sleep(waitOptions.retryInterval)
		}
	}

	if runState.logFileName != "" {
		logSummary := waitOptions.readLogSummary(runState.logDir, runState.logFileName)
		if logSummary != "" {
			return "", fmt.Errorf("Tart VM %q did not expose an IPv4 address before timeout. Last tart run log output: %s. Last IP discovery error: %w", vmName, logSummary, lastErr)
		}
	}

	return "", fmt.Errorf("Tart VM %q started but no IPv4 address became available: %w", vmName, lastErr)
}

func withDefaultTartReachabilityWaitOptions(
	projectDir string,
	vmName string,
	options tartReachabilityWaitOptions,
) tartReachabilityWaitOptions {
	discoveryOptions := withDefaultTartIPv4DiscoveryOptions(tartIPv4DiscoveryOptions{})

	if options.detectIPv4 == nil {
		options.detectIPv4 = func() (string, error) {
			return detectTartVMIPv4(projectDir, vmName, discoveryOptions.runCommand, discoveryOptions.commandTimeout)
		}
	}
	if options.isProcessRunning == nil {
		options.isProcessRunning = isProcessRunning
	}
	if options.readLogSummary == nil {
		options.readLogSummary = readTartRunLogSummary
	}
	if options.sleep == nil {
		options.sleep = time.Sleep
	}
	if options.retryInterval <= 0 {
		options.retryInterval = discoveryOptions.retryInterval
	}
	if options.maxAttempts <= 0 {
		options.maxAttempts = discoveryOptions.maxAttempts
	}

	return options
}

func parseBackgroundPID(output string) (int, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}

		pid, err := strconv.Atoi(line)
		if err == nil && pid > 0 {
			return pid, nil
		}
	}

	return 0, errors.New("no shell background pid found in command output")
}

func isProcessRunning(pid int) (bool, error) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}

	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrProcessDone) {
		return false, nil
	}

	errText := strings.ToLower(err.Error())
	if strings.Contains(errText, "process already finished") || strings.Contains(errText, "no such process") {
		return false, nil
	}

	return false, err
}

func readTartRunLogSummary(logDir string, logFileName string) string {
	if strings.TrimSpace(logDir) == "" || strings.TrimSpace(logFileName) == "" {
		return ""
	}

	logRoot, err := os.OpenRoot(logDir)
	if err != nil {
		return ""
	}
	defer logRoot.Close()

	content, err := logRoot.ReadFile(logFileName)
	if err != nil {
		return ""
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) == 0 {
		return ""
	}

	const maxLines = 5
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	summary := strings.TrimSpace(strings.Join(lines, " | "))
	if summary == "" {
		return ""
	}

	return summary
}

// TartMacOSBridgedInterfaceEnvVar names the host interface Tart VMs are bridged to;
// unset means Tart's shared NAT network.
const TartMacOSBridgedInterfaceEnvVar = tartMacOSBridgedInterfaceEnvVar
