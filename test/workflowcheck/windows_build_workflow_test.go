package workflowcheck

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestWindowsHypervRouting(t *testing.T) {
	script := `import importlib.util
spec = importlib.util.spec_from_file_location('routes', 'test/hyperv-profile/routes.py')
r = importlib.util.module_from_spec(spec)
spec.loader.exec_module(r)
def pr(labels=(), ref='feature', action='synchronize', label=''):
    return {'action': action, 'label': {'name': label}, 'pull_request': {'head': {'ref': ref}, 'labels': [{'name': x} for x in labels]}}
cases = [
    ('pull_request', pr(), ['docs/testing-workflows.md'], False),
    ('pull_request', pr(), ['pkg/deploy/linux-libvirt-deploy.go'], False),
    ('pull_request', pr(), ['build/packer/windows/windows11-on-linux.pkr.hcl'], False),
    ('pull_request', pr(['ci:hyperv']), [], True),
    ('pull_request', pr(ref='release-please--main'), [], True),
    ('pull_request', pr(['ci:hyperv'], action='labeled', label='docs'), ['runtimeassets.go'], False),
    ('pull_request', pr(['ci:hyperv'], action='labeled', label='ci:hyperv'), [], True),
    ('workflow_dispatch', {}, [], True), ('schedule', {}, [], True),
]
for path in ('pkg/build/macos-silicon-build.go', 'pkg/build/utm_paths.go', 'pkg/build/qemu-arm64.go', 'pkg/build/linux-build.go', 'pkg/build/windows-linux-build.go', 'pkg/build/process_helpers_unix.go', 'pkg/build/memory_other.go', 'pkg/deploy/tart-macos-deploy.go', 'pkg/deploy/utm_registration.go', 'pkg/deploy/macos-silicon-deploy.go', 'cmd/cmd/create.go'):
    cases.append(('pull_request', pr(), [path], False))
for path in ('pkg/build/interrupt.go', 'pkg/build/log_sanitizer.go', 'pkg/build/memory_windows.go', 'pkg/build/vnc_recording.go', 'pkg/build/brand_new_helper.go', 'pkg/deploy/command-runner.go', 'pkg/deploy/shell_util.go', 'pkg/deploy/windows-hyperv-deploy.go', 'cmd/main.go', 'cmd/cmd/install.go', 'build/packer/windows/hyperv/autounattend.xml', 'build/packer/windows/windows11-on-windows-hyperv.pkr.hcl', 'pkg/build/windows-build.go', 'pkg/build/dependencies.go', 'pkg/build/directories.go', 'pkg/build/generic_build.go', 'pkg/build/win11iso.go', 'pkg/build/helper.go', 'pkg/build/virtual-machine.go', 'runtimeassets.go', '.github/workflows/test-build-windows.yml', '.github/actions/download-build-cache/action.yml', '.github/actions/upload-build-cache/action.yml', 'scripts/gh-runner-func/delete-resource-group.sh', 'scripts/windows/install_oscdimg.ps1'):
    cases.append(('pull_request', pr(), ['unrelated'] * 301 + [path], True))
for name, event, files, want in cases:
    assert r.select(name, event, files) == want, (name, event, files[-1:] , want)
`
	cmd := exec.Command("python3", "-c", script)
	cmd.Dir = repoPath(t, ".")
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("routing: %v\n%s", err, out)
	}
}

func TestWindowsHypervCostGate(t *testing.T) {
	wf := loadWorkflow(t, "test-build-windows.yml")
	provision := wf.job(t, "provision-hyperv-runner")
	if !slices.Equal([]string(provision.Needs), []string{"select", "preflight"}) || provision.If != "needs.select.outputs.hyperv == 'true'" {
		t.Fatal("provision must require selection and successful preflight")
	}
	preflight := wf.job(t, "preflight")
	if !slices.Equal([]string(preflight.Needs), []string{"select"}) || preflight.If != provision.If || !slices.Contains(preflight.RunsOn, "ubuntu-24.04") {
		t.Fatal("preflight must follow routing on Linux")
	}
	if preflight.Permissions["contents"] != "read" || len(preflight.Permissions) != 1 {
		t.Error("preflight runs PR code and must not inherit id-token: write")
	}
	data, err := os.ReadFile(repoPath(t, ".github/workflows/test-build-windows.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"types: [opened, synchronize, reopened, labeled]", "cron: '0 4 * * 1'", "github.event.action != 'labeled' || github.event.label.name == 'ci:hyperv'", "github.event.pull_request.number || github.run_id", "${{ github.workflow }}-${{ github.event_name }}-${{ github.event_name == 'pull_request' &&", "cancel-in-progress: ${{ github.event_name == 'pull_request' }}", "go test ./pkg/build", "packer validate -syntax-only build/packer/windows/windows11-on-windows-hyperv.pkr.hcl"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing contract: %s", want)
		}
	}
	if len(wf.On.PullRequest.Paths) != 0 {
		t.Error("PR files must not be truncated by trigger path filters")
	}
	names := provision.stepByID(t, "provision-hyperv-runner", "vars").Run
	cleanup := wf.job(t, "cleanup-hyperv")
	if cleanup.If != "always() && needs.provision-hyperv-runner.result != 'skipped'" {
		t.Error("cleanup must run after cancellation or failure")
	}
	deletion := cleanup.Steps[len(cleanup.Steps)-1]
	for key, want := range map[string]string{"RESOURCE_GROUP": "gh-runner-tmp-${{ github.run_id }}-hv", "RUNNER_NAME": "${{ github.run_id }}-hv"} {
		if deletion.Env[key] != want || !strings.Contains(names, strings.ReplaceAll(want, "${{ github.run_id }}", "${GITHUB_RUN_ID}")) {
			t.Errorf("inconsistent %s", key)
		}
	}
	if strings.Contains(deletion.Run, "if !") || !strings.Contains(deletion.Run, "bash scripts/gh-runner-func/delete-resource-group.sh") {
		t.Error("deletion failure must fail cleanup")
	}
}
