package workflowcheck

import (
	"slices"
	"testing"
)

const windowsBuildWorkflow = "test-build-windows.yml"

// Jobs that call the runner broker and therefore need a GitHub OIDC token.
var windowsBrokerJobs = []string{
	"provision-hyperv-runner",
	"provision-virtualbox-runner",
	"watch-hyperv-runner-queue",
	"cleanup-hyperv",
	"cleanup-virtualbox",
}

// The build jobs run pull request code on the self-hosted runner VM. They must
// not be able to request an OIDC token for the runner broker identity.
func TestWindowsBuildWorkflowGrantsIDTokenOnlyToBrokerJobs(t *testing.T) {
	t.Parallel()

	wf := loadWorkflow(t, windowsBuildWorkflow)
	if wf.Permissions["id-token"] != "" {
		t.Error("workflow must not grant id-token at workflow scope")
	}
	for _, jobID := range sortedKeys(wf.Jobs) {
		j := wf.Jobs[jobID]
		isBrokerJob := slices.Contains(windowsBrokerJobs, jobID)
		if isBrokerJob && j.Permissions["id-token"] != "write" {
			t.Errorf("job %q: calls the runner broker and needs id-token: write", jobID)
		}
		if !isBrokerJob && j.Permissions["id-token"] != "" {
			t.Errorf("job %q: must not be granted id-token", jobID)
		}
	}
}

// The broker identity has no Azure RBAC role, so its logins must not select a
// subscription. A subscription-scoped login would only work again if someone
// re-added a role assignment for the identity.
func TestWindowsBuildWorkflowLogsInWithoutSubscription(t *testing.T) {
	t.Parallel()

	wf := loadWorkflow(t, windowsBuildWorkflow)
	logins := 0
	for _, jobID := range sortedKeys(wf.Jobs) {
		for _, s := range wf.Jobs[jobID].stepsUsing("azure/login") {
			logins++
			if s.With["subscription-id"] != "" {
				t.Errorf("job %q step %q: broker login must not set subscription-id", jobID, s.Name)
			}
			if s.With["allow-no-subscriptions"] != "true" {
				t.Errorf("job %q step %q: broker login needs allow-no-subscriptions: true", jobID, s.Name)
			}
		}
	}
	if logins == 0 {
		t.Fatal("expected azure/login steps in the Windows build workflow")
	}
}
