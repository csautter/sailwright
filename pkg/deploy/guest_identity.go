package deploy

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	build "github.com/csautter/dev-alchemy/pkg/build"
)

var runGuestIdentityCommand = runCommandWithCombinedOutput

const (
	utmctlPath                  = "/Applications/UTM.app/Contents/MacOS/utmctl"
	linuxRunningHostnamePath    = "/proc/sys/kernel/hostname"
	linuxConfiguredHostnamePath = "/etc/hostname"
	linuxMachineIDPath          = "/etc/machine-id"
	linuxIdentityPath           = "/var/lib/sailwright/instance-identity"
	windowsIdentityProbePath    = `C:\Windows\System32\sailwright-instance-identity.txt`
	windowsPowerShellPath       = `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
)

var (
	guestHostnamePattern  = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)
	linuxMachineIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

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
	project := build.GetDirectoriesInstance().ProjectDir
	deadline := time.Now().Add(3 * time.Minute)
	if vm.VirtualizationEngine == build.VirtualizationEngineUtm {
		return configureNamedUTMGuestFromBundle(vm, project, deadline)
	}
	path, args := guestHostnameCommand(vm)
	payload, _ := json.Marshal(map[string]any{"execute": "guest-exec", "arguments": map[string]any{"path": path, "arg": args, "capture-output": true}})
	var lastErr error
	for time.Now().Before(deadline) {
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
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("set hostname of named VM %s: guest agent unavailable: %w", build.InstanceName(vm), lastErr)
}

func configureNamedUTMGuestFromBundle(vm build.VirtualMachineConfig, project string, deadline time.Time) error {
	// Start already verified the registration belongs to this bundle.
	id, exists, err := inspectUtmInstanceIDWithRunner(vm, func(project string, timeout time.Duration, executable string, args []string) (string, error) {
		return runGuestIdentityCommandBefore(deadline, project, timeout, executable, args)
	})
	if err != nil {
		return fmt.Errorf("select named UTM guest %s for identity setup: %w", build.InstanceName(vm), err)
	}
	if !exists {
		return fmt.Errorf("select named UTM guest %s for identity setup: VM bundle is missing", build.InstanceName(vm))
	}
	// utmctl compares the identifier with UTM's uppercase UUID string.
	return configureNamedUTMGuest(vm, project, strings.ToUpper(id), deadline, 2*time.Second)
}

func runGuestIdentityCommandBefore(deadline time.Time, project string, timeout time.Duration, executable string, args []string) (string, error) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return "", context.DeadlineExceeded
	}
	return runGuestIdentityCommand(project, min(remaining, timeout), executable, args)
}

func configureNamedUTMGuest(vm build.VirtualMachineConfig, project, id string, deadline time.Time, retryInterval time.Duration) error {
	instance, hostname := build.InstanceName(vm), build.InstanceHostname(vm)
	run := func(args ...string) (string, error) {
		return runGuestIdentityCommandBefore(deadline, project, 30*time.Second, utmctlPath, args)
	}
	pull := func(path string) (string, error) {
		output, err := run("file", "pull", id, path)
		if err != nil {
			return "", fmt.Errorf("read guest file %s: %w", path, err)
		}
		return strings.TrimSuffix(output, "\n"), nil
	}
	retry := func(check func() error) error {
		lastErr := error(context.DeadlineExceeded)
		for time.Now().Before(deadline) {
			lastErr = check()
			if lastErr == nil {
				if time.Now().Before(deadline) {
					return nil
				}
				return context.DeadlineExceeded
			}
			time.Sleep(min(time.Until(deadline), retryInterval))
		}
		return lastErr
	}

	probe := func() error {
		output, err := pull(linuxRunningHostnamePath)
		if err == nil && !guestHostnamePattern.MatchString(output) {
			return fmt.Errorf("guest agent did not return a valid running hostname")
		}
		return err
	}
	verify := func() error {
		return verifyLinuxUTMIdentity(instance, hostname, pull)
	}
	// The in-guest lock survives a host timeout or an early utmctl exit, so
	// overlapping requests cannot allocate different IDs or race a restart.
	apply := []string{"exec", id, "--cmd", "/usr/bin/flock", "-n", "/run/lock/sailwright-instance-identity.lock", "/bin/sh", "-ec", linuxInstanceIdentityScript(vm)}
	if vm.OS == "windows11" {
		token := rand.Text()
		probeArgs := windowsUTMIdentityCommand(id, hostname, token+"-ready", false)
		probe = func() error {
			// The report may arrive after a premature exit or host timeout.
			_, _ = run(probeArgs...)
			output, err := pull(windowsIdentityProbePath)
			if err != nil {
				return err
			}
			prefix, reportedName, _ := strings.Cut(output, ":")
			if prefix != token+"-ready" || !guestHostnamePattern.MatchString(reportedName) {
				return fmt.Errorf("guest agent did not return a fresh Windows hostname")
			}
			return nil
		}
		apply = windowsUTMIdentityCommand(id, hostname, token+"-verified", true)
		verify = func() error {
			output, err := pull(windowsIdentityProbePath)
			if err != nil {
				return err
			}
			prefix, reportedName, _ := strings.Cut(output, ":")
			if prefix != token+"-verified" || !strings.EqualFold(reportedName, hostname) {
				return fmt.Errorf("guest agent did not confirm Windows computer name %q", hostname)
			}
			return nil
		}
	}

	if err := retry(probe); err != nil {
		return fmt.Errorf("timed out waiting for UTM guest agent readiness for named VM %s: %w", instance, err)
	}

	if err := retry(func() error {
		_, applyErr := run(apply...)
		// Older utmctl versions can exit before a guest command completes.
		// Read back guest state even if the host-side apply call timed out.
		err := verify()
		if err != nil && applyErr != nil {
			return fmt.Errorf("%w (applying identity: %v)", err, applyErr)
		}
		return err
	}); err != nil {
		return fmt.Errorf("timed out verifying UTM guest identity for named VM %s: %w", instance, err)
	}
	return nil
}

func verifyLinuxUTMIdentity(instance, hostname string, pull func(string) (string, error)) error {
	stamp, err := pull(linuxIdentityPath)
	if err != nil {
		return err
	}
	stampedInstance, stampedID, _ := strings.Cut(stamp, ":")
	if stampedInstance != instance || !linuxMachineIDPattern.MatchString(stampedID) || stampedID == "00000000000000000000000000000000" {
		return fmt.Errorf("guest identity stamp %s does not contain %s and a valid machine ID", linuxIdentityPath, instance)
	}
	machineID, err := pull(linuxMachineIDPath)
	if err != nil {
		return err
	}
	if machineID != stampedID {
		return fmt.Errorf("guest machine ID does not match %s", linuxIdentityPath)
	}
	runningHostname, err := pull(linuxRunningHostnamePath)
	if err != nil {
		return err
	}
	if runningHostname != hostname {
		return fmt.Errorf("guest hostname in %s does not match %q", linuxRunningHostnamePath, hostname)
	}
	configuredHostname, err := pull(linuxConfiguredHostnamePath)
	if err != nil {
		return err
	}
	if parseLinuxConfiguredHostname(configuredHostname) != hostname {
		return fmt.Errorf("guest hostname in %s does not match %q", linuxConfiguredHostnamePath, hostname)
	}
	return nil
}

// parseLinuxConfiguredHostname extracts the effective static hostname from
// /etc/hostname using systemd's hostname-setup.c line rules: ignore empty lines
// and lines whose first non-whitespace byte is '#', and take the first
// remaining trimmed line without lossy character cleanup.
func parseLinuxConfiguredHostname(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.Trim(line, " \t\r")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return trimmed
	}
	return ""
}

func windowsUTMIdentityCommand(id, hostname, token string, apply bool) []string {
	report := fmt.Sprintf("[IO.File]::WriteAllText('%s', '%s:' + [Environment]::MachineName)", windowsIdentityProbePath, token)
	script := "$ErrorActionPreference='Stop'\n" + report
	if apply {
		// Serialize renames across overlapping guest-exec requests. A configured
		// name can already be correct while Windows still needs to reboot.
		script = fmt.Sprintf(`$ErrorActionPreference='Stop'
$mutex = [Threading.Mutex]::new($false, 'Global\SailwrightInstanceIdentity')
try {
  try { $locked = $mutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $locked = $true }
  if (!$locked) { exit 1 }
  $expected = '%s'
  $configured = Get-ItemPropertyValue -Path 'HKLM:\SYSTEM\CurrentControlSet\Control\ComputerName\ComputerName' -Name ComputerName
  if ($configured -ne $expected) {
    Rename-Computer -NewName $expected -Force
  }
  if ([Environment]::MachineName -ne $expected -or $configured -ne $expected) {
    shutdown.exe /r /t 5
    exit $LASTEXITCODE
  }
  %s
} finally {
  if ($locked) { $mutex.ReleaseMutex() }
  $mutex.Dispose()
}`, hostname, report)
	}
	return []string{"exec", id, "--cmd", windowsPowerShellPath, "-NoProfile", "-NonInteractive", "-Command", script}
}

func guestHostnameCommand(vm build.VirtualMachineConfig) (string, []string) {
	hostname := build.InstanceHostname(vm)
	if vm.OS == "windows11" {
		script := fmt.Sprintf("$ErrorActionPreference='Stop'; if ($env:COMPUTERNAME -ne '%s') { Rename-Computer -NewName '%s' -Force; shutdown.exe /r /t 5 }", hostname, hostname)
		return windowsPowerShellPath, []string{"-NoProfile", "-NonInteractive", "-Command", script}
	}
	return "/bin/sh", []string{"-ec", linuxInstanceIdentityScript(vm)}
}

// The guest agent reaches the selected clone without depending on its inherited
// DHCP lease. Persist the new ID once, and restart network clients so they discard
// the image's cached DUID before any IP-based provisioning or tunnel lookup.
func linuxInstanceIdentityScript(vm build.VirtualMachineConfig) string {
	return `set -eu
umask 077
hostnamectl set-hostname ` + build.InstanceHostname(vm) + `
instance=` + build.InstanceName(vm) + `
state=` + linuxIdentityPath + `
pending="$state.pending"
renderers="$pending.renderers"
matches_file() {
  # The host accepts either exact content or a single final newline.
  printf '%s\n' "$2" | cmp -s "$1" - || printf '%s' "$2" | cmp -s "$1" -
}
machine_id=
if [ -f /etc/machine-id ]; then
  machine_id=$(cat /etc/machine-id)
fi
case "$machine_id" in
  *[!0123456789abcdef]*|00000000000000000000000000000000) machine_id= ;;
esac
if [ "${#machine_id}" -ne 32 ] || ! matches_file /etc/machine-id "$machine_id" ||
   [ ! -f "$state" ] || ! matches_file "$state" "$instance:$machine_id"; then
  mkdir -p /var/lib/sailwright
  if [ ! -f "$pending" ]; then
    tr -d '-' < /proc/sys/kernel/random/uuid > "$pending.tmp"
    mv "$pending.tmp" "$pending"
  fi
  if [ ! -f "$renderers" ]; then
    # Persist the restart list before changing the machine ID so a failed
    # restart or a configured renderer starting during boot still refreshes.
    : > "$renderers.tmp"
    for renderer in systemd-networkd.service NetworkManager.service; do
      active_state=$(systemctl show --property=ActiveState --value "$renderer")
      case "$active_state" in
        active|activating|reloading|failed) ;;
        *)
          unit_file_state=$(systemctl show --property=UnitFileState --value "$renderer")
          case "$unit_file_state" in
            enabled|enabled-runtime) ;;
            *) continue ;;
          esac
          ;;
      esac
      printf '%s\n' "$renderer" >> "$renderers.tmp"
    done
    mv "$renderers.tmp" "$renderers"
  fi
  rm -f "$state"
  cat "$pending" > /etc/machine-id
  mkdir -p /var/lib/dbus
  ln -sf /etc/machine-id /var/lib/dbus/machine-id
  while IFS= read -r renderer; do
    systemctl restart "$renderer"
  done < "$renderers"
  printf '%s:%s\n' "$instance" "$(cat /etc/machine-id)" > "$state.tmp"
  mv "$state.tmp" "$state"
fi
rm -f "$renderers" "$pending"
`
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
