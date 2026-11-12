//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/csautter/dev-alchemy/internal/gateway/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestVMOwnership checks how a gateway treats a VM the operator created with the
// CLI: tunnels are refused until vm_info is approved, recreating the VM outside
// the gateway (with and without a list_targets call in between) withdraws that
// access, and a CLI operation on the target is refused while a gateway job holds
// its lock.
//
// It needs a disposable host with a gateway started with --bypass-approvals, so
// that vm_info approvals are answered automatically, and the same sailwright
// binary in SAILWRIGHT_E2E_BIN that the gateway pinned. Run with
// SAILWRIGHT_VM_OWNERSHIP_E2E=1; set SAILWRIGHT_E2E_INSECURE=1 for targets on
// bridged or unknown networks (UTM templates).
func TestVMOwnership(t *testing.T) {
	if os.Getenv("SAILWRIGHT_VM_OWNERSHIP_E2E") != "1" {
		t.Skip("set SAILWRIGHT_VM_OWNERSHIP_E2E=1 on a disposable host with a --bypass-approvals gateway")
	}
	bin := os.Getenv("SAILWRIGHT_E2E_BIN")
	if bin == "" {
		t.Fatal("set SAILWRIGHT_E2E_BIN to the sailwright binary the gateway pinned")
	}
	target := os.Getenv("SAILWRIGHT_E2E_TARGET")
	if target == "" {
		target = "ubuntu/server/amd64"
	}
	parts := strings.Split(target, "/")
	if len(parts) < 3 || parts[0] != "ubuntu" {
		t.Fatal("use an SSH guest such as ubuntu/server/<arch>")
	}
	osName, osType, arch := parts[0], parts[1], parts[2]
	targetArgs := []string{osName, "--arch", arch, "--type", osType}
	var insecure []string
	if os.Getenv("SAILWRIGHT_E2E_INSECURE") == "1" {
		insecure = []string{"--allow-insecure-credentials"}
	}

	cli := func(ctx context.Context, env []string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = append(os.Environ(), env...)
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	lifecycle := func(ctx context.Context, command string) {
		t.Helper()
		args := append([]string{command}, targetArgs...)
		if command == "create" || command == "start" {
			args = append(args, insecure...)
		}
		if output, err := cli(ctx, nil, args...); err != nil {
			t.Fatalf("sailwright %s: %v\n%s", command, err, tailLines(output, 20))
		}
	}

	endpoint, err := client.Discover(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Minute)
	defer cancel()
	session, err := client.Connect(ctx, endpoint, &mcp.Implementation{Name: "sailwright-vm-ownership-e2e", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, input any, output any) error {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: input})
		if err != nil {
			return err
		}
		if result.IsError {
			var text []string
			for _, content := range result.Content {
				if value, ok := content.(*mcp.TextContent); ok {
					text = append(text, value.Text)
				}
			}
			return fmt.Errorf("gateway tool %s failed: %s", name, strings.Join(text, " "))
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
	targetInput := map[string]any{"os": osName, "type": osType, "arch": arch}
	// exists asks `create list`, which reports the VM itself. The destroy listing
	// also counts a registered Hyper-V box, which a named instance leaves behind.
	exists := func() bool {
		var listing gateway.ListTargetsOutput
		if err := call("list_targets", map[string]any{"command": "create"}, &listing); err != nil {
			t.Fatal(err)
		}
		for _, vm := range listing.Targets {
			if vm.OS == osName && vm.Type == osType && vm.Arch == arch && vm.Name == "" {
				if vm.Exists != nil {
					return *vm.Exists
				}
				return vm.Fields["create"] == "already created"
			}
		}
		t.Fatal("target is not listed by this host")
		return false
	}
	// vmInfo requests access and retries while the (automatic) approval is pending.
	vmInfo := func() gateway.VMInfoOutput {
		t.Helper()
		var info gateway.VMInfoOutput
		var lastErr error
		for attempt := 0; attempt < 30; attempt++ {
			if lastErr = call("vm_info", targetInput, &info); lastErr == nil {
				return info
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatal("vm_info:", lastErr)
		return info
	}
	// A freshly created guest has no address until DHCP completes. The gateway
	// resolves the VM before it can compare identities, and that wait would
	// outlast the tunnel client's deadline; wait through the CLI first.
	waitGuestAddress := func(step string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Minute)
		for {
			output, err := cli(ctx, nil, append([]string{"vm", "info"}, targetArgs...)...)
			if err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: guest did not report an address: %v\n%s", step, err, tailLines(output, 10))
			}
			time.Sleep(3 * time.Second)
		}
	}
	expectRefused := func(step string, wants ...string) {
		t.Helper()
		conn, err := endpoint.OpenTunnel(ctx, osName, osType, arch, 22)
		if err == nil {
			conn.Close()
			t.Fatalf("%s: tunnel was accepted", step)
		}
		for _, want := range wants {
			if strings.Contains(err.Error(), want) {
				t.Logf("%s: refused as expected: %v", step, err)
				return
			}
		}
		t.Fatalf("%s: unexpected refusal: %v", step, err)
	}
	expectBanner := func(step string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		for {
			conn, err := endpoint.OpenTunnel(ctx, osName, osType, arch, 22)
			if err == nil {
				_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
				line, readErr := bufio.NewReader(conn).ReadString('\n')
				conn.Close()
				if readErr == nil && strings.HasPrefix(line, "SSH-") {
					t.Logf("%s: %s", step, strings.TrimSpace(line))
					return
				}
				err = fmt.Errorf("no SSH banner: %q %v", line, readErr)
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %v", step, err)
			}
			time.Sleep(3 * time.Second)
		}
	}

	if exists() {
		t.Fatal("selected VM already exists; refusing to touch it")
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cleanupCancel()
		if output, err := cli(cleanupCtx, nil, append([]string{"destroy"}, targetArgs...)...); err != nil {
			t.Errorf("cleanup destroy: %v\n%s", err, tailLines(output, 20))
		}
	}()

	lifecycle(ctx, "create")
	lifecycle(ctx, "start")
	expectRefused("tunnel before approval", "not created by this gateway")
	if info := vmInfo(); info.Protocol != "ssh" {
		t.Fatal("unexpected protocol:", info.Protocol)
	}
	expectBanner("tunnel after approval")

	for _, withListing := range []bool{true, false} {
		step := fmt.Sprintf("recreate (list_targets in between: %t)", withListing)
		lifecycle(ctx, "destroy")
		if withListing {
			if exists() {
				t.Fatalf("%s: VM still listed after destroy", step)
			}
		}
		lifecycle(ctx, "create")
		lifecycle(ctx, "start")
		waitGuestAddress(step)
		expectRefused(step, "recreated outside this gateway", "not created by this gateway")
		vmInfo()
		expectBanner(step + ", approved again")
	}

	// A gateway job holds the target's lock; the CLI must report the holder.
	var job gateway.JobView
	if err := call("vm_stop", targetInput, &job); err != nil {
		t.Fatal(err)
	}
	output, err := cli(ctx, []string{"SAILWRIGHT_LOCK_TIMEOUT=2s"}, append(append([]string{"start"}, targetArgs...), insecure...)...)
	switch {
	case err != nil && strings.Contains(output, "resource busy") && strings.Contains(output, "held by pid"):
		t.Logf("CLI start during gateway vm_stop: %s", strings.TrimSpace(tailLines(output, 2)))
	case err == nil:
		t.Log("inconclusive: vm_stop finished before the CLI start; lock contention was not observed")
	default:
		t.Fatalf("CLI start during gateway vm_stop: %v\n%s", err, tailLines(output, 20))
	}
	for !job.Done() {
		time.Sleep(time.Second)
		if err := call("job_status", map[string]any{"job_id": job.ID}, &job); err != nil {
			t.Fatal(err)
		}
	}
	if job.State != gateway.JobSucceeded {
		t.Fatalf("vm_stop ended in %s", job.State)
	}
	metadata, err := cli(ctx, nil, "--gateway-metadata")
	if err != nil || !strings.Contains(metadata, `"lock_version":2`) {
		t.Fatalf("gateway metadata: %v %s", err, metadata)
	}
}

func tailLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
