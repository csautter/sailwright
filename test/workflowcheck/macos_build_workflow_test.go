package workflowcheck

import (
	"strings"
	"testing"
)

const (
	macOSBuildJob          = "macos-full"
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
		want := "false"
		if entry.arch() == "amd64" {
			want = "true"
		}
		if entry["packer_start_only"] != want {
			t.Errorf("matrix entry %s must default packer_start_only to %s", entry["go_test_name"], want)
		}
	}

	deploy := j.stepByID(t, macOSBuildJob, "deploy_smoke")
	for _, condition := range []string{"steps.packer_build.outcome == 'success'", "env.DEV_ALCHEMY_PACKER_START_ONLY != 'true'"} {
		if !strings.Contains(deploy.If, condition) {
			t.Errorf("deploy smoke step must require %s", condition)
		}
	}
}

func TestMacOSQuickChecksNeverUseTartOrFullBuilds(t *testing.T) {
	t.Parallel()
	j := loadWorkflow(t, macOSBuildWorkflow).job(t, "macos-quick")
	if len(j.RunsOn) != 1 || j.RunsOn[0] != "macos-26" {
		t.Errorf("quick checks must use hosted macos-26, got %v", j.RunsOn)
	}
	for _, entry := range j.Strategy.Matrix.Include {
		if entry["packer_start_only"] != "true" {
			t.Errorf("quick matrix entry %s must only probe startup", entry["go_test_name"])
		}
	}
	if j.stepIndex(func(s step) bool { return s.ID == "deploy_smoke" }) != -1 {
		t.Error("quick checks must not boot guests in UTM")
	}
}

func TestMacOSFullBuildsRestrictAmd64ToReleaseOrMonthlySelection(t *testing.T) {
	t.Parallel()
	j := loadWorkflow(t, macOSBuildWorkflow).job(t, macOSBuildJob)
	want := "${{ matrix.packer_start_only == 'true' && needs.select.outputs.full_amd64 != 'true' && 'true' || 'false' }}"
	if j.Env["DEV_ALCHEMY_PACKER_START_ONLY"] != want {
		t.Errorf("full builds must gate amd64 installation on monthly/release selection")
	}
}
