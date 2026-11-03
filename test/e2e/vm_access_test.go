//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/csautter/dev-alchemy/internal/gateway/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// askpassEnv makes the test binary act as SSH_ASKPASS: it prints the value and
// exits. Windows has no sshpass, but its OpenSSH honours SSH_ASKPASS.
const askpassEnv = "SAILWRIGHT_E2E_ASKPASS_SECRET"

func TestMain(m *testing.M) {
	if secret, ok := os.LookupEnv(askpassEnv); ok {
		fmt.Println(secret)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// sshClient runs the guest ssh and scp commands with the guest password.
type sshClient struct {
	ssh, scp string
	// extra options go before the other arguments; command-line options win over ssh_config.
	extra   []string
	askpass bool
}

// newSSHClient uses sshpass on Linux and macOS, as in the devcontainer. On
// Windows it uses the system OpenSSH with SSH_ASKPASS and without connection
// sharing, which Windows OpenSSH does not support.
func newSSHClient() (sshClient, error) {
	if runtime.GOOS != "windows" {
		for _, binary := range []string{"ssh", "scp", "sshpass"} {
			if _, err := exec.LookPath(binary); err != nil {
				return sshClient{}, fmt.Errorf("client prerequisite %s: %w", binary, err)
			}
		}
		return sshClient{ssh: "ssh", scp: "scp"}, nil
	}
	client := sshClient{askpass: true, extra: []string{"-o", "ControlMaster=no", "-o", "ControlPath=none"}}
	system := filepath.Join(os.Getenv("SystemRoot"), "System32", "OpenSSH")
	for _, name := range []string{"ssh", "scp"} {
		path := filepath.Join(system, name+".exe")
		if _, err := os.Stat(path); err != nil {
			if path, err = exec.LookPath(name); err != nil {
				return sshClient{}, fmt.Errorf("client prerequisite %s: %w", name, err)
			}
		}
		if name == "ssh" {
			client.ssh = path
		} else {
			client.scp = path
		}
	}
	return client, nil
}

func (c sshClient) command(ctx context.Context, binary string, password string, argv ...string) (*exec.Cmd, error) {
	path := c.ssh
	if binary == "scp" {
		path = c.scp
	}
	argv = append(append([]string(nil), c.extra...), argv...)
	if !c.askpass {
		cmd := exec.CommandContext(ctx, "sshpass", append([]string{"-e", path}, argv...)...)
		cmd.Env = append(os.Environ(), "SSHPASS="+password)
		return cmd, nil
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, argv...)
	cmd.Env = append(os.Environ(), "SSH_ASKPASS="+self, "SSH_ASKPASS_REQUIRE=force", "DISPLAY=sailwright-e2e", askpassEnv+"="+password)
	return cmd, nil
}

// TestVMAccess exercises the actual SSH/scp clients and gateway transport, from
// the devcontainer (sshpass) or natively on a Windows gateway host (SSH_ASKPASS).
// It is opt-in because it creates and destroys the selected managed VM.
func TestVMAccess(t *testing.T) {
	runVMAccess(t, false)
}

// TestWindowsVMAccess runs an actual WinRM command through the gateway listener.
// On a native Windows Hyper-V host it uses Ansible in CYGWIN_BASH_PATH.
func TestWindowsVMAccess(t *testing.T) {
	runVMAccess(t, true)
}

func runVMAccess(t *testing.T, windowsGuest bool) {
	if os.Getenv("SAILWRIGHT_VM_ACCESS_E2E") != "1" {
		t.Skip("set SAILWRIGHT_VM_ACCESS_E2E=1 to test a disposable guest over the gateway")
	}
	var sshClient sshClient
	var err error
	if windowsGuest {
		if runtime.GOOS == "windows" {
			// Cygwin's ansible is a Python script, not a native executable.
			bash := os.Getenv("CYGWIN_BASH_PATH")
			if bash == "" {
				t.Fatal("set CYGWIN_BASH_PATH to the native Cygwin bin/bash.exe")
			}
			preflightCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := exec.CommandContext(preflightCtx, bash, "-lc", "ansible --version >/dev/null && python3 -c 'import winrm'").Run(); err != nil {
				t.Fatal("Cygwin ansible/pywinrm preflight failed:", err)
			}
		} else if _, err := exec.LookPath("ansible"); err != nil {
			t.Fatal("WinRM scenario requires ansible and pywinrm on the client PATH:", err)
		}
	} else {
		sshClient, err = newSSHClient()
		if err != nil {
			t.Fatal(err)
		}
	}
	// The ProxyCommand in the generated ssh_config runs sailwright-gateway.
	if _, err := exec.LookPath("sailwright-gateway"); err != nil {
		t.Fatalf("client prerequisite sailwright-gateway: %v", err)
	}
	target := os.Getenv("SAILWRIGHT_E2E_TARGET")
	if target == "" {
		target = "ubuntu/server/amd64"
		if windowsGuest {
			target = "windows11//amd64"
		}
	}
	parts := strings.Split(target, "/")
	if len(parts) != 3 || (!windowsGuest && parts[0] != "ubuntu") || (windowsGuest && (parts[0] != "windows11" || parts[1] != "")) {
		t.Fatal("use ubuntu/server/<arch> for SSH or windows11//<arch> for WinRM")
	}
	name := os.Getenv("SAILWRIGHT_E2E_NAME")
	if name == "" {
		name = "agent-access"
	}
	args := map[string]any{"os": parts[0], "type": parts[1], "arch": parts[2], "name": name}
	endpoint, err := client.Discover(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	session, err := client.Connect(ctx, endpoint, &mcp.Implementation{Name: "sailwright-vm-access-e2e", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(callCtx context.Context, name string, input any, output any) error {
		result, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: name, Arguments: input})
		if err != nil {
			return err
		}
		if result.IsError {
			return fmt.Errorf("gateway tool %s failed", name)
		}
		if output != nil {
			data, err := json.Marshal(result.StructuredContent)
			if err != nil {
				return err
			}
			return json.Unmarshal(data, output)
		}
		return nil
	}
	job := func(callCtx context.Context, tool string) error {
		var view gateway.JobView
		if err := call(callCtx, tool, args, &view); err != nil {
			return err
		}
		for !view.Done() {
			select {
			case <-callCtx.Done():
				return callCtx.Err()
			case <-time.After(time.Second):
			}
			if err := call(callCtx, "job_status", map[string]any{"job_id": view.ID}, &view); err != nil {
				return err
			}
		}
		if view.State != gateway.JobSucceeded {
			return fmt.Errorf("%s ended in %s", tool, view.State)
		}
		return nil
	}
	var listing gateway.ListTargetsOutput
	if err := call(ctx, "list_targets", map[string]any{"command": "destroy"}, &listing); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, vm := range listing.Targets {
		if vm.OS == parts[0] && vm.Type == parts[1] && vm.Arch == parts[2] {
			found = true
			if vm.Name == name && vm.Fields["state"] != "missing" {
				t.Fatal("selected VM already exists; refusing to touch it")
			}
		}
	}
	if !found {
		t.Fatal("selected VM is not listed by this host")
	}
	// Register cleanup before create: partial creation must also be cleaned up.
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cleanupCancel()
		if err := job(cleanupCtx, "vm_destroy"); err != nil {
			t.Error("VM cleanup:", err)
			return
		}
		var after gateway.ListTargetsOutput
		if err := call(cleanupCtx, "list_targets", map[string]any{"command": "destroy"}, &after); err != nil {
			t.Error("cleanup listing:", err)
			return
		}
		missing := false
		for _, vm := range after.Targets {
			if vm.OS == parts[0] && vm.Type == parts[1] && vm.Arch == parts[2] {
				missing = vm.Fields["state"] == "missing"
			}
		}
		if !missing {
			t.Error("cleanup did not confirm selected VM is missing")
		} else {
			t.Log("cleanup verified: selected VM is missing")
		}
	}()
	if err := job(ctx, "vm_create"); err != nil {
		t.Fatal(err)
	}
	if err := job(ctx, "vm_start"); err != nil {
		t.Fatal(err)
	}
	var info gateway.VMInfoOutput
	if err := call(ctx, "vm_info", args, &info); err != nil {
		t.Fatal(err)
	}
	if info.Protocol != map[bool]string{false: "ssh", true: "winrm"}[windowsGuest] {
		t.Fatal("unexpected guest protocol:", info.Protocol)
	}
	// IP discovery can finish before sshd/WinRM is listening. Wait through the
	// gateway itself; direct guest probes would bypass the path under test.
	readyCtx, readyCancel := context.WithTimeout(ctx, 3*time.Minute)
	defer readyCancel()
	for {
		conn, err := endpoint.OpenTunnel(readyCtx, parts[0], parts[1], parts[2], info.Port)
		if err == nil {
			conn.Close()
			break
		}
		if readyCtx.Err() != nil {
			t.Fatal("guest service did not become reachable through gateway:", err)
		}
		if !strings.Contains(err.Error(), "HTTP 502: cannot connect to guest service") {
			t.Fatal("guest tunnel readiness:", err)
		}
		select {
		case <-readyCtx.Done():
			t.Fatal("guest tunnel readiness timed out")
		case <-time.After(time.Second):
		}
	}
	refusedPort := 5985
	if windowsGuest {
		refusedPort = 22
	}
	if conn, err := endpoint.OpenTunnel(ctx, parts[0], parts[1], parts[2], refusedPort); err == nil {
		conn.Close()
		t.Fatal("gateway accepted a port for the wrong guest protocol")
	} else if !strings.Contains(err.Error(), "HTTP 403: port does not match the VM's guest protocol") {
		t.Fatal("unexpected tunnel refusal:", err)
	}
	if windowsGuest {
		testWinRMAccess(t, ctx, target, info)
		return
	}
	directory := t.TempDir()
	// The generated config shares connections through a socket in ~/.ssh, which
	// ssh requires to exist.
	if home, err := os.UserHomeDir(); err == nil {
		if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(directory, "ssh_config")
	// Keep ephemeral guest host keys separate from the user's SSH files.
	content := info.SSHConfig + "  StrictHostKeyChecking accept-new\n  UserKnownHostsFile " + filepath.ToSlash(filepath.Join(directory, "known_hosts")) + "\n"
	if err := os.WriteFile(config, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := "sailwright-" + strings.ReplaceAll(target+"/"+name, "/", "-")
	// Close the shared connection before destroying the guest, so audit byte counts
	// and tunnel_closed are flushed and no background SSH client outlives the test.
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd, err := sshClient.command(closeCtx, "ssh", info.Password, "-F", config, "-O", "exit", alias)
		if err == nil {
			_ = cmd.Run()
		}
	}()
	run := func(binary string, argv ...string) string {
		cmd, err := sshClient.command(ctx, binary, info.Password, argv...)
		if err != nil {
			t.Fatal(err)
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			// The password is never part of the output: it reaches ssh through
			// SSHPASS or the askpass helper only.
			t.Fatalf("guest %s failed: %v\n%s", binary, err, output)
		}
		return strings.ReplaceAll(string(output), "\r\n", "\n")
	}
	if output := run("ssh", "-F", config, "-o", "ConnectionAttempts=30", "-o", "ConnectTimeout=5", alias, "uname -a"); !strings.Contains(output, "Linux") {
		t.Fatal("unexpected guest kernel")
	}
	source := filepath.Join(directory, "sailwright-access.txt")
	payload := "sailwright guest access verified\n"
	if err := os.WriteFile(source, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	remote := "/tmp/sailwright-access-" + fmt.Sprint(time.Now().UnixNano()) + ".txt"
	run("scp", "-F", config, source, alias+":"+remote)
	if output := run("ssh", "-F", config, alias, "cat "+remote); output != payload {
		t.Fatal("guest file contents differ")
	}
}

// testWinRMAccess uses the generated inventory unchanged. The CLI listener opens
// authenticated gateway tunnels; Ansible must never connect to the guest IP.
func testWinRMAccess(t *testing.T, ctx context.Context, target string, info gateway.VMInfoOutput) {
	t.Helper()
	if info.Port != 5985 && info.Port != 5986 {
		t.Fatal("unexpected WinRM port")
	}
	port := 15985
	if info.Port == 5986 {
		port = 15986
	}
	// Refuse an occupied local port rather than sending guest credentials to
	// an unrelated listener. This host must be disposable and locally trusted.
	address := fmt.Sprintf("127.0.0.1:%d", port)
	probe, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("WinRM local listener port is unavailable:", err)
	}
	probe.Close()
	listenerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(listenerCtx, "sailwright-gateway", "vm", "tunnel", target, fmt.Sprint(info.Port), "--listen", fmt.Sprintf("127.0.0.1:%d", port))
	// Keep listener output out of logs: inventory and guest credentials are private.
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = command.Wait() }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			t.Fatal("WinRM tunnel listener did not become ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	inventory := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(inventory, []byte(info.AnsibleInventory), 0o600); err != nil {
		t.Fatal(err)
	}
	// win_shell executes inside the Windows guest; a successful image build or
	// TCP connection is insufficient evidence of gateway guest access.
	ansibleArgs := []string{"-m", "ansible.windows.win_shell", "-a", "[System.Environment]::OSVersion.Platform.ToString()", "-e", "ansible_winrm_connection_timeout=120"}
	cmd := exec.CommandContext(ctx, "ansible", append([]string{"all", "-i", inventory}, ansibleArgs...)...)
	if runtime.GOOS == "windows" {
		// Pass data as positional arguments, and convert only the inventory path.
		// Never interpolate credentials, paths or PowerShell into shell source.
		argv := append([]string{"-lc", `inventory=$(cygpath -u "$1") || exit; shift; exec ansible all -i "$inventory" "$@"`, "sailwright-winrm-e2e", inventory}, ansibleArgs...)
		cmd = exec.CommandContext(ctx, os.Getenv("CYGWIN_BASH_PATH"), argv...)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("guest WinRM command failed: %v (output suppressed to protect guest credentials)", err)
	}
	if !strings.Contains(string(output), "Win32NT") {
		t.Fatal("WinRM command did not report a Windows guest")
	}
	t.Log("actual guest WinRM command returned Win32NT through the gateway")
}
