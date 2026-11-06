package gateway_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/csautter/dev-alchemy/internal/gateway/gatewaytest"
)

var accessRequestID = regexp.MustCompile(`request ([0-9a-f]{8})`)

func TestForeignVMsNeedOperatorApproval(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{Approvals: true, GateForeignVMs: true})
	session := h.Connect(t)
	ubuntu := map[string]any{"os": "ubuntu", "type": "server", "arch": "amd64"}
	windows := map[string]any{"os": "windows11", "arch": "amd64"}

	// The operator created and started this VM with their own CLI.
	if err := os.WriteFile(filepath.Join(h.FakeStateDir, "ubuntu-server-amd64"), []byte("running"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.Gateway.RefreshTargets(context.Background())

	// Lifecycle jobs on the operator's VM wait for approval instead of running.
	var view gateway.JobView
	mustCall(t, session, "vm_stop", ubuntu, &view)
	if view.State != gateway.JobPendingApproval || view.Tier != "approval" {
		t.Fatalf("foreign stop was not gated: %+v", view)
	}
	h.Deny(t, view.ID)
	if got := waitJob(t, session, view.ID); got.State != gateway.JobDenied {
		t.Fatalf("denied stop: %+v", got)
	}

	// Credentials and tunnels need a separate, explicit grant.
	if _, err := h.TCPEndpoint().OpenTunnel(context.Background(), "ubuntu", "server", "amd64", 22); err == nil || !strings.Contains(err.Error(), "not created by this gateway") {
		t.Fatalf("tunnel to foreign VM: %v", err)
	}
	err := call(t, session, "vm_info", ubuntu, nil)
	match := accessRequestID.FindStringSubmatch(errString(err))
	if match == nil || !strings.Contains(err.Error(), "not created by this gateway") {
		t.Fatalf("vm_info on foreign VM: %v", err)
	}
	if again := call(t, session, "vm_info", ubuntu, nil); again == nil || !strings.Contains(again.Error(), match[1]) {
		t.Fatalf("second call did not reuse pending request %s: %v", match[1], again)
	}
	h.Approve(t, match[1])
	deadline := time.Now().Add(10 * time.Second)
	for call(t, session, "vm_info", ubuntu, nil) != nil {
		if time.Now().After(deadline) {
			t.Fatal("approval did not grant access")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var info gateway.VMInfoOutput
	mustCall(t, session, "vm_info", ubuntu, &info)
	if info.Password != "vm-secret-value" {
		t.Fatalf("granted vm_info: %+v", info)
	}
	mustCall(t, session, "vm_stop", ubuntu, &view)
	if view.State == gateway.JobPendingApproval {
		t.Fatal("granted VM still needs approval for stop")
	}
	waitJob(t, session, view.ID)

	// VMs the gateway creates are the agent's without asking, across restarts.
	mustCall(t, session, "vm_create", windows, &view)
	if got := waitJob(t, session, view.ID); got.State != gateway.JobSucceeded {
		t.Fatalf("create: %+v", got)
	}
	mustCall(t, session, "vm_start", windows, &view)
	if view.State == gateway.JobPendingApproval {
		t.Fatal("gateway-created VM needs approval")
	}
	waitJob(t, session, view.ID)
	mustCall(t, session, "vm_info", windows, &info)
	ownership := filepath.Join(h.StateDir, "owned_vms.json")
	if data, err := os.ReadFile(ownership); err != nil || !strings.Contains(string(data), `"windows11//amd64"`) || strings.Contains(string(data), "ubuntu") {
		t.Fatalf("ownership record: %s %v", data, err)
	}

	// The operator destroying the VM outside the gateway ends ownership, so a VM
	// they create later at the same target is theirs again.
	if err := os.Remove(filepath.Join(h.FakeStateDir, "windows11--amd64")); err != nil {
		t.Fatal(err)
	}
	h.Gateway.RefreshTargets(context.Background())
	if data, err := os.ReadFile(ownership); err != nil || strings.Contains(string(data), "windows11") {
		t.Fatalf("stale ownership kept: %s %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(h.FakeStateDir, "windows11--amd64"), []byte("running"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustCall(t, session, "vm_stop", windows, &view)
	if view.State != gateway.JobPendingApproval {
		t.Fatalf("recreated operator VM was not gated: %+v", view)
	}
	h.Deny(t, view.ID)
	waitJob(t, session, view.ID)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// A VM the operator destroys and recreates under the same name before the next
// listing is a new instance; its id differs from the one the gateway recorded.
func TestRecreatedVMIsNotTakenForTheGateways(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{Approvals: true, GateForeignVMs: true})
	session := h.Connect(t)
	ubuntu := map[string]any{"os": "ubuntu", "type": "server", "arch": "amd64"}

	var view gateway.JobView
	mustCall(t, session, "vm_create", ubuntu, &view)
	if got := waitJob(t, session, view.ID); got.State != gateway.JobSucceeded {
		t.Fatalf("create: %+v", got)
	}
	instance, err := os.ReadFile(filepath.Join(h.FakeStateDir, "ubuntu-server-amd64.instance"))
	if err != nil {
		t.Fatal(err)
	}
	ownership := filepath.Join(h.StateDir, "owned_vms.json")
	if data, err := os.ReadFile(ownership); err != nil || !strings.Contains(string(data), `"instance_id": "`+string(instance)+`"`) {
		t.Fatalf("create did not record the instance id %s: %s %v", instance, data, err)
	}
	mustCall(t, session, "vm_start", ubuntu, &view)
	if view.State == gateway.JobPendingApproval {
		t.Fatal("gateway-created VM needs approval")
	}
	waitJob(t, session, view.ID)
	mustCall(t, session, "vm_info", ubuntu, nil)

	// Out of band, with no listing in between: same name, new instance.
	if err := os.WriteFile(filepath.Join(h.FakeStateDir, "ubuntu-server-amd64.instance"), []byte("operator-instance"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Lifecycle jobs check the instance right before running unapproved; the tool
	// call returns before that hypervisor query.
	mustCall(t, session, "vm_stop", ubuntu, &view)
	h.Deny(t, view.ID)
	if got := waitJob(t, session, view.ID); got.State != gateway.JobDenied || got.Tier != "approval" {
		t.Fatalf("recreated VM was not gated: %+v", got)
	}
	if data, err := os.ReadFile(ownership); err != nil || strings.Contains(string(data), "ubuntu") {
		t.Fatalf("ownership of the destroyed instance kept: %s %v", data, err)
	}
	if _, err := h.TCPEndpoint().OpenTunnel(context.Background(), "ubuntu", "server", "amd64", 22); err == nil || !strings.Contains(err.Error(), "not created by this gateway") {
		t.Fatalf("tunnel to recreated VM: %v", err)
	}
	if err := call(t, session, "vm_info", ubuntu, nil); err == nil || !strings.Contains(err.Error(), "not created by this gateway") {
		t.Fatalf("vm_info on recreated VM: %v", err)
	}
}

// A slow hypervisor query (Hyper-V takes tens of seconds) must not hold up the tool
// call: the job confirms the instance itself and can be cancelled meanwhile.
func TestLifecycleCallsDoNotWaitForInstanceCheck(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{Approvals: true, GateForeignVMs: true})
	session := h.Connect(t)
	ubuntu := map[string]any{"os": "ubuntu", "type": "server", "arch": "amd64"}
	var view gateway.JobView
	mustCall(t, session, "vm_create", ubuntu, &view)
	if got := waitJob(t, session, view.ID); got.State != gateway.JobSucceeded {
		t.Fatalf("create: %+v", got)
	}
	if err := os.WriteFile(filepath.Join(h.FakeStateDir, "identity-delay"), []byte("1m"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	mustCall(t, session, "vm_stop", ubuntu, &view)
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("vm_stop waited %s for the instance check", elapsed)
	}
	if view.State != gateway.JobRunning || view.Tier != "safe" {
		t.Fatalf("owned VM stop: %+v", view)
	}
	var cancelled gateway.JobView
	mustCall(t, session, "job_cancel", map[string]any{"job_id": view.ID}, &cancelled)
	if got := waitJob(t, session, view.ID); got.State != gateway.JobCancelled {
		t.Fatalf("cancel during the instance check: %+v", got)
	}
	if time.Since(started) > 30*time.Second {
		t.Fatal("cancel did not stop the instance check")
	}
}

// Tunnels and vm_info check the instance themselves, without a lifecycle job first.
func TestTunnelRefusesRecreatedVM(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{Approvals: true, GateForeignVMs: true})
	session := h.Connect(t)
	ubuntu := map[string]any{"os": "ubuntu", "type": "server", "arch": "amd64"}
	var view gateway.JobView
	for _, tool := range []string{"vm_create", "vm_start"} {
		mustCall(t, session, tool, ubuntu, &view)
		if got := waitJob(t, session, view.ID); got.State != gateway.JobSucceeded {
			t.Fatalf("%s: %+v", tool, got)
		}
	}
	if err := os.WriteFile(filepath.Join(h.FakeStateDir, "ubuntu-server-amd64.instance"), []byte("operator-instance"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.TCPEndpoint().OpenTunnel(context.Background(), "ubuntu", "server", "amd64", 22); err == nil || !strings.Contains(err.Error(), "recreated outside this gateway") {
		t.Fatalf("tunnel to recreated VM: %v", err)
	}
	if err := call(t, session, "vm_info", ubuntu, nil); err == nil || !strings.Contains(err.Error(), "not created by this gateway") {
		t.Fatalf("vm_info on recreated VM: %v", err)
	}
}

// A create that fails after deployment began leaves a VM the agent must be able to
// clean up; a create refused for an existing VM claims nothing.
func TestFailedCreateKeepsOwnershipOnlyAfterDeployBegan(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{Approvals: true, GateForeignVMs: true})
	session := h.Connect(t)
	windows := map[string]any{"os": "windows11", "arch": "amd64"}
	ubuntu := map[string]any{"os": "ubuntu", "type": "server", "arch": "amd64"}

	if err := os.WriteFile(filepath.Join(h.FakeStateDir, "create-fails-after-start"), []byte("yes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var view gateway.JobView
	mustCall(t, session, "vm_create", windows, &view)
	if got := waitJob(t, session, view.ID); got.State != gateway.JobFailed {
		t.Fatalf("create should fail: %+v", got)
	}
	mustCall(t, session, "vm_destroy", windows, &view)
	if view.State == gateway.JobPendingApproval {
		t.Fatal("the partly created VM of a failed gateway create needs approval to destroy")
	}
	if got := waitJob(t, session, view.ID); got.State != gateway.JobSucceeded {
		t.Fatalf("destroy: %+v", got)
	}
	if err := os.Remove(filepath.Join(h.FakeStateDir, "create-fails-after-start")); err != nil {
		t.Fatal(err)
	}

	// The operator's VM exists: create refuses before deployment and claims nothing.
	if err := os.WriteFile(filepath.Join(h.FakeStateDir, "ubuntu-server-amd64"), []byte("stopped"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustCall(t, session, "vm_create", ubuntu, &view)
	if got := waitJob(t, session, view.ID); got.State != gateway.JobFailed {
		t.Fatalf("create of an existing VM: %+v", got)
	}
	mustCall(t, session, "vm_destroy", ubuntu, &view)
	if view.State != gateway.JobPendingApproval {
		t.Fatalf("refused create made the operator's VM the gateway's: %+v", view)
	}
	h.Deny(t, view.ID)
	waitJob(t, session, view.ID)
	if entries, err := filepath.Glob(filepath.Join(h.StateDir, "staging", "create-*.started")); err != nil || len(entries) != 0 {
		t.Fatalf("create markers left behind: %v %v", entries, err)
	}
}

// Before approval, lookup failures for a VM that may be the operator's stay generic.
func TestVMInfoHidesForeignLookupDetails(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{Approvals: true, GateForeignVMs: true})
	session := h.Connect(t)
	if err := os.WriteFile(filepath.Join(h.FakeStateDir, "ubuntu-server-amd64"), []byte("stopped"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := call(t, session, "vm_info", map[string]any{"os": "ubuntu", "type": "server", "arch": "amd64"}, nil)
	if err == nil || !strings.Contains(err.Error(), "is not running or cannot be inspected") || strings.Contains(err.Error(), "exited with") {
		t.Fatalf("foreign lookup failure: %v", err)
	}
}

func TestBypassApprovalsApprovesJobsAndForeignVMs(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{AutoApprovals: true, GateForeignVMs: true})
	session := h.Connect(t)
	ubuntu := map[string]any{"os": "ubuntu", "type": "server", "arch": "amd64"}

	// A VM the operator created is granted without a human.
	if err := os.WriteFile(filepath.Join(h.FakeStateDir, "ubuntu-server-amd64"), []byte("running"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.Gateway.RefreshTargets(context.Background())
	var info gateway.VMInfoOutput
	mustCall(t, session, "vm_info", ubuntu, &info)
	if info.Password != "vm-secret-value" {
		t.Fatalf("auto-approved vm_info: %+v", info)
	}

	// Approval-tier jobs run without waiting for "approve <id>".
	var view gateway.JobView
	mustCall(t, session, "install", map[string]any{"hyperv": true, "vagrant": true}, &view)
	if got := waitJob(t, session, view.ID); got.State != gateway.JobSucceeded {
		t.Fatalf("auto-approved install: %+v", got)
	}
	var argv []string
	for _, inv := range h.Invocations(t) {
		if len(inv.Argv) > 0 && inv.Argv[0] == "install" {
			argv = inv.Argv
		}
	}
	if strings.Join(argv, " ") != "install --hyperv --vagrant" {
		t.Fatalf("install argv: %v", argv)
	}

	audit, err := os.ReadFile(filepath.Join(h.StateDir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{`"event":"vm_access_decision"`, `"event":"approval_decision"`} {
		found := false
		for _, line := range strings.Split(string(audit), "\n") {
			if strings.Contains(line, event) && strings.Contains(line, `"auto_approved":true`) && strings.Contains(line, `"decision":"approved"`) {
				found = true
			}
		}
		if !found {
			t.Fatalf("audit has no auto-approved %s:\n%s", event, audit)
		}
	}
	if !strings.Contains(h.Console.String(), "auto-approved (--bypass-approvals)") {
		t.Fatalf("console does not show auto approvals:\n%s", h.Console.String())
	}
}
