package provision

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

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

func tartMacOSVMName(vm alchemy_build.VirtualMachineConfig) string {
	return alchemy_build.InstanceName(vm)
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
