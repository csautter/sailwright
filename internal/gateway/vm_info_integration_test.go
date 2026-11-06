package gateway_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/csautter/dev-alchemy/internal/gateway/gatewaytest"
)

func TestVMInfoUsesPinnedCLIAndReturnsControllerConfig(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{})
	session := h.Connect(t)
	for _, osName := range []string{"ubuntu", "windows11"} {
		args := map[string]any{"os": osName, "arch": "amd64"}
		if osName == "ubuntu" {
			args["type"] = "server"
		}
		var view gateway.JobView
		mustCall(t, session, "vm_create", args, &view)
		if got := waitJob(t, session, view.ID); got.State != gateway.JobSucceeded {
			t.Fatalf("create: %+v", got)
		}
		mustCall(t, session, "vm_start", args, &view)
		if got := waitJob(t, session, view.ID); got.State != gateway.JobSucceeded {
			t.Fatalf("start: %+v", got)
		}
		var info gateway.VMInfoOutput
		mustCall(t, session, "vm_info", args, &info)
		if info.Password != "vm-secret-value" || !strings.Contains(info.AnsibleInventory, "ansible_password") {
			t.Fatalf("guest config missing: %+v", info)
		}
		if osName == "ubuntu" && !strings.Contains(info.SSHConfig, "ProxyCommand sailwright-gateway vm tunnel ubuntu/server/amd64 22") {
			t.Fatal(info.SSHConfig)
		}
		if osName == "windows11" && !strings.Contains(info.ListenCommand, "--listen 127.0.0.1:15985") {
			t.Fatal(info.ListenCommand)
		}
		// Repeated info uses the short cache; a tunnel must bypass it after stop.
		mustCall(t, session, "vm_info", args, &info)
		mustCall(t, session, "vm_stop", args, &view)
		if got := waitJob(t, session, view.ID); got.State != gateway.JobSucceeded {
			t.Fatalf("stop: %+v", got)
		}
		typ := ""
		port := 5985
		if osName == "ubuntu" {
			typ, port = "server", 22
		}
		conn, err := h.TCPEndpoint().OpenTunnel(context.Background(), osName, typ, "amd64", port)
		if err == nil {
			conn.Close()
			t.Fatal("stopped VM reached through cached info")
		}
		if !strings.Contains(err.Error(), "404") {
			t.Fatalf("expected stopped VM refusal, got %v", err)
		}

	}
	audit, err := os.ReadFile(filepath.Join(h.StateDir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(audit), "vm-secret-value") {
		t.Fatal("password leaked to audit")
	}
	count := 0
	for _, invocation := range h.Invocations(t) {
		if len(invocation.Argv) > 1 && invocation.Argv[0] == "vm" && invocation.Argv[1] == "info" {
			count++
		}
	}
	if count != 4 {
		t.Fatalf("expected pinned CLI VM queries, got %d", count)
	}
}
