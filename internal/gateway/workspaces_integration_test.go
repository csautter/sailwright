package gateway_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/csautter/dev-alchemy/internal/gateway/gatewaytest"
)

// Built at runtime so secret scanners do not flag a hex literal.
var probeToken = strings.Repeat("0123456789abcdef", 2)

func writeProbe(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, gateway.WorkspaceProbeName), []byte(probeToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func registerArgs(h *gatewaytest.Harness) map[string]any {
	return map[string]any{"agent_path": gatewaytest.AgentWorkspace, "host_path": h.WorkspaceHost, "probe_token": probeToken, "reason": "run my playbook"}
}

// A fresh gateway has no workspaces; the agent registers its own with one operator
// approval and uses it as bundle_root without a gateway restart.
func TestWorkspaceRegisterEnablesBundleRootWithoutRestart(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{Approvals: true, NoWorkspace: true})
	session := h.Connect(t)
	playbook := filepath.Join(h.WorkspaceHost, "site.yml")
	if err := os.WriteFile(playbook, []byte("workspace playbook"), 0o600); err != nil {
		t.Fatal(err)
	}
	runArgs := map[string]any{"args": []string{"provision", "ubuntu", "--playbook", gatewaytest.AgentWorkspace + "/site.yml"}, "bundle_root": gatewaytest.AgentWorkspace}

	if err := call(t, session, "run_cli", runArgs, nil); err == nil || !strings.Contains(err.Error(), "workspace_register") {
		t.Fatalf("unregistered bundle_root must point at workspace_register: %v", err)
	}

	// Without the probe the host path is not proven to be the agent's mount.
	if err := call(t, session, "workspace_register", registerArgs(h), nil); err == nil || !strings.Contains(err.Error(), gateway.WorkspaceProbeName) {
		t.Fatalf("register without probe: %v", err)
	}
	writeProbe(t, h.WorkspaceHost)

	err := call(t, session, "workspace_register", registerArgs(h), nil)
	match := accessRequestID.FindStringSubmatch(errString(err))
	if match == nil {
		t.Fatalf("register must wait for approval: %v", err)
	}
	if again := call(t, session, "workspace_register", registerArgs(h), nil); again == nil || !strings.Contains(again.Error(), match[1]) {
		t.Fatalf("second call did not reuse pending request %s: %v", match[1], again)
	}
	h.WaitForPrompt(t, match[1])
	if console := h.Console.String(); !strings.Contains(console, "sailwright-gateway workspace add "+gatewaytest.AgentWorkspace) || !strings.Contains(console, "AGENT_WORKSPACE") || !strings.Contains(console, "run my playbook") {
		t.Fatalf("approval prompt:\n%s", console)
	}
	h.Approve(t, match[1])

	var out gateway.WorkspaceRegisterOutput
	deadline := time.Now().Add(10 * time.Second)
	for call(t, session, "workspace_register", registerArgs(h), &out) != nil {
		if time.Now().After(deadline) {
			t.Fatal("approval did not register the workspace")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if out.Status != "registered" || out.AgentPath != gatewaytest.AgentWorkspace {
		t.Fatalf("register result: %+v", out)
	}

	var info gateway.GatewayInfo
	mustCall(t, session, "gateway_info", map[string]any{}, &info)
	if len(info.AgentWorkspaces) != 1 || info.AgentWorkspaces[0] != gatewaytest.AgentWorkspace {
		t.Fatalf("gateway_info workspaces: %v", info.AgentWorkspaces)
	}

	var view gateway.JobView
	mustCall(t, session, "run_cli", runArgs, &view)
	if view.Snapshot == nil || view.State != gateway.JobPendingApproval {
		t.Fatalf("bundle_root after registration: %+v", view)
	}
	h.Deny(t, view.ID)
	waitJob(t, session, view.ID)

	// The mapping persists for the next gateway run, outside gateway.yaml.
	data, err := os.ReadFile(filepath.Join(h.StateDir, "agent_workspaces.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Workspaces []gateway.RegisteredWorkspace `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &file); err != nil || len(file.Workspaces) != 1 || file.Workspaces[0].ApprovalID != match[1] {
		t.Fatalf("persisted workspaces: %s %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(h.StateDir, "gateway.yaml")); !os.IsNotExist(err) {
		t.Fatalf("gateway.yaml must not be written: %v", err)
	}

	// A different host path for a registered agent path is refused.
	other := t.TempDir()
	writeProbe(t, other)
	conflict := registerArgs(h)
	conflict["host_path"] = other
	if err := call(t, session, "workspace_register", conflict, nil); err == nil || !strings.Contains(err.Error(), "already mapped") {
		t.Fatalf("conflicting register: %v", err)
	}
}

func TestWorkspaceRegisterDeniedLeavesNoMapping(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{Approvals: true, NoWorkspace: true})
	session := h.Connect(t)
	writeProbe(t, h.WorkspaceHost)
	match := accessRequestID.FindStringSubmatch(errString(call(t, session, "workspace_register", registerArgs(h), nil)))
	if match == nil {
		t.Fatal("register must wait for approval")
	}
	h.Deny(t, match[1])
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := call(t, session, "workspace_register", registerArgs(h), nil)
		if again := accessRequestID.FindStringSubmatch(errString(err)); again != nil && again[1] != match[1] {
			break // the denied request is settled; a new one was opened
		}
		if err == nil {
			t.Fatal("denied workspace was registered")
		}
		if time.Now().After(deadline) {
			t.Fatalf("denial not settled: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var info gateway.GatewayInfo
	mustCall(t, session, "gateway_info", map[string]any{}, &info)
	if len(info.AgentWorkspaces) != 0 {
		t.Fatalf("denied workspace listed: %v", info.AgentWorkspaces)
	}
}

func TestWorkspaceRegisterWithoutConsoleNamesOperatorCommand(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{NoWorkspace: true})
	session := h.Connect(t)
	writeProbe(t, h.WorkspaceHost)
	if err := call(t, session, "workspace_register", registerArgs(h), nil); err == nil || !strings.Contains(err.Error(), "sailwright-gateway workspace add") {
		t.Fatalf("register without console: %v", err)
	}
}

func TestWorkspaceRegisterBypassApprovals(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{AutoApprovals: true, NoWorkspace: true})
	session := h.Connect(t)
	writeProbe(t, h.WorkspaceHost)
	var out gateway.WorkspaceRegisterOutput
	mustCall(t, session, "workspace_register", registerArgs(h), &out)
	if out.Status != "registered" {
		t.Fatalf("auto-approved register: %+v", out)
	}
	if !strings.Contains(h.Console.String(), "auto-approved (--bypass-approvals) workspace_register: sailwright-gateway workspace add") {
		t.Fatalf("console:\n%s", h.Console.String())
	}
}

// The agent registers a directory inside its mount and swaps it for a symlink to
// another host directory while the request waits for the operator. The approval must
// not register the swapped path, and run_cli must not use it.
func TestWorkspaceRegisterRefusesPathSwappedBeforeApproval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory symlinks need extra privileges on Windows")
	}
	h := gatewaytest.Start(t, gatewaytest.Options{Approvals: true, NoWorkspace: true})
	session := h.Connect(t)
	sub := filepath.Join(h.WorkspaceHost, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProbe(t, sub)
	args := map[string]any{"agent_path": gatewaytest.AgentWorkspace + "/sub", "host_path": sub, "probe_token": probeToken}
	match := accessRequestID.FindStringSubmatch(errString(call(t, session, "workspace_register", args, nil)))
	if match == nil {
		t.Fatal("register must wait for approval")
	}
	h.WaitForPrompt(t, match[1])

	secret := t.TempDir()
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, sub); err != nil {
		t.Fatal(err)
	}
	h.Approve(t, match[1])

	deadline := time.Now().Add(10 * time.Second)
	for console := h.Console.String(); !strings.Contains(console, "approved but not registered") || !strings.Contains(console, "not the approved"); console = h.Console.String() {
		if time.Now().After(deadline) {
			t.Fatalf("swapped path was not refused:\n%s", h.Console.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	var info gateway.GatewayInfo
	mustCall(t, session, "gateway_info", map[string]any{}, &info)
	if len(info.AgentWorkspaces) != 0 {
		t.Fatalf("swapped workspace registered: %v", info.AgentWorkspaces)
	}
}
