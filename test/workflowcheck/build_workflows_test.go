package workflowcheck

import (
	"slices"
	"strings"
	"testing"
)

const (
	linuxBuildWorkflow = "test-build-linux.yml"
	macOSBuildWorkflow = "test-build-macos.yml"
)

var buildWorkflows = []string{linuxBuildWorkflow, macOSBuildWorkflow}

func TestBuildWorkflowMatrixEntriesReferenceExistingTests(t *testing.T) {
	t.Parallel()

	testsByPackage := map[string]map[string]bool{
		"pkg/build":  testFuncNames(t, "pkg/build"),
		"pkg/deploy": testFuncNames(t, "pkg/deploy"),
	}
	packageByKey := map[string]string{
		"go_test_name":         "pkg/build",
		"dependency_test_name": "pkg/build",
		"deploy_test_name":     "pkg/deploy",
	}

	for _, fileName := range buildWorkflows {
		t.Run(fileName, func(t *testing.T) {
			t.Parallel()

			jobs := loadWorkflow(t, fileName).buildJobs(t)
			for _, jobID := range sortedKeys(jobs) {
				for _, entry := range jobs[jobID].Strategy.Matrix.Include {
					for _, key := range sortedKeys(packageByKey) {
						name, pkg := entry[key], packageByKey[key]
						if name != "" && !testsByPackage[pkg][name] {
							t.Errorf("job %q matrix %s %q does not match a test function in %s", jobID, key, name, pkg)
						}
					}
				}
			}
		})
	}
}

func TestBuildWorkflowsPassPackerStartOnlySettingsToBuildStep(t *testing.T) {
	t.Parallel()

	for _, fileName := range buildWorkflows {
		t.Run(fileName, func(t *testing.T) {
			t.Parallel()

			jobs := loadWorkflow(t, fileName).buildJobs(t)
			for _, jobID := range sortedKeys(jobs) {
				j := jobs[jobID]
				for _, entry := range j.Strategy.Matrix.Include {
					if startOnly := entry["packer_start_only"]; startOnly != "true" && startOnly != "false" {
						t.Errorf("job %q matrix entry %s must set packer_start_only to \"true\" or \"false\", got %q", jobID, entry["go_test_name"], startOnly)
					}
					if entry["packer_start_timeout"] == "" {
						t.Errorf("job %q matrix entry %s must set packer_start_timeout", jobID, entry["go_test_name"])
					}
				}

				build := j.stepByID(t, jobID, "packer_build")
				for envName, want := range map[string]string{
					"DEV_ALCHEMY_PACKER_START_ONLY":    "${{ matrix.packer_start_only }}",
					"DEV_ALCHEMY_PACKER_START_TIMEOUT": "${{ matrix.packer_start_timeout }}",
				} {
					if got := build.Env[envName]; got != want {
						t.Errorf("job %q build step must set %s=%s, got %q", jobID, envName, want, got)
					}
				}
			}
		})
	}
}

func TestBuildWorkflowsSetUpGoFromGoModBeforeInstallingDependencies(t *testing.T) {
	t.Parallel()

	for _, fileName := range buildWorkflows {
		t.Run(fileName, func(t *testing.T) {
			t.Parallel()

			jobs := loadWorkflow(t, fileName).buildJobs(t)
			for _, jobID := range sortedKeys(jobs) {
				j := jobs[jobID]
				setupGo := j.stepIndex(func(s step) bool {
					return usesAction(s, "actions/setup-go") && s.With["go-version-file"] == "go.mod"
				})
				install := j.stepIndex(func(s step) bool {
					return strings.Contains(s.Run, "go run cmd/main.go install")
				})
				if setupGo == -1 || install == -1 || setupGo > install {
					t.Errorf("job %q must set up Go from go.mod before running the dependency install", jobID)
				}
			}
		})
	}
}

func TestBuildWorkflowsCacheArchitectureSpecificDependencies(t *testing.T) {
	t.Parallel()

	for _, fileName := range buildWorkflows {
		t.Run(fileName, func(t *testing.T) {
			t.Parallel()

			jobs := loadWorkflow(t, fileName).buildJobs(t)
			for _, jobID := range sortedKeys(jobs) {
				for _, entry := range jobs[jobID].Strategy.Matrix.Include {
					testName, arch := entry["go_test_name"], entry.arch()
					if arch == "" {
						t.Errorf("job %q matrix entry %s does not name its architecture (Amd64/Arm64)", jobID, testName)
						continue
					}

					var want []string
					if arch == "arm64" {
						want = append(want, "./.dev-alchemy/cache/qemu-efi-aarch64_all.deb")
					}
					if entry["vm_os"] == "windows11" {
						want = append(want,
							"./.dev-alchemy/cache/windows11/iso/win11_25h2_english_"+arch+".iso",
							"./.dev-alchemy/cache/utm/utm-guest-tools-latest.iso",
						)
					}

					cached := entry.cacheFiles(t)
					for _, path := range want {
						if !slices.Contains(cached, path) {
							t.Errorf("job %q matrix entry %s must cache %s", jobID, testName, path)
						}
					}
				}
			}
		})
	}
}
