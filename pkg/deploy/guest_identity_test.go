package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	write("bin/hostnamectl", "#!/bin/sh\nexit 0\n", 0o700)
	write("bin/systemctl", `#!/bin/sh
if [ "$1" = is-active ]; then
  [ "$3" = systemd-networkd.service ]
else
  printf '%s\n' "$*" >> "$TEST_CALLS"
  [ "${TEST_FAIL_NETWORK:-}" != 1 ]
fi
`, 0o700)
	vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "server", Arch: "amd64", Name: "a", VirtualizationEngine: build.VirtualizationEngineQemu}
	run := func(fail bool) error {
		t.Helper()
		script := strings.NewReplacer("/etc/machine-id", filepath.Join(root, "etc/machine-id"), "/var/lib", filepath.Join(root, "var/lib"), "/proc/sys/kernel/random/uuid", filepath.Join(root, "uuid")).Replace(linuxInstanceIdentityScript(vm))
		cmd := exec.Command("sh", "-ec", script)
		cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "TEST_CALLS="+filepath.Join(root, "calls"))
		if fail {
			cmd.Env = append(cmd.Env, "TEST_FAIL_NETWORK=1")
		}
		data, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, data)
		}
		return nil
	}
	if err := run(true); err == nil {
		t.Fatal("network restart failure was ignored")
	}
	id := read("etc/machine-id")
	if strings.TrimSpace(id) != "12345678123412341234123456789abc" {
		t.Fatal(id)
	}
	if _, err := os.Stat(filepath.Join(root, "var/lib/sailwright/instance-identity")); !os.IsNotExist(err) {
		t.Fatal("marked failed initialization complete")
	}
	// Retry retains the pending machine ID even if the random source changes.
	write("uuid", "87654321-4321-4321-4321-cba987654321\n", 0o600)
	if err := run(false); err != nil {
		t.Fatal(err)
	}
	if read("etc/machine-id") != id {
		t.Fatal("retry changed the pending identity")
	}
	calls := read("calls")
	if strings.Count(calls, "restart systemd-networkd.service") != 2 || strings.Contains(calls, "restart NetworkManager") {
		t.Fatal(calls)
	}
	if err := run(false); err != nil {
		t.Fatal(err)
	}
	if read("etc/machine-id") != id || read("calls") != calls {
		t.Fatal("ordinary start changed identity or restarted networking")
	}
	// Even an image containing another instance's marker receives a fresh ID.
	vm.Name = "b"
	if err := run(false); err != nil {
		t.Fatal(err)
	}
	if read("etc/machine-id") == id {
		t.Fatal("sibling clone retained the machine ID")
	}
	if read("var/lib/dbus/machine-id") != read("etc/machine-id") {
		t.Fatal("D-Bus machine ID differs")
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

func TestNamedUTMGuestHostnameCommand(t *testing.T) {
	old := runGuestIdentityCommand
	defer func() { runGuestIdentityCommand = old }()
	vm := build.VirtualMachineConfig{OS: "ubuntu", UbuntuType: "desktop", Arch: "arm64", Name: "work", VirtualizationEngine: build.VirtualizationEngineUtm}
	runGuestIdentityCommand = func(_ string, _ time.Duration, executable string, args []string) (string, error) {
		if !strings.HasSuffix(executable, "utmctl") || args[1] != build.InstanceName(vm) || !strings.Contains(strings.Join(args, " "), build.InstanceHostname(vm)) {
			t.Fatal(executable, args)
		}
		return "", nil
	}
	if err := ConfigureNamedGuestHostname(vm); err != nil {
		t.Fatal(err)
	}
}
