//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
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
	if os.Getenv("SAILWRIGHT_VM_ACCESS_E2E") != "1" {
		t.Skip("set SAILWRIGHT_VM_ACCESS_E2E=1 to test a disposable Ubuntu VM over the gateway")
	}
	sshClient, err := newSSHClient()
	if err != nil {
		t.Fatal(err)
	}
	// The ProxyCommand in the generated ssh_config runs sailwright-gateway.
	if _, err := exec.LookPath("sailwright-gateway"); err != nil {
		t.Fatalf("client prerequisite sailwright-gateway: %v", err)
	}
	target := os.Getenv("SAILWRIGHT_E2E_TARGET")
	if target == "" {
		target = "ubuntu/server/amd64"
	}
	parts := strings.Split(target, "/")
	if len(parts) != 3 || parts[0] != "ubuntu" {
		t.Fatal("VM access scenario needs an Ubuntu os/type/arch target")
	}
	args := map[string]any{"os": parts[0], "type": parts[1], "arch": parts[2]}
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
			if vm.Fields["state"] != "missing" {
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
	alias := "sailwright-" + strings.ReplaceAll(target, "/", "-")
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
	if output := run("ssh", "-F", config, alias, "uname -a"); !strings.Contains(output, "Linux") {
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
