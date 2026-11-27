package workflowcheck

import (
	"strings"
	"testing"
)

const releaseBinariesWorkflow = "release-binaries.yml"

func TestReleaseWorkflowsKeepActiveRuns(t *testing.T) {
	t.Parallel()

	for _, file := range []string{"release-please.yml", releaseBinariesWorkflow} {
		wf := loadWorkflow(t, file)
		if got := wf.Concurrency.CancelInProgress; got != "false" {
			t.Errorf("%s must set cancel-in-progress to false to protect active releases, got %q", file, got)
		}
	}
}

func TestReleaseBinariesBuildChecksOutRequestedTag(t *testing.T) {
	t.Parallel()

	wf := loadWorkflow(t, releaseBinariesWorkflow)
	checkouts := wf.job(t, "build").stepsUsing("actions/checkout")
	if len(checkouts) != 1 {
		t.Fatalf("build must have one checkout, got %d", len(checkouts))
	}
	const wantRef = "${{ (inputs.version || github.event_name != 'pull_request') && format('refs/tags/{0}', inputs.version) || '' }}"
	if got := checkouts[0].With["ref"]; got != wantRef {
		t.Errorf("build checkout must use the input tag for releases and the default ref for PRs, got %q, want %q", got, wantRef)
	}
	if got := checkouts[0].If; got != "" {
		t.Errorf("build checkout must run for releases and PRs, got condition %q", got)
	}
}

func TestReleaseBinariesRequiresMainForPublishing(t *testing.T) {
	t.Parallel()

	wf := loadWorkflow(t, releaseBinariesWorkflow)
	publish := wf.job(t, "publish")
	if len(publish.Steps) == 0 ||
		publish.Steps[0].If != "${{ github.ref != 'refs/heads/main' }}" ||
		!strings.Contains(publish.Steps[0].Run, "\nexit 1\n") {
		t.Error("publish must reject workflow runs outside refs/heads/main before signing assets")
	}
}
