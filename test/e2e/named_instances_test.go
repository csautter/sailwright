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

// TestNamedVMInstances is opt-in: it creates two independently reachable guests
// from one existing Ubuntu image, then destroys a and verifies b still answers.
func TestNamedVMInstances(t *testing.T) {
	if os.Getenv("SAILWRIGHT_NAMED_VM_E2E") != "1" {
		t.Skip("set SAILWRIGHT_NAMED_VM_E2E=1 with a host gateway and an existing Ubuntu image")
	}
	for _, binary := range []string{"ssh", "sshpass", "sailwright-gateway"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Fatalf("prerequisite %s unavailable", binary)
		}
	}
	target := os.Getenv("SAILWRIGHT_E2E_TARGET")
	if target == "" {
		target = "ubuntu/server/amd64"
	}
	parts := strings.Split(target, "/")
	if len(parts) != 3 || parts[0] != "ubuntu" {
		t.Fatal("select an Ubuntu os/type/arch target")
	}
	endpoint, err := client.Discover(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	session, err := client.Connect(ctx, endpoint, &mcp.Implementation{Name: "sailwright-named-vm-e2e", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(callCtx context.Context, tool string, input, output any) error {
		result, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: tool, Arguments: input})
		if err != nil {
			return err
		}
		if result.IsError {
			return fmt.Errorf("%s failed", tool)
		}
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, output)
	}
	args := func(name string) map[string]any {
		return map[string]any{"os": parts[0], "type": parts[1], "arch": parts[2], "name": name}
	}
	job := func(jobCtx context.Context, tool, name string) error {
		var view gateway.JobView
		if err := call(jobCtx, tool, args(name), &view); err != nil {
			return err
		}
		for !view.Done() {
			select {
			case <-jobCtx.Done():
				return jobCtx.Err()
			case <-time.After(time.Second):
			}
			if err := call(jobCtx, "job_status", map[string]any{"job_id": view.ID}, &view); err != nil {
				return err
			}
		}
		if view.State != gateway.JobSucceeded {
			return fmt.Errorf("%s %s ended in %s", tool, name, view.State)
		}
		return nil
	}
	list := func() ([]gateway.Target, error) {
		var output gateway.ListTargetsOutput
		err := call(ctx, "list_targets", map[string]any{"command": "start"}, &output)
		return output.Targets, err
	}
	matches := func(vm gateway.Target) bool { return vm.OS == parts[0] && vm.Type == parts[1] && vm.Arch == parts[2] }
	before, err := list()
	if err != nil {
		t.Fatal(err)
	}
	defaultState := ""
	for _, vm := range before {
		if matches(vm) {
			if vm.Name == "" {
				defaultState = vm.Fields["state"]
			}
			if vm.Name == "e2e-a" || vm.Name == "e2e-b" {
				t.Fatal("test instance already exists; refusing to touch it")
			}
		}
	}
	if defaultState == "" {
		t.Fatal("target not listed by host")
	}
	var touched []string
	defer func() {
		for _, name := range touched {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Minute)
			err := job(cleanupCtx, "vm_destroy", name)
			cleanupCancel()
			if err != nil {
				t.Error("cleanup:", err)
			}
		}
	}()
	directory := t.TempDir()
	ssh := func(info gateway.VMInfoOutput, name string) string {
		t.Helper()
		config := filepath.Join(directory, name+".ssh")
		text := "Host *\n  ControlPath none\n" + info.SSHConfig + "  StrictHostKeyChecking accept-new\n  UserKnownHostsFile " + filepath.Join(directory, name+".known_hosts") + "\n"
		if err := os.WriteFile(config, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		alias := "sailwright-" + strings.ReplaceAll(target+"/"+name, "/", "-")
		command := exec.CommandContext(ctx, "sshpass", "-e", "ssh", "-F", config, alias, "hostname")
		command.Env = append(os.Environ(), "SSHPASS="+info.Password)
		data, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("guest SSH %s: %v", name, err)
		}
		return strings.TrimSpace(string(data))
	}
	infos := map[string]gateway.VMInfoOutput{}
	hostnames := map[string]string{}
	for _, name := range []string{"e2e-a", "e2e-b"} {
		touched = append(touched, name)
		for _, tool := range []string{"vm_create", "vm_start"} {
			if err := job(ctx, tool, name); err != nil {
				t.Fatal(err)
			}
		}
		var info gateway.VMInfoOutput
		if err := call(ctx, "vm_info", args(name), &info); err != nil {
			t.Fatal(err)
		}
		if info.Name != name {
			t.Fatal("vm_info lost instance name")
		}
		infos[name] = info
		hostnames[name] = ssh(info, name)
	}
	if infos["e2e-a"].Address == infos["e2e-b"].Address || hostnames["e2e-a"] == hostnames["e2e-b"] {
		t.Fatal("instances share a guest address or hostname")
	}
	running, err := list()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"e2e-a", "e2e-b"} {
		found := false
		for _, vm := range running {
			if matches(vm) && vm.Name == name && vm.Fields["state"] == "running" {
				found = true
			}
		}
		if !found {
			t.Fatalf("running instance %s missing from listing", name)
		}
	}
	if err := job(ctx, "vm_destroy", "e2e-a"); err != nil {
		t.Fatal(err)
	}
	if ssh(infos["e2e-b"], "e2e-b") != hostnames["e2e-b"] {
		t.Fatal("destroying a changed b")
	}
	after, err := list()
	if err != nil {
		t.Fatal(err)
	}
	for _, vm := range after {
		if matches(vm) && vm.Name == "" && vm.Fields["state"] != defaultState {
			t.Fatal("default instance state changed")
		}
	}
}
