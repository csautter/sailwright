package workflowcheck

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// Keep the costly real-guest test wired in: the normal Go suite skips it.
func TestGatewayGuestAccessWorkflow(t *testing.T) {
	wf := loadWorkflow(t, "test-gateway-guest-access.yml")
	for event, paths := range map[string][]string{"push": wf.On.Push.Paths, "pull_request": wf.On.PullRequest.Paths} {
		for _, path := range []string{"internal/gateway/**", "cmd/**", "pkg/deploy/**", "test/e2e/**", "scripts/ci/*gateway*"} {
			if !slices.Contains(paths, path) {
				t.Errorf("%s must cover %s", event, path)
			}
		}
	}
	job := wf.job(t, "linux-libvirt")
	if !slices.Equal(job.RunsOn, stringList{"ubuntu-24.04"}) {
		t.Fatal("guest access must use a disposable hosted Linux runner")
	}
	var runs, artifacts bool
	for _, step := range job.Steps {
		runs = runs || strings.Contains(step.Run, "bash scripts/ci/gateway-guest-access-linux.sh")
		artifacts = artifacts || strings.HasPrefix(step.Uses, "actions/upload-artifact@") && step.If == "always()" && step.With["path"] == "artifact/gateway-guest-access/"
	}
	if !runs || !artifacts {
		t.Fatal("workflow must execute guest access and always upload cleanup/audit evidence")
	}
	script, err := os.ReadFile(repoPath(t, "scripts/ci/gateway-guest-access-linux.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"SAILWRIGHT_VM_ACCESS_E2E=1", "-tags e2e", "-run '^TestVMAccess$'", "trap cleanup EXIT", "verify-gateway-guest-audit.py"} {
		if !strings.Contains(string(script), required) {
			t.Errorf("runner script must include %s", required)
		}
	}
	if strings.Contains(string(script), "t.Skip") || strings.Contains(string(script), "continue-on-error") {
		t.Fatal("real guest failures must not be silently skipped")
	}
}
