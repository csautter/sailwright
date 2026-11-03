package gateway_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/csautter/dev-alchemy/internal/gateway/gatewaytest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNamedInstancesRemainIndependentThroughGateway(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{GateForeignVMs: true})
	session := h.Connect(t)
	args := func(name string) map[string]any {
		return map[string]any{"os": "ubuntu", "type": "server", "arch": "amd64", "name": name}
	}
	job := func(tool, name string) {
		t.Helper()
		var view gateway.JobView
		mustCall(t, session, tool, args(name), &view)
		if got := waitJob(t, session, view.ID); got.State != gateway.JobSucceeded {
			t.Fatalf("%s %s: %+v console=%s", tool, name, got, h.Console.String())
		}
	}
	for _, name := range []string{"", "a", "b"} {
		job("vm_create", name)
		job("vm_start", name)
	}
	for _, name := range []string{"a", "b"} {
		var info gateway.VMInfoOutput
		mustCall(t, session, "vm_info", args(name), &info)
		if info.Name != name || !strings.Contains(info.SSHConfig, "ubuntu/server/amd64/"+name+" 22") {
			t.Fatalf("wrong named info: %+v", info)
		}
	}
	var listing gateway.ListTargetsOutput
	mustCall(t, session, "list_targets", map[string]any{"command": "start"}, &listing)
	names := map[string]bool{}
	for _, vm := range listing.Targets {
		if vm.OS == "ubuntu" && vm.Type == "server" && vm.Arch == "amd64" && vm.Fields["state"] == "running" {
			names[vm.Name] = true
		}
	}
	if len(names) != 3 {
		t.Fatalf("missing instances: %+v", listing.Targets)
	}
	job("vm_destroy", "a")
	for _, name := range []string{"", "b"} {
		var info gateway.VMInfoOutput
		mustCall(t, session, "vm_info", args(name), &info)
		if info.Name != name {
			t.Fatal(info.Name)
		}
	}
	if _, err := os.Stat(filepath.Join(h.FakeStateDir, "ubuntu-server-amd64-a")); !os.IsNotExist(err) {
		t.Fatal("destroy left instance a")
	}
	data, err := os.ReadFile(filepath.Join(h.StateDir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"name":"b"`) {
		t.Fatal("named audit entry missing")
	}
	// The default is still foreign after recreation; name b's grant cannot open it.
	job("vm_destroy", "")
	if err := os.WriteFile(filepath.Join(h.FakeStateDir, "ubuntu-server-amd64"), []byte("running"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "vm_info", Arguments: args("")})
	if err == nil && !result.IsError {
		t.Fatal("named ownership leaked to the default")
	}
	job("vm_destroy", "b")
}

func TestGatewayInstanceBudgetAndNameValidation(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{})
	session := h.Connect(t)
	for _, name := range []string{"a", "b", "c", "d"} {
		var view gateway.JobView
		mustCall(t, session, "vm_create", map[string]any{"os": "ubuntu", "name": name}, &view)
		if got := waitJob(t, session, view.ID); got.State != gateway.JobSucceeded {
			t.Fatal(got)
		}
	}
	var view gateway.JobView
	mustCall(t, session, "vm_create", map[string]any{"os": "ubuntu", "name": "e"}, &view)
	if got := waitJob(t, session, view.ID); got.State != gateway.JobFailed || !strings.Contains(got.Error, "instance limit") {
		t.Fatalf("budget: %+v", got)
	}
	before := len(h.Invocations(t))
	for _, name := range []string{"all", "default", "UPPER", "../escape"} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "vm_create", Arguments: map[string]any{"os": "ubuntu", "name": name}})
		if err == nil && !result.IsError {
			t.Fatalf("accepted %q", name)
		}
	}
	// Validation must not dispatch a create job for a rejected name.
	for _, inv := range h.Invocations(t)[before:] {
		if len(inv.Argv) > 1 && inv.Argv[0] == "create" && inv.Argv[1] != "list" {
			data, _ := json.Marshal(inv.Argv)
			t.Fatalf("dispatched invalid name: %s", data)
		}
	}
	mustCall(t, session, "vm_destroy", map[string]any{"os": "ubuntu", "name": "a"}, &view)
	waitJob(t, session, view.ID)
	mustCall(t, session, "vm_create", map[string]any{"os": "ubuntu", "name": "e"}, &view)
	if got := waitJob(t, session, view.ID); got.State != gateway.JobSucceeded {
		t.Fatalf("budget not released after destroy: %+v", got)
	}
}
