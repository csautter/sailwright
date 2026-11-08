package workflowcheck

import (
	"os"
	"path/filepath"
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

func TestMacOSFullBuildsRequireGuestReadinessAfterBundleCreation(t *testing.T) {
	t.Parallel()
	j := loadWorkflow(t, macOSBuildWorkflow).job(t, macOSBuildJob)
	for _, entry := range j.Strategy.Matrix.Include {
		want := "windows11"
		if entry["vm_os"] == "ubuntu" {
			want = "ubuntu-server"
			if strings.Contains(entry["go_test_name"], "Desktop") {
				want = "ubuntu-desktop"
			}
		}
		if entry["utm_os"] != want {
			t.Errorf("%s must boot %s, got %q", entry["go_test_name"], want, entry["utm_os"])
		}
	}
	if !strings.Contains(j.Env["SAILWRIGHT_UTM_VM_DIR"], "${{ github.workspace }}/.dev-alchemy/") {
		t.Error("boot checks must use disposable workspace bundles")
	}
	smoke := j.stepByID(t, macOSBuildJob, "deploy_smoke")
	create := strings.Index(smoke.Run, "go test -timeout 60m")
	boot := strings.Index(smoke.Run, "python3 test/macos-profile/boot-utm.py")
	if create == -1 || boot <= create {
		t.Fatal("full deploy smoke must boot the bundle after creation, not only copy its disk")
	}
	for _, required := range []string{"$SAILWRIGHT_UTM_VM_DIR/${{ matrix.utm_os }}-${{ matrix.arch }}-dev-alchemy.utm", "--software-emulation", "--diagnostics artifact/utm-boot"} {
		if !strings.Contains(smoke.Run, required) {
			t.Errorf("boot check missing %q", required)
		}
	}
	if j.stepIndex(func(s step) bool {
		return usesAction(s, "actions/upload-artifact") && s.If == "always()" && s.With["path"] == "artifact/utm-boot"
	}) == -1 {
		t.Error("UTM failures must retain diagnostics")
	}
}

func TestMacOSUnrelatedLabelsDoNotCancelRunningMacJobs(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(repoPath(t, filepath.Join(".github", "workflows", macOSBuildWorkflow)))
	if err != nil {
		t.Fatal(err)
	}
	// Selector skips unrelated labels; the group must keep them from cancelling.
	want := "${{ github.event.action == 'labeled' && !startsWith(github.event.label.name, 'ci:macos') && format('-label-{0}', github.run_id) || '' }}"
	if !strings.Contains(string(data), want) {
		t.Errorf("concurrency group must isolate unrelated label events with %s", want)
	}
}
