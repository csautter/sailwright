package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/csautter/dev-alchemy/internal/gateway/client"
	"github.com/csautter/dev-alchemy/internal/gateway/gatewaytest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func call(t *testing.T, session *mcp.ClientSession, tool string, args any, out any) error {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", tool, err)
	}
	if result.IsError {
		var text []string
		for _, content := range result.Content {
			if tc, ok := content.(*mcp.TextContent); ok {
				text = append(text, tc.Text)
			}
		}
		return errors.New(strings.Join(text, " "))
	}
	if out != nil {
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("decode %s result: %v (%s)", tool, err, data)
		}
	}
	return nil
}

func mustCall(t *testing.T, session *mcp.ClientSession, tool string, args any, out any) {
	t.Helper()
	if err := call(t, session, tool, args, out); err != nil {
		t.Fatalf("%s returned an error: %v", tool, err)
	}
}

func waitJob(t *testing.T, session *mcp.ClientSession, id string) gateway.JobView {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var view gateway.JobView
		mustCall(t, session, "job_status", map[string]any{"job_id": id}, &view)
		if view.Done() {
			return view
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not finish: %+v", id, view)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func jobOutput(t *testing.T, session *mcp.ClientSession, id string) string {
	t.Helper()
	var out gateway.JobOutputResult
	mustCall(t, session, "job_output", map[string]any{"job_id": id}, &out)
	return out.Data
}

func TestGatewayExposesExpectedTools(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{})
	session := h.Connect(t)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	want := []string{"gateway_info", "list_targets", "command_help", "vm_build", "vm_create", "vm_start", "vm_stop", "vm_destroy", "vm_provision", "artifact_pull", "provision_local", "install", "run_cli", "job_status", "job_output", "job_cancel", "job_list", "job_screenshot"}
	for _, name := range want {
		if !strings.Contains(strings.Join(names, ","), name) {
			t.Fatalf("tool %s missing from %v", name, names)
		}
	}
	if len(names) != len(want) {
		t.Fatalf("unexpected tool set %v", names)
	}

	var info gateway.GatewayInfo
	mustCall(t, session, "gateway_info", map[string]any{}, &info)
	if info.BinarySHA256 == "" || info.ApprovalEnabled || len(info.Targets["create"]) == 0 {
		t.Fatalf("unexpected gateway info %+v", info)
	}
}

func TestGatewayRunsSafeJobsWithCanonicalArgvAndCleanEnv(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{})
	session := h.Connect(t)

	var targets gateway.ListTargetsOutput
	mustCall(t, session, "list_targets", map[string]any{"command": "start"}, &targets)
	if len(targets.Targets) == 0 {
		t.Fatal("expected start targets")
	}

	var view gateway.JobView
	mustCall(t, session, "vm_create", map[string]any{"os": "ubuntu"}, &view)
	if view.Tier != "safe" {
		t.Fatalf("unexpected tier %q", view.Tier)
	}
	final := waitJob(t, session, view.ID)
	if final.State != gateway.JobSucceeded || final.ExitCode == nil || *final.ExitCode != 0 {
		t.Fatalf("unexpected final state %+v", final)
	}
	if !strings.Contains(jobOutput(t, session, view.ID), "created") {
		t.Fatal("expected fake CLI output")
	}

	mustCall(t, session, "run_cli", map[string]any{"args": []string{"start", "ubuntu", "-t", "server"}}, &view)
	if view.Tier != "safe" {
		t.Fatalf("run_cli inside the safe set must not need approval, got %+v", view)
	}
	waitJob(t, session, view.ID)

	mustCall(t, session, "list_targets", map[string]any{"command": "start"}, &targets)
	running := false
	for _, target := range targets.Targets {
		if target.OS == "ubuntu" && target.Type == "server" && target.Fields["state"] == "running" {
			running = true
		}
	}
	if !running {
		t.Fatalf("expected ubuntu/server to be running: %+v", targets.Targets)
	}

	var create, start *gatewaytest.Invocation
	invocations := h.Invocations(t)
	for i := range invocations {
		switch invocations[i].Argv[0] {
		case "create":
			if invocations[i].Argv[1] != "list" {
				create = &invocations[i]
			}
		case "start":
			if invocations[i].Argv[1] != "list" {
				start = &invocations[i]
			}
		}
	}
	if create == nil || start == nil {
		t.Fatalf("missing invocations: %+v", invocations)
	}
	if want := []string{"create", "ubuntu", "--arch", "amd64", "--type", "server"}; !reflect.DeepEqual(create.Argv, want) {
		t.Fatalf("create argv %q, want %q", create.Argv, want)
	}
	if want := []string{"start", "ubuntu", "--arch", "amd64", "--type", "server"}; !reflect.DeepEqual(start.Argv, want) {
		t.Fatalf("start argv %q, want %q", start.Argv, want)
	}
	env := strings.Join(create.Env, "\n")
	for _, leaked := range []string{"SSH_AUTH_SOCK", "GITHUB_TOKEN", "host-github-token"} {
		if strings.Contains(env, leaked) {
			t.Fatalf("child env leaks %s", leaked)
		}
	}
	if !strings.Contains(env, "DEV_ALCHEMY_APP_DATA_DIR="+filepath.Join(h.StateDir, "data", "app")) {
		t.Fatalf("child env lacks the managed app data dir:\n%s", env)
	}
	if !strings.HasPrefix(evalPath(t, create.Cwd), evalPath(t, h.StateDir)) {
		t.Fatalf("command ran outside the gateway work dir: %s", create.Cwd)
	}
}

func evalPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

func TestGatewayReportsFailuresBusyAndCancellation(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{})
	session := h.Connect(t)

	var view gateway.JobView
	mustCall(t, session, "vm_create", map[string]any{"os": "failos"}, &view)
	final := waitJob(t, session, view.ID)
	if final.State != gateway.JobFailed || final.ExitCode == nil || *final.ExitCode != 3 {
		t.Fatalf("expected exit code 3, got %+v", final)
	}

	mustCall(t, session, "vm_start", map[string]any{"os": "slowos"}, &view)
	if err := call(t, session, "vm_create", map[string]any{"os": "ubuntu"}, nil); err == nil || !strings.Contains(err.Error(), "another job is active") {
		t.Fatalf("expected busy error, got %v", err)
	}
	started := time.Now()
	mustCall(t, session, "job_cancel", map[string]any{"job_id": view.ID}, nil)
	final = waitJob(t, session, view.ID)
	if final.State != gateway.JobCancelled {
		t.Fatalf("expected cancelled, got %+v", final)
	}
	if time.Since(started) > 20*time.Second {
		t.Fatal("cancellation took too long")
	}

	if err := call(t, session, "vm_create", map[string]any{"os": "macos"}, nil); err == nil {
		t.Fatal("unlisted targets must be rejected")
	}
	if err := call(t, session, "vm_provision", map[string]any{"os": "local"}, nil); err == nil {
		t.Fatal("vm_provision must refuse the local target")
	}
}

func TestGatewayRequiresApprovalConsoleForUnsafeCalls(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{})
	session := h.Connect(t)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"provision_local", map[string]any{"check": true}},
		{"install", map[string]any{}},
		{"run_cli", map[string]any{"args": []string{"provision", "ubuntu", "--", "-e", "x=1"}}},
	} {
		if err := call(t, session, tc.tool, tc.args, nil); err == nil || !strings.Contains(err.Error(), "approval") {
			t.Fatalf("%s without an approval console must fail, got %v", tc.tool, err)
		}
	}
	for _, inv := range h.Invocations(t) {
		if inv.Argv[0] == "install" || (inv.Argv[0] == "provision" && inv.Argv[1] != "list") {
			t.Fatalf("unapproved command was executed: %q", inv.Argv)
		}
	}
}

func TestGatewayApprovalFlow(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{Approvals: true})
	session := h.Connect(t)

	var view gateway.JobView
	mustCall(t, session, "provision_local", map[string]any{"check": true, "reason": "validate the host"}, &view)
	if view.State != gateway.JobPendingApproval || view.Tier != "approval" {
		t.Fatalf("expected pending approval, got %+v", view)
	}
	h.Deny(t, view.ID)
	if final := waitJob(t, session, view.ID); final.State != gateway.JobDenied {
		t.Fatalf("expected denied, got %+v", final)
	}

	mustCall(t, session, "run_cli", map[string]any{"args": []string{"provision", "local", "--check"}, "reason": "SW check"}, &view)
	if !strings.Contains(strings.Join(view.Risk, ","), gateway.RiskHostCode) {
		t.Fatalf("expected HOST-CODE risk, got %v", view.Risk)
	}
	h.Approve(t, view.ID)
	if final := waitJob(t, session, view.ID); final.State != gateway.JobSucceeded {
		t.Fatalf("expected success, got %+v", final)
	}
	if !strings.Contains(h.Console.String(), "agent note (untrusted): SW check") {
		t.Fatalf("console must show the agent note:\n%s", h.Console.String())
	}

	executed := 0
	for _, inv := range h.Invocations(t) {
		if inv.Argv[0] == "provision" && len(inv.Argv) > 1 && inv.Argv[1] == "local" {
			executed++
			if want := []string{"provision", "local", "--check"}; !reflect.DeepEqual(inv.Argv, want) {
				t.Fatalf("approved argv %q, want %q", inv.Argv, want)
			}
		}
	}
	if executed != 1 {
		t.Fatalf("expected exactly the approved call to run, got %d", executed)
	}
}

func TestGatewayRunsApprovedBundleFromSnapshot(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{Approvals: true})
	session := h.Connect(t)

	playbook := filepath.Join(h.WorkspaceHost, "ansible", "site.yml")
	if err := os.MkdirAll(filepath.Dir(playbook), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(playbook, []byte("original playbook"), 0o600); err != nil {
		t.Fatal(err)
	}

	agentRoot := gatewaytest.AgentWorkspace + "/ansible"
	var view gateway.JobView
	mustCall(t, session, "run_cli", map[string]any{
		"args":        []string{"provision", "ubuntu", "--playbook", agentRoot + "/site.yml"},
		"bundle_root": agentRoot,
	}, &view)
	if view.Snapshot == nil || view.Snapshot.Files != 1 {
		t.Fatalf("expected a snapshot, got %+v", view)
	}

	// The agent edits the file after requesting approval; the snapshot must win.
	if err := os.WriteFile(playbook, []byte("tampered playbook"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.Approve(t, view.ID)
	final := waitJob(t, session, view.ID)
	if final.State != gateway.JobSucceeded {
		t.Fatalf("expected success, got %+v", final)
	}
	output := jobOutput(t, session, view.ID)
	if !strings.Contains(output, "PLAYBOOK: original playbook") || strings.Contains(output, "tampered") {
		t.Fatalf("approved run did not use the snapshot:\n%s", output)
	}
	if _, err := os.Stat(view.Snapshot.Dir); !os.IsNotExist(err) {
		t.Fatalf("snapshot must be removed after the job: %v", err)
	}

	if err := call(t, session, "run_cli", map[string]any{"args": []string{"provision", "ubuntu", "--playbook", "/etc/x.yml"}, "bundle_root": "/etc"}, nil); err == nil {
		t.Fatal("bundle_root outside the agent workspace must be rejected")
	}
}

func TestGatewayRedactsSecretsInOutputAndAudit(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{Approvals: true})
	session := h.Connect(t)

	var view gateway.JobView
	mustCall(t, session, "run_cli", map[string]any{"args": []string{"push", "ghcr.io/x/y:z", "--password", "vm-secret-value"}}, &view)
	if strings.Contains(strings.Join(view.Argv, " "), "vm-secret-value") {
		t.Fatalf("argv must be redacted: %q", view.Argv)
	}
	h.Deny(t, view.ID)
	waitJob(t, session, view.ID)
	if strings.Contains(h.Console.String(), "vm-secret-value") {
		t.Fatal("console must not show secrets")
	}
	audit, err := os.ReadFile(filepath.Join(h.StateDir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(audit), "vm-secret-value") || strings.Contains(string(audit), h.Token) {
		t.Fatal("audit log must not contain secrets")
	}
	if !strings.Contains(string(audit), `"decision":"denied"`) {
		t.Fatalf("audit log must record the decision:\n%s", audit)
	}
}

func TestGatewayRejectsClientsWithoutToken(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{})
	wrong := h.TCPEndpoint()
	wrong.Token = strings.Repeat("0", 64)
	if _, err := client.Connect(context.Background(), wrong, &mcp.Implementation{Name: "test"}); err == nil {
		t.Fatal("connecting with a wrong token must fail")
	}
	if h.SocketPath != "" {
		wrongSocket := h.SocketEndpoint()
		wrongSocket.Token = strings.Repeat("0", 64)
		if _, err := client.Connect(context.Background(), wrongSocket, &mcp.Implementation{Name: "test"}); err == nil {
			t.Fatal("the socket must still require the token")
		}
	}
}

func TestGatewayServesTheAgentSocket(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{})
	if h.SocketPath == "" {
		t.Skip("no agent socket on this OS")
	}

	info, err := os.Stat(h.SocketPath)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket must exist with mode 0600: %v %v", info.Mode(), err)
	}
	token, err := os.ReadFile(filepath.Join(h.AgentDir, "token"))
	if err != nil || strings.TrimSpace(string(token)) != h.Token {
		t.Fatalf("agent dir must hold the token copy: %v", err)
	}
	entries, _ := os.ReadDir(h.AgentDir)
	if len(entries) != 2 {
		t.Fatalf("agent dir must only hold the socket and the token, got %d entries", len(entries))
	}

	session := h.ConnectTo(t, h.SocketEndpoint())
	var view gateway.JobView
	mustCall(t, session, "vm_create", map[string]any{"os": "ubuntu"}, &view)
	if final := waitJob(t, session, view.ID); final.State != gateway.JobSucceeded {
		t.Fatalf("unexpected state %+v", final)
	}

	audit, err := os.ReadFile(filepath.Join(h.StateDir, "audit.jsonl"))
	if err != nil || !strings.Contains(string(audit), `"peer":"unix-socket"`) {
		t.Fatalf("audit log must record socket requests: %v\n%s", err, audit)
	}
}

func TestGatewayShowsLiveOutputOnTheConsole(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{FollowOutput: true})
	session := h.Connect(t)
	var view gateway.JobView
	mustCall(t, session, "vm_create", map[string]any{"os": "ubuntu"}, &view)
	waitJob(t, session, view.ID)
	if !strings.Contains(h.Console.String(), "["+view.ID+"] created") {
		t.Fatalf("expected mirrored job output on the console:\n%s", h.Console.String())
	}
}

func TestGatewayPassesSudoPasswordOnlyToTheApprovedJob(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local provisioning on Windows does not use sudo")
	}
	h := gatewaytest.Start(t, gatewaytest.Options{Approvals: true})
	session := h.Connect(t)
	const password = "host-sudo-pass"

	var view gateway.JobView
	mustCall(t, session, "provision_local", map[string]any{"check": true}, &view)
	h.ApproveSudo(t, view.ID, password)
	final := waitJob(t, session, view.ID)
	if final.State != gateway.JobSucceeded || !final.SudoPassword {
		t.Fatalf("unexpected job %+v", final)
	}
	if !strings.Contains(strings.Join(final.Argv, " "), "-- --become-password-file") {
		t.Fatalf("argv must carry the password file: %q", final.Argv)
	}

	output := jobOutput(t, session, view.ID)
	if !strings.Contains(output, "BECOME-PASSWORD: ***") || strings.Contains(output, password) {
		t.Fatalf("the password must reach the CLI but be redacted in output:\n%s", output)
	}
	for _, file := range []string{"audit.jsonl", filepath.Join("logs", view.ID+".log")} {
		data, err := os.ReadFile(filepath.Join(h.StateDir, file))
		if err != nil || strings.Contains(string(data), password) {
			t.Fatalf("%s must not contain the password (%v)", file, err)
		}
	}
	if strings.Contains(h.Console.String(), password) {
		t.Fatal("the console must not show the password")
	}
	staging, _ := os.ReadDir(filepath.Join(h.StateDir, "staging"))
	if len(staging) != 0 {
		t.Fatalf("the password file must be removed after the job, found %d entries", len(staging))
	}

	// A plain approve runs without a password file.
	mustCall(t, session, "provision_local", map[string]any{"check": true}, &view)
	h.Approve(t, view.ID)
	if final := waitJob(t, session, view.ID); final.SudoPassword || strings.Contains(strings.Join(final.Argv, " "), "become-password-file") {
		t.Fatalf("plain approve must not pass a password: %+v", final)
	}
}

// TestJobsRunWithoutTheGatewayTerminal makes sure a job cannot reach the terminal the
// operator types approvals and sudo passwords into.
func TestJobsRunWithoutTheGatewayTerminal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sessions and controlling terminals are Unix concepts")
	}
	pinned, err := gateway.PinBinary(gatewaytest.BuildFakeCLI(t), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	runner := &gateway.Runner{Binary: pinned, Env: os.Environ()}
	if code, err := runner.Run(context.Background(), []string{"__session"}, t.TempDir(), &out); err != nil || code != 0 {
		t.Fatalf("run: %d %v", code, err)
	}
	var pid, sid, pgid int
	var tty string
	if _, err := fmt.Sscanf(strings.TrimSpace(out.String()), "pid=%d sid=%d pgid=%d %s", &pid, &sid, &pgid, &tty); err != nil {
		t.Fatalf("parse %q: %v", out.String(), err)
	}
	if sid != pid || pgid != pid || tty != "no-tty" {
		t.Fatalf("job must lead its own session without a terminal: %q", out.String())
	}
}
