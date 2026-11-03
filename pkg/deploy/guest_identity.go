package deploy

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	build "github.com/csautter/dev-alchemy/pkg/build"
)

var runGuestIdentityCommand = runCommandWithCombinedOutput

// ConfigureNamedGuestHostname uses the guest agent, so even a cloned guest with
// an inherited network identity is selected by its provider identity, not its IP.
// Defaults are deliberately untouched. Tart uses its existing SSH provisioning.
func ConfigureNamedGuestHostname(vm build.VirtualMachineConfig) error {
	if vm.Name == "" {
		return nil
	}
	if err := build.ValidateInstance(vm); err != nil {
		return err
	}
	if vm.VirtualizationEngine != build.VirtualizationEngineQemu && vm.VirtualizationEngine != build.VirtualizationEngineUtm {
		return nil
	}
	path, args := guestHostnameCommand(vm)
	project := build.GetDirectoriesInstance().ProjectDir
	deadline := time.Now().Add(3 * time.Minute)
	var lastErr error
	for time.Now().Before(deadline) {
		if vm.VirtualizationEngine == build.VirtualizationEngineUtm {
			_, lastErr = runGuestIdentityCommand(project, 30*time.Second, "/Applications/UTM.app/Contents/MacOS/utmctl", append([]string{"exec", build.InstanceName(vm), "--cmd", path}, args...))
			if lastErr == nil {
				return nil
			}
		} else {
			payload, _ := json.Marshal(map[string]any{"execute": "guest-exec", "arguments": map[string]any{"path": path, "arg": args, "capture-output": true}})
			output, err := runGuestIdentityCommand(project, 30*time.Second, "virsh", []string{"--connect", linuxLibvirtURI(), "qemu-agent-command", build.InstanceName(vm), string(payload)})
			lastErr = err
			if err == nil {
				var started struct {
					Return struct {
						PID int `json:"pid"`
					} `json:"return"`
				}
				if err = json.Unmarshal([]byte(output), &started); err != nil || started.Return.PID <= 0 {
					return fmt.Errorf("guest agent did not return a hostname process id")
				}
				return waitGuestHostnameProcess(vm, started.Return.PID, deadline)
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("set hostname of named VM %s: guest agent unavailable: %w", build.InstanceName(vm), lastErr)
}

func guestHostnameCommand(vm build.VirtualMachineConfig) (string, []string) {
	hostname := build.InstanceHostname(vm)
	if vm.OS == "windows11" {
		script := fmt.Sprintf("$ErrorActionPreference='Stop'; if ($env:COMPUTERNAME -ne '%s') { Rename-Computer -NewName '%s' -Force; shutdown.exe /r /t 5 }", hostname, hostname)
		return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, []string{"-NoProfile", "-NonInteractive", "-Command", script}
	}
	return "/bin/sh", []string{"-ec", "hostnamectl set-hostname " + hostname}
}

func waitGuestHostnameProcess(vm build.VirtualMachineConfig, pid int, deadline time.Time) error {
	payload := `{"execute":"guest-exec-status","arguments":{"pid":` + strconv.Itoa(pid) + `}}`
	for time.Now().Before(deadline) {
		output, err := runGuestIdentityCommand(build.GetDirectoriesInstance().ProjectDir, 30*time.Second, "virsh", []string{"--connect", linuxLibvirtURI(), "qemu-agent-command", build.InstanceName(vm), payload})
		if err != nil {
			return err
		}
		var status struct {
			Return *struct {
				Exited   bool `json:"exited"`
				ExitCode int  `json:"exitcode"`
				Signal   int  `json:"signal"`
			} `json:"return"`
		}
		if err = json.Unmarshal([]byte(output), &status); err != nil || status.Return == nil {
			return fmt.Errorf("invalid guest hostname process status")
		}
		if status.Return.Exited {
			if status.Return.ExitCode != 0 || status.Return.Signal != 0 {
				return fmt.Errorf("guest hostname command failed (exit %d, signal %d)", status.Return.ExitCode, status.Return.Signal)
			}
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("guest hostname command timed out")
}
