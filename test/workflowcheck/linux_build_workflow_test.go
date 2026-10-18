package workflowcheck

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	linuxBuildJob         = "test-packer-build"
	linuxPublishJob       = "publish-ubuntu-oci-artifacts"
	ubuntuOCIArtifactName = "ubuntu-oci-${{ matrix.ubuntu_type }}-${{ matrix.arch }}"
)

func TestLinuxBuildWorkflowMatrixFieldsMatchTestNames(t *testing.T) {
	t.Parallel()

	jobs := loadWorkflow(t, linuxBuildWorkflow).buildJobs(t)
	for _, jobID := range sortedKeys(jobs) {
		for _, entry := range jobs[jobID].Strategy.Matrix.Include {
			testName := entry["go_test_name"]
			if entry["arch"] != entry.arch() {
				t.Errorf("job %q matrix entry %s has arch %q", jobID, testName, entry["arch"])
			}

			switch {
			case strings.HasPrefix(testName, "TestBuildQemuWindows11"):
				if entry["vm_os"] != "windows11" {
					t.Errorf("job %q matrix entry %s must set vm_os: windows11", jobID, testName)
				}
			case strings.HasPrefix(testName, "TestBuildQemuUbuntu"):
				if entry["vm_os"] != "ubuntu" {
					t.Errorf("job %q matrix entry %s must set vm_os: ubuntu", jobID, testName)
				}
				wantType := "server"
				if strings.Contains(testName, "Desktop") {
					wantType = "desktop"
				}
				if entry["ubuntu_type"] != wantType {
					t.Errorf("job %q matrix entry %s must set ubuntu_type: %s", jobID, testName, wantType)
				}
			default:
				t.Errorf("job %q matrix entry %s is neither an Ubuntu nor a Windows 11 QEMU build", jobID, testName)
			}
		}
	}
}

func TestLinuxBuildWorkflowProbesWindowsAndFullyBuildsUbuntu(t *testing.T) {
	t.Parallel()

	jobs := loadWorkflow(t, linuxBuildWorkflow).buildJobs(t)
	for _, jobID := range sortedKeys(jobs) {
		for _, entry := range jobs[jobID].Strategy.Matrix.Include {
			want := "false"
			if entry["vm_os"] == "windows11" {
				want = "true"
			}
			if entry["packer_start_only"] != want {
				t.Errorf("job %q matrix entry %s must set packer_start_only: %q", jobID, entry["go_test_name"], want)
			}
			if entry["vm_os"] == "windows11" && !slices.Contains(entry.cacheFiles(t), "./.dev-alchemy/cache/windows/virtio-win.iso") {
				t.Errorf("job %q matrix entry %s must cache the virtio-win ISO", jobID, entry["go_test_name"])
			}
		}
	}
}

func TestLinuxBuildWorkflowRunnersMatchArchitecture(t *testing.T) {
	t.Parallel()

	build := loadWorkflow(t, linuxBuildWorkflow).job(t, linuxBuildJob)
	for _, entry := range build.Strategy.Matrix.Include {
		if isArmRunner := strings.HasSuffix(entry["runs_on"], "-arm"); isArmRunner != (entry.arch() == "arm64") {
			t.Errorf("matrix entry %s runs on %q, which does not match its %s architecture", entry["go_test_name"], entry["runs_on"], entry.arch())
		}
	}
}

func TestLinuxBuildWorkflowGrantsPackagesWriteOnlyToPublishJob(t *testing.T) {
	t.Parallel()

	wf := loadWorkflow(t, linuxBuildWorkflow)
	if wf.Permissions["packages"] == "write" {
		t.Error("workflow must not grant packages: write at workflow scope")
	}
	for _, jobID := range sortedKeys(wf.Jobs) {
		grantsWrite := wf.Jobs[jobID].Permissions["packages"] == "write"
		if grantsWrite != (jobID == linuxPublishJob) {
			t.Errorf("job %q: packages: write must be granted to %q and no other job", jobID, linuxPublishJob)
		}
	}
}

var passwordArgRE = regexp.MustCompile(`--password[ =]`)

func TestLinuxBuildWorkflowPublishesUbuntuOCIArtifactsOnlyFromMain(t *testing.T) {
	t.Parallel()

	wf := loadWorkflow(t, linuxBuildWorkflow)
	if !slices.Equal(wf.On.Push.Branches, []string{"main"}) {
		t.Errorf("workflow must run push builds only for main, got branches %v", wf.On.Push.Branches)
	}
	for event, paths := range map[string][]string{"push": wf.On.Push.Paths, "pull_request": wf.On.PullRequest.Paths} {
		if !slices.Contains(paths, "pkg/oci/**") {
			t.Errorf("workflow %s trigger must include pkg/oci/** so OCI publishing changes run the pipeline", event)
		}
	}

	buildJobs := wf.buildJobs(t)
	publishedImages := map[string]bool{}
	for _, jobID := range sortedKeys(buildJobs) {
		j := buildJobs[jobID]
		for _, entry := range j.Strategy.Matrix.Include {
			if entry["vm_os"] == "ubuntu" {
				publishedImages[entry["ubuntu_type"]+"-"+entry["arch"]] = true
			}
		}

		upload := slices.IndexFunc(j.stepsUsing("actions/upload-artifact"), func(s step) bool {
			return s.With["name"] == ubuntuOCIArtifactName &&
				s.With["include-hidden-files"] == "true" &&
				strings.Contains(s.If, "matrix.vm_os == 'ubuntu'") &&
				strings.Contains(s.If, "github.ref == 'refs/heads/main'")
		})
		if upload == -1 {
			t.Errorf("job %q must upload the Ubuntu QCOW2 as %s for main-branch Ubuntu builds, including hidden files", jobID, ubuntuOCIArtifactName)
		}
	}

	publish := wf.job(t, linuxPublishJob)
	for _, condition := range []string{"github.event_name != 'pull_request'", "github.ref == 'refs/heads/main'"} {
		if !strings.Contains(publish.If, condition) {
			t.Errorf("publish job must require %s", condition)
		}
	}
	for _, jobID := range sortedKeys(buildJobs) {
		if !slices.Contains(publish.Needs, jobID) {
			t.Errorf("publish job must wait for build job %q", jobID)
		}
	}

	publishMatrix := map[string]bool{}
	for _, entry := range publish.Strategy.Matrix.Include {
		publishMatrix[entry["ubuntu_type"]+"-"+entry["arch"]] = true
	}
	if !maps.Equal(publishMatrix, publishedImages) {
		t.Errorf("publish matrix %v must match the Ubuntu builds %v", sortedKeys(publishMatrix), sortedKeys(publishedImages))
	}

	if slices.IndexFunc(publish.stepsUsing("actions/download-artifact"), func(s step) bool {
		return s.With["name"] == ubuntuOCIArtifactName
	}) == -1 {
		t.Errorf("publish job must download the %s artifact", ubuntuOCIArtifactName)
	}

	for _, jobID := range sortedKeys(wf.Jobs) {
		for _, s := range wf.Jobs[jobID].Steps {
			if !strings.Contains(s.Run, "go run cmd/main.go push") {
				continue
			}
			if jobID != linuxPublishJob {
				t.Errorf("job %q must not push OCI artifacts; only %q may", jobID, linuxPublishJob)
			}
			if !strings.Contains(s.Run, "--os ubuntu") {
				t.Errorf("job %q step %q must only push Ubuntu OCI artifacts", jobID, s.Name)
			}
			if !strings.Contains(s.Run, "--password-stdin") || passwordArgRE.MatchString(s.Run) {
				t.Errorf("job %q step %q must pass the registry token via --password-stdin, not on the command line", jobID, s.Name)
			}
		}
	}
}
