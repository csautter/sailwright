package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestLinuxCloneIdentityIsPersistentAndRefreshesNetworking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executes the Linux guest shell script")
	}
	root := t.TempDir()
	for _, dir := range []string{"etc", "var/lib", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, value string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(value), mode); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	write("etc/machine-id", "inherited-image-id\n", 0o600)
	write("uuid", "12345678-1234-1234-1234-123456789abc\n", 0o600)
	write("network-state", "active\n", 0o600)
	write("bin/hostnamectl", "#!/bin/sh\nexit 0\n", 0o700)
	write("bin/systemctl", `#!/bin/sh
case "$*" in
'show --property=ActiveState --value systemd-networkd.service')
  cat "$TEST_NETWORK_STATE" ;;
'show --property=ActiveState --value NetworkManager.service')
  printf '%s\n' inactive ;;
'show --property=UnitFileState --value systemd-networkd.service')
  printf '%s\n' enabled ;;
'show --property=UnitFileState --value NetworkManager.service')
  printf '%s\n' disabled ;;
'restart systemd-networkd.service'|'restart NetworkManager.service')
  printf '%s\n' "$*" >> "$TEST_CALLS"
  if [ "${TEST_FAIL_NETWORK:-}" = 1 ]; then
    printf '%s\n' failed > "$TEST_NETWORK_STATE"
    exit 1
  fi
  printf '%s\n' active > "$TEST_NETWORK_STATE" ;;
*) exit 2 ;;
esac
`, 0o700)
	vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", Name: "a", VirtualizationEngine: build.VirtualizationEngineQemu}
	const machineFile = "etc/machine-id"
	stamp := strings.TrimPrefix(linuxIdentityPath, "/")
	pending := stamp + ".pending"
	run := func(fail bool) error {
		t.Helper()
		script := strings.NewReplacer("/etc/machine-id", filepath.Join(root, "etc/machine-id"), "/var/lib", filepath.Join(root, "var/lib"), "/proc/sys/kernel/random/uuid", filepath.Join(root, "uuid")).Replace(linuxInstanceIdentityScript(vm))
		cmd := exec.Command("sh", "-ec", script)
		cmd.Env = append(os.Environ(),
			"PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
			"TEST_CALLS="+filepath.Join(root, "calls"),
			"TEST_NETWORK_STATE="+filepath.Join(root, "network-state"),
		)
		if fail {
			cmd.Env = append(cmd.Env, "TEST_FAIL_NETWORK=1")
		}
		data, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, data)
		}
		return nil
	}
	requireUnstamped := func() {
		t.Helper()
		if _, err := os.Stat(filepath.Join(root, stamp)); !os.IsNotExist(err) {
			t.Fatal("marked failed initialization complete")
		}
	}
	verify := func() error {
		return verifyLinuxUTMIdentity(build.InstanceName(vm), build.InstanceHostname(vm), func(path string) (string, error) {
			if path == linuxRunningHostnamePath || path == linuxConfiguredHostnamePath {
				return build.InstanceHostname(vm), nil
			}
			return strings.TrimSuffix(read(strings.TrimPrefix(path, "/")), "\n"), nil
		})
	}
	if err := run(true); err == nil {
		t.Fatal("network restart failure was ignored")
	}
	id := read(machineFile)
	if id != "12345678123412341234123456789abc\n" || read("network-state") != "failed\n" {
		t.Fatal(id)
	}
	requireUnstamped()
	// Retry retains the pending machine ID even if the random source changes.
	write("uuid", "87654321-4321-4321-4321-cba987654321\n", 0o600)
	if err := run(true); err == nil {
		t.Fatal("retry ignored the failed renderer")
	}
	requireUnstamped()
	if read(pending) != id || read(machineFile) != id {
		t.Fatal("failed retry changed the pending identity")
	}
	write("network-state", "inactive\n", 0o600)
	if err := run(false); err != nil {
		t.Fatal(err)
	}
	if read(machineFile) != id {
		t.Fatal("retry changed the pending identity")
	}
	calls := read("calls")
	const restart = "restart systemd-networkd.service\n"
	if calls != strings.Repeat(restart, 3) {
		t.Fatal(calls)
	}
	if err := verify(); err != nil {
		t.Fatal(err)
	}
	// Both forms previously accepted by Go must remain idempotent.
	write(machineFile, strings.TrimSuffix(id, "\n"), 0o600)
	write(stamp, strings.TrimSuffix(read(stamp), "\n"), 0o600)
	if err := run(false); err != nil {
		t.Fatal(err)
	}
	if read(machineFile) != strings.TrimSuffix(id, "\n") || read("calls") != calls {
		t.Fatal("ordinary start changed identity or restarted networking")
	}
	// Older scripts could leave an installed pending ID and no renderer list.
	for _, state := range []string{"failed", "inactive"} {
		if err := os.Remove(filepath.Join(root, stamp)); err != nil {
			t.Fatal(err)
		}
		write(pending, id, 0o600)
		write("network-state", state+"\n", 0o600)
		if err := run(false); err != nil {
			t.Fatal(err)
		}
		calls += restart
		if read(machineFile) != id || read("calls") != calls {
			t.Fatalf("legacy retry with %s renderer did not restore networking with the pending ID", state)
		}
		if err := verify(); err != nil {
			t.Fatal(err)
		}
	}
	// Even an image containing another instance's marker receives a fresh ID.
	vm.Name = "b"
	if err := run(false); err != nil {
		t.Fatal(err)
	}
	if read(machineFile) == id {
		t.Fatal("sibling clone retained the machine ID")
	}
	if read("var/lib/dbus/machine-id") != read(machineFile) {
		t.Fatal("D-Bus machine ID differs")
	}
	for _, path := range []string{stamp, machineFile} {
		valid := read(path)
		write(path, valid+"\n", 0o600)
		if err := verify(); err == nil {
			t.Fatalf("readback accepted extra newline in %s", path)
		}
		if err := run(false); err != nil {
			t.Fatal(err)
		}
		if got := read(path); got != valid {
			t.Fatalf("repair of %s returned %q, want %q", path, got, valid)
		}
		if err := verify(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLinuxCloneIdentityRefreshesRendererStartingDuringBoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executes the Linux guest shell script")
	}
	for _, tc := range []struct {
		name         string
		initialState string
	}{
		{"activating networkd during sampling", "activating"},
		{"inactive enabled-runtime networkd during boot", "inactive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"etc", "var/lib", "bin"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, value string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, path), []byte(value), mode); err != nil {
					t.Fatal(err)
				}
			}
			read := func(path string) string {
				t.Helper()
				data, err := os.ReadFile(filepath.Join(root, path))
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}
			const (
				inheritedID = "11111111111111111111111111111111\n"
				freshUUID   = "22222222-2222-2222-2222-222222222222\n"
				freshID     = "22222222222222222222222222222222\n"
			)
			vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "arm64", Name: "work", VirtualizationEngine: build.VirtualizationEngineUtm}
			stampRel := strings.TrimPrefix(linuxIdentityPath, "/")
			write("etc/machine-id", inheritedID, 0o600)
			write("etc/hostname", "# managed locally\n"+build.InstanceHostname(vm)+"\n", 0o600)
			write("uuid", freshUUID, 0o600)
			write("network-state", tc.initialState+"\n", 0o600)
			// Model hostnamectl returning 0 without rewriting an already-matching annotated /etc/hostname.
			write("bin/hostnamectl", "#!/bin/sh\nexit 0\n", 0o700)
			write("bin/systemctl", `#!/bin/sh
set -eu
case "$*" in
'show --property=ActiveState --value systemd-networkd.service')
  cat "$TEST_NETWORK_STATE" ;;
'show --property=UnitFileState --value systemd-networkd.service')
  printf '%s\n' enabled-runtime ;;
'show --property=ActiveState --value NetworkManager.service')
  # Simulate networkd completing activation and caching the inherited ID
  # before the script writes the replacement machine ID.
  if [ ! -f "$TEST_RENDERER_ID" ]; then
    cat "$TEST_MACHINE_ID" > "$TEST_RENDERER_ID"
    printf '%s\n' active > "$TEST_NETWORK_STATE"
  fi
  printf '%s\n' inactive ;;
'show --property=UnitFileState --value NetworkManager.service')
  # Absent unit reports empty UnitFileState with exit 0.
  printf '\n' ;;
'restart systemd-networkd.service')
  if [ -f "$TEST_STAMP" ]; then
    echo "stamp published before renderer restart" >&2
    exit 9
  fi
  printf '%s\n' "$*" >> "$TEST_CALLS"
  cat "$TEST_MACHINE_ID" > "$TEST_RENDERER_ID"
  printf '%s\n' active > "$TEST_NETWORK_STATE" ;;
'restart NetworkManager.service')
  echo "attempted to restart absent NetworkManager.service" >&2
  exit 5 ;;
*) exit 2 ;;
esac
`, 0o700)
			run := func() error {
				t.Helper()
				script := strings.NewReplacer(
					"/etc/machine-id", filepath.Join(root, "etc/machine-id"),
					"/var/lib", filepath.Join(root, "var/lib"),
					"/proc/sys/kernel/random/uuid", filepath.Join(root, "uuid"),
				).Replace(linuxInstanceIdentityScript(vm))
				cmd := exec.Command("sh", "-ec", script)
				cmd.Env = append(os.Environ(),
					"PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
					"TEST_CALLS="+filepath.Join(root, "calls"),
					"TEST_NETWORK_STATE="+filepath.Join(root, "network-state"),
					"TEST_MACHINE_ID="+filepath.Join(root, "etc/machine-id"),
					"TEST_RENDERER_ID="+filepath.Join(root, "renderer-id"),
					"TEST_STAMP="+filepath.Join(root, stampRel),
				)
				if data, err := cmd.CombinedOutput(); err != nil {
					return fmt.Errorf("%w: %s", err, data)
				}
				return nil
			}
			verify := func() error {
				return verifyLinuxUTMIdentity(build.InstanceName(vm), build.InstanceHostname(vm), func(path string) (string, error) {
					if path == linuxRunningHostnamePath {
						return build.InstanceHostname(vm), nil
					}
					return strings.TrimSuffix(read(strings.TrimPrefix(path, "/")), "\n"), nil
				})
			}

			if err := run(); err != nil {
				t.Fatal(err)
			}
			if got := read("etc/machine-id"); got != freshID {
				t.Fatalf("disk machine-id = %q, want %q", got, freshID)
			}
			if got := read("renderer-id"); got != freshID {
				t.Fatalf("renderer cached machine-id = %q, want %q", got, freshID)
			}
			const restart = "restart systemd-networkd.service\n"
			if got := read("calls"); got != restart {
				t.Fatalf("systemctl calls = %q, want %q", got, restart)
			}
			if err := verify(); err != nil {
				t.Fatal(err)
			}
			// Subsequent start on an already-stamped guest remains idempotent.
			if err := run(); err != nil {
				t.Fatal(err)
			}
			if got := read("calls"); got != restart {
				t.Fatalf("second apply restarted networking: %q", got)
			}
			if err := verify(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNamedGuestHostnameUsesProviderIdentity(t *testing.T) {
	old := runGuestIdentityCommand
	defer func() { runGuestIdentityCommand = old }()
	vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", Name: "work", VirtualizationEngine: build.VirtualizationEngineQemu}
	calls := 0
	runGuestIdentityCommand = func(_ string, _ time.Duration, executable string, args []string) (string, error) {
		calls++
		if executable != "virsh" || args[3] != build.InstanceName(vm) {
			t.Fatal(executable, args)
		}
		var payload struct {
			Execute   string         `json:"execute"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(args[4]), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Execute == "guest-exec" {
			if !strings.Contains(args[4], "hostnamectl set-hostname ubuntu-server-amd64-work-dev-alchemy") {
				t.Fatal(args[4])
			}
			return `{"return":{"pid":12}}`, nil
		}
		return `{"return":{"exited":true,"exitcode":0}}`, nil
	}
	if err := ConfigureNamedGuestHostname(vm); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	vm.Name = ""
	if err := ConfigureNamedGuestHostname(vm); err != nil || calls != 2 {
		t.Fatal("default guest was changed", err)
	}
}

const guestIdentityTestUTMUUID = "AABBCCDD-1111-2222-3333-444444444444"

func fakeUTMGuestIdentity(t *testing.T, fake func(time.Duration, []string) (string, error)) {
	t.Helper()
	old := runGuestIdentityCommand
	t.Cleanup(func() { runGuestIdentityCommand = old })
	runGuestIdentityCommand = func(_ string, timeout time.Duration, executable string, args []string) (string, error) {
		if executable != utmctlPath || timeout <= 0 || timeout > 30*time.Second {
			t.Fatalf("unexpected command %s with timeout %s", executable, timeout)
		}
		switch args[0] {
		case "exec":
			if args[1] != guestIdentityTestUTMUUID || args[2] != "--cmd" {
				t.Fatal(args)
			}
		case "file":
			if len(args) != 4 || args[1] != "pull" || args[2] != guestIdentityTestUTMUUID {
				t.Fatal(args)
			}
		default:
			t.Fatal(args)
		}
		return fake(timeout, args)
	}
}

func TestConfigureNamedUTMGuestUsesBundleUUID(t *testing.T) {
	for _, tc := range []struct {
		name   string
		budget time.Duration
	}{
		{"lookup cap", 0},
		{"lookup and readiness share the deadline", time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm, _, _ := mockMacOSLifecycle(t)
			vm.Name = "work"
			bundle, err := utmVirtualMachinePath(vm)
			if err != nil {
				t.Fatal(err)
			}
			writeMacFixture(t, filepath.Join(bundle, "config.plist"), strings.ToLower(guestIdentityTestUTMUUID))
			const (
				machineID  = "12345678123412341234123456789abc"
				lookupTime = 20 * time.Millisecond
			)
			var lookupTimeout time.Duration
			fakeUTMGuestIdentity(t, func(timeout time.Duration, args []string) (string, error) {
				if lookupTimeout == 0 {
					t.Fatal("guest contacted before UUID lookup")
				}
				if tc.budget > 0 && timeout > lookupTimeout-lookupTime {
					t.Fatalf("guest command received %s after lookup spent at least %s of %s", timeout, lookupTime, lookupTimeout)
				}
				if args[0] == "exec" {
					return "", nil
				}
				switch args[3] {
				case linuxIdentityPath:
					return build.InstanceName(vm) + ":" + machineID + "\n", nil
				case linuxMachineIDPath:
					return machineID + "\n", nil
				case linuxRunningHostnamePath, linuxConfiguredHostnamePath:
					return build.InstanceHostname(vm) + "\n", nil
				}
				t.Fatal(args)
				return "", nil
			})
			runUTM := runGuestIdentityCommand
			runGuestIdentityCommand = func(project string, timeout time.Duration, executable string, args []string) (string, error) {
				if executable != "plutil" {
					return runUTM(project, timeout, executable, args)
				}
				lookupTimeout = timeout
				if tc.budget > 0 {
					if timeout <= 0 || timeout > tc.budget {
						t.Fatalf("lookup timeout %s outside the remaining budget %s", timeout, tc.budget)
					}
					time.Sleep(lookupTime)
				} else if timeout != instanceIDQueryTimeout {
					t.Fatalf("lookup timeout %s, want %s", timeout, instanceIDQueryTimeout)
				}
				return runCommandWithCombinedOutput(project, timeout, executable, args)
			}
			if tc.budget > 0 {
				err = configureNamedUTMGuestFromBundle(vm, "", time.Now().Add(tc.budget))
			} else {
				err = ConfigureNamedGuestHostname(vm)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNamedUTMGuestVerifiesIdentityAfterLateAgentReadiness(t *testing.T) {
	vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "desktop", Arch: "arm64", Name: "work", VirtualizationEngine: build.VirtualizationEngineUtm}
	instance := build.InstanceName(vm)
	hostname := build.InstanceHostname(vm)
	const machineID = "12345678123412341234123456789abc"
	probes, applies := 0, 0
	fakeUTMGuestIdentity(t, func(_ time.Duration, args []string) (string, error) {
		if args[0] == "exec" {
			if probes != 3 || args[3] != "/usr/bin/flock" || args[4] != "-n" || !strings.Contains(args[len(args)-1], "hostnamectl set-hostname "+hostname) {
				t.Fatal(args)
			}
			applies++
			if applies == 2 {
				// A guest command can still finish after the host call times out.
				return "", errors.New("host timeout")
			}
			return "", nil
		}
		if applies == 0 {
			if args[3] != linuxRunningHostnamePath {
				t.Fatal(args)
			}
			probes++
			if probes < 3 {
				return "", nil
			}
			return "ubuntu-desktop\n", nil
		}
		switch args[3] {
		case linuxIdentityPath:
			if applies == 1 {
				return "", nil
			}
			return instance + ":" + machineID + "\n", nil
		case linuxMachineIDPath:
			return machineID + "\n", nil
		case linuxRunningHostnamePath, linuxConfiguredHostnamePath:
			return hostname + "\n", nil
		}
		t.Fatal(args)
		return "", nil
	})
	if err := configureNamedUTMGuest(vm, "", guestIdentityTestUTMUUID, time.Now().Add(time.Second), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if probes != 3 || applies != 2 {
		t.Fatalf("got %d readiness probes and %d apply calls", probes, applies)
	}
}

func TestNamedUTMGuestIdentityDeadline(t *testing.T) {
	vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "arm64", Name: "work", VirtualizationEngine: build.VirtualizationEngineUtm}
	const budget = 100 * time.Millisecond
	t.Run("no readiness evidence", func(t *testing.T) {
		fakeUTMGuestIdentity(t, func(timeout time.Duration, args []string) (string, error) {
			if args[0] != "file" || args[3] != linuxRunningHostnamePath || timeout > budget {
				t.Fatal(timeout, args)
			}
			return "", nil
		})
		start := time.Now()
		err := configureNamedUTMGuest(vm, "", guestIdentityTestUTMUUID, start.Add(budget), 2*time.Second)
		if err == nil || !strings.Contains(err.Error(), "timed out waiting for UTM guest agent readiness") || !strings.Contains(err.Error(), build.InstanceName(vm)) {
			t.Fatal(err)
		}
		if time.Since(start) >= time.Second {
			t.Fatal("readiness retry slept past the deadline")
		}
	})
	t.Run("no verified state", func(t *testing.T) {
		fakeUTMGuestIdentity(t, func(timeout time.Duration, args []string) (string, error) {
			if timeout > budget {
				t.Fatal(timeout)
			}
			switch {
			case args[0] == "exec":
				return "", nil
			case args[3] == linuxRunningHostnamePath:
				return "ubuntu-server\n", nil
			case args[3] == linuxIdentityPath:
				return "", nil
			}
			t.Fatal(args)
			return "", nil
		})
		start := time.Now()
		err := configureNamedUTMGuest(vm, "", guestIdentityTestUTMUUID, start.Add(budget), 2*time.Second)
		if err == nil || !strings.Contains(err.Error(), "timed out verifying UTM guest identity") || !strings.Contains(err.Error(), linuxIdentityPath) {
			t.Fatal(err)
		}
		if time.Since(start) >= time.Second {
			t.Fatal("verification retry slept past the deadline")
		}
	})
	t.Run("apply consumes the remaining budget", func(t *testing.T) {
		fakeUTMGuestIdentity(t, func(timeout time.Duration, args []string) (string, error) {
			if timeout > budget {
				t.Fatal(timeout)
			}
			switch {
			case args[0] == "exec":
				time.Sleep(timeout)
				return "", errors.New("host timeout")
			case args[3] == linuxRunningHostnamePath:
				return "ubuntu-server\n", nil
			}
			t.Fatal("readback ran after the deadline", args)
			return "", nil
		})
		err := configureNamedUTMGuest(vm, "", guestIdentityTestUTMUUID, time.Now().Add(budget), 2*time.Second)
		if err == nil || !strings.Contains(err.Error(), "timed out verifying UTM guest identity") || !strings.Contains(err.Error(), "host timeout") {
			t.Fatal(err)
		}
	})
}

func TestVerifyLinuxUTMIdentityAcceptsAnnotatedConfiguredHostname(t *testing.T) {
	const instance = "ubuntu-server-arm64-work-dev-alchemy"
	const machineID = "12345678123412341234123456789abc"
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"comment above hostname", "# managed locally\n" + instance},
		{"blank lines, whitespace, and trailing comment", "\n  # managed locally\n\n  " + instance + " \t\r\n# trailing comment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := map[string]string{
				linuxIdentityPath:           instance + ":" + machineID,
				linuxMachineIDPath:          machineID,
				linuxRunningHostnamePath:    instance,
				linuxConfiguredHostnamePath: tc.content,
			}
			if err := verifyLinuxUTMIdentity(instance, instance, func(path string) (string, error) {
				return state[path], nil
			}); err != nil {
				t.Fatalf("rejected valid annotated %s (%q): %v", linuxConfiguredHostnamePath, tc.content, err)
			}
		})
	}
}

func TestVerifyLinuxUTMIdentityRejectsMismatchedState(t *testing.T) {
	const instance = "ubuntu-server-arm64-work-dev-alchemy"
	const machineID = "12345678123412341234123456789abc"
	for _, check := range []struct {
		name, path, content string
	}{
		{"blank machine ID", linuxMachineIDPath, ""},
		{"invalid stamped machine ID", linuxIdentityPath, instance + ":invalid"},
		{"zero stamped machine ID", linuxIdentityPath, instance + ":00000000000000000000000000000000"},
		{"different instance", linuxIdentityPath, "sibling:" + machineID},
		{"different machine ID", linuxMachineIDPath, "87654321432143214321cba987654321"},
		{"different running hostname", linuxRunningHostnamePath, "ubuntu-server"},
		{"annotated running hostname", linuxRunningHostnamePath, "# managed locally\n" + instance},
		{"different configured hostname", linuxConfiguredHostnamePath, "ubuntu-desktop"},
		{"annotated different configured hostname", linuxConfiguredHostnamePath, "# managed locally\nubuntu-desktop"},
		{"comment-only configured hostname", linuxConfiguredHostnamePath, "# managed locally\n\n"},
		{"inline comment in configured hostname", linuxConfiguredHostnamePath, instance + " # managed locally"},
	} {
		t.Run(check.name, func(t *testing.T) {
			state := map[string]string{
				linuxIdentityPath:           instance + ":" + machineID,
				linuxMachineIDPath:          machineID,
				linuxRunningHostnamePath:    instance,
				linuxConfiguredHostnamePath: instance,
			}
			state[check.path] = check.content
			if err := verifyLinuxUTMIdentity(instance, instance, func(path string) (string, error) {
				return state[path], nil
			}); err == nil {
				t.Fatal("accepted mismatched state")
			}
		})
	}
}

func TestNamedUTMWindowsGuestVerifiesComputerNameAfterRestart(t *testing.T) {
	vm := build.VirtualMachineConfig{OS: "windows11", Arch: "arm64", Name: "work", VirtualizationEngine: build.VirtualizationEngineUtm}
	probes, applies := 0, 0
	var report string
	tokenPattern := regexp.MustCompile(`'([A-Z2-7]+-(ready|verified)):'`)
	fakeUTMGuestIdentity(t, func(_ time.Duration, args []string) (string, error) {
		if args[0] == "file" {
			if args[3] != windowsIdentityProbePath {
				t.Fatal(args)
			}
			return report, nil
		}
		if args[3] != windowsPowerShellPath || len(args) != 8 {
			t.Fatal(args)
		}
		script := args[7]
		fields := tokenPattern.FindStringSubmatch(script)
		if len(fields) != 3 {
			t.Fatal(script)
		}
		if fields[2] == "ready" {
			probes++
			if probes == 1 {
				report = "STALE-ready:WIN-IMAGE"
			} else {
				report = fields[1] + ":WIN-IMAGE"
			}
			return "", nil
		}
		if probes != 2 || !strings.Contains(script, "Rename-Computer") || !strings.Contains(script, "shutdown.exe /r /t 5") {
			t.Fatal(script)
		}
		applies++
		if applies == 2 {
			report = fields[1] + ":WRONG-NAME"
		} else if applies == 3 {
			report = fields[1] + ":" + strings.ToUpper(build.InstanceHostname(vm))
		}
		return "", nil
	})
	if err := configureNamedUTMGuest(vm, "", guestIdentityTestUTMUUID, time.Now().Add(time.Second), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if probes != 2 || applies != 3 {
		t.Fatalf("got %d readiness probes and %d apply calls", probes, applies)
	}
}
