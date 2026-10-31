//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/csautter/dev-alchemy/internal/gateway/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestVMAccess exercises the actual container SSH/scp clients and gateway transport.
// It is opt-in because it creates and destroys the selected managed VM.
func TestVMAccess(t *testing.T) {
	if os.Getenv("SAILWRIGHT_VM_ACCESS_E2E") != "1" {
		t.Skip("set SAILWRIGHT_VM_ACCESS_E2E=1 to test a disposable Ubuntu VM over the gateway")
	}
	for _, binary := range []string{"ssh", "scp", "sshpass", "sailwright-gateway"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Fatalf("container prerequisite %s: %v", binary, err)
		}
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
	config := filepath.Join(directory, "ssh_config")
	// Keep ephemeral guest host keys separate from the user's SSH files.
	content := info.SSHConfig + "  StrictHostKeyChecking accept-new\n  UserKnownHostsFile " + filepath.Join(directory, "known_hosts") + "\n"
	if err := os.WriteFile(config, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := "sailwright-" + strings.ReplaceAll(target, "/", "-")
	run := func(binary string, argv ...string) string {
		cmd := exec.CommandContext(ctx, "sshpass", append([]string{"-e", binary}, argv...)...)
		cmd.Env = append(os.Environ(), "SSHPASS="+info.Password)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("guest %s failed: %v", binary, err)
		}
		return string(output)
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
