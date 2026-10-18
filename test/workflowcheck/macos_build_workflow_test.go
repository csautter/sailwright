package workflowcheck

import (
	"strings"
	"testing"
)

const (
	macOSBuildJob          = "test-packer-build"
	macOSRunnerExpression  = "${{ vars.USE_TART_MACOS_RUNNERS == 'true' && matrix.tart_runs_on || matrix.runs_on }}"
	macOSUseTartExpression = "${{ vars.USE_TART_MACOS_RUNNERS == 'true' && 'true' || 'false' }}"
)

func TestMacOSBuildWorkflowDefaultsToGitHubHostedRunnersWithTartOptIn(t *testing.T) {
	t.Parallel()

	j := loadWorkflow(t, macOSBuildWorkflow).job(t, macOSBuildJob)
	if len(j.RunsOn) != 1 || j.RunsOn[0] != macOSRunnerExpression {
		t.Errorf("job must run on %s, got %v", macOSRunnerExpression, j.RunsOn)
	}
	if got := j.Env["USE_TART_MACOS_RUNNERS"]; got != macOSUseTartExpression {
		t.Errorf("job must set USE_TART_MACOS_RUNNERS=%s, got %q", macOSUseTartExpression, got)
	}

	for _, entry := range j.Strategy.Matrix.Include {
		testName, runsOn := entry["go_test_name"], entry["runs_on"]
		if !strings.HasPrefix(runsOn, "macos-") {
			t.Errorf("matrix entry %s must default to a GitHub-hosted macOS runner, got %q", testName, runsOn)
		}
		if want := runsOn + "-tart"; entry["tart_runs_on"] != want {
			t.Errorf("matrix entry %s must offer the Tart runner %q as opt-in, got %q", testName, want, entry["tart_runs_on"])
		}
	}
}

func TestMacOSBuildWorkflowSkipsDeploySmokeTestsForStartOnlyProbes(t *testing.T) {
	t.Parallel()

	j := loadWorkflow(t, macOSBuildWorkflow).job(t, macOSBuildJob)
	for _, entry := range j.Strategy.Matrix.Include {
		if entry["vm_os"] == "ubuntu" && entry["packer_start_only"] != "true" {
			t.Errorf("matrix entry %s must run as a start-only Packer probe", entry["go_test_name"])
		}
	}

	deploy := j.stepByID(t, macOSBuildJob, "deploy_smoke")
	for _, condition := range []string{"steps.packer_build.outcome == 'success'", "matrix.packer_start_only != 'true'"} {
		if !strings.Contains(deploy.If, condition) {
			t.Errorf("deploy smoke step must require %s", condition)
		}
	}
}
