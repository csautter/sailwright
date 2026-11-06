package workflowcheck

import (
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
	validate := wf.job(t, "validate")
	for _, step := range validate.Steps {
		if strings.HasPrefix(step.Uses, "actions/setup-go@") || strings.Contains(step.Run, "go test") {
			t.Fatal("ordinary PR validation must not install Go or boot guests")
		}
	}
	job := wf.job(t, "linux-libvirt")
	if !slices.Contains(job.Needs, "validate") {
		t.Fatal("real guest work must wait for cheap safety checks")
	}
	wantGate := "github.event_name == 'schedule' || github.event_name == 'workflow_dispatch' || (github.event_name == 'pull_request' && contains(github.event.pull_request.labels.*.name, 'ci:guest-access'))"
	if strings.Join(strings.Fields(job.If), " ") != wantGate {
		t.Errorf("real guests must run only weekly, manually or on explicitly opted-in PRs; got %s", job.If)
	}
	content, err := os.ReadFile(repoPath(t, ".github/workflows/test-gateway-guest-access.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var events struct {
		On struct {
			Schedule    []struct{ Cron string }
			PullRequest struct{ Types []string } `yaml:"pull_request"`
		}
	}
	if err := yaml.Unmarshal(content, &events); err != nil {
		t.Fatal(err)
	}
	if len(events.On.Schedule) != 1 || events.On.Schedule[0].Cron != "17 4 * * 1" {
		t.Fatal("real guests need a weekly regression run")
	}
	if !slices.Contains(events.On.PullRequest.Types, "labeled") {
		t.Fatal("applying the opt-in label must trigger guest CI")
	}
	if !slices.Equal(job.RunsOn, stringList{"ubuntu-24.04"}) {
		t.Fatal("guest access must use a disposable hosted Linux runner")
	}
	var runs, artifacts bool
	for _, step := range job.Steps {
		runs = runs || strings.Contains(step.Run, "bash scripts/ci/gateway-guest-access-linux.sh")
		artifacts = artifacts || strings.HasPrefix(step.Uses, "actions/upload-artifact@") && step.If == "always() && steps.guest.outcome != 'skipped'" && step.With["path"] == "artifact/gateway-guest-access/"
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
