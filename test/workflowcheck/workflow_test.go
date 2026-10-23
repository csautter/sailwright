package workflowcheck

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// workflow models the subset of the GitHub Actions workflow schema these tests
// inspect. Unknown keys are ignored.
type workflow struct {
	On          triggers          `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]job    `yaml:"jobs"`
}

type triggers struct {
	Push        eventFilter `yaml:"push"`
	PullRequest eventFilter `yaml:"pull_request"`
}

type eventFilter struct {
	Branches []string `yaml:"branches"`
	Paths    []string `yaml:"paths"`
}

type job struct {
	Name        string            `yaml:"name"`
	Needs       stringList        `yaml:"needs"`
	If          string            `yaml:"if"`
	RunsOn      stringList        `yaml:"runs-on"`
	Permissions map[string]string `yaml:"permissions"`
	Env         map[string]string `yaml:"env"`
	Strategy    struct {
		Matrix struct {
			Include []matrixEntry `yaml:"include"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []step `yaml:"steps"`
}

type matrixEntry map[string]string

type step struct {
	ID   string            `yaml:"id"`
	Name string            `yaml:"name"`
	If   string            `yaml:"if"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
	With map[string]string `yaml:"with"`
}

// stringList accepts a scalar or a sequence, as GitHub does for keys such as
// needs and runs-on.
type stringList []string

func (l *stringList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		*l = stringList{node.Value}
		return nil
	}
	var values []string
	if err := node.Decode(&values); err != nil {
		return err
	}
	*l = values
	return nil
}

func loadWorkflow(t *testing.T, fileName string) workflow {
	t.Helper()

	relPath := filepath.Join(".github", "workflows", fileName)
	content, err := os.ReadFile(repoPath(t, relPath))
	if err != nil {
		t.Fatalf("failed to read workflow %q: %v", relPath, err)
	}

	var wf workflow
	if err := yaml.Unmarshal(content, &wf); err != nil {
		t.Fatalf("failed to parse workflow %q: %v", relPath, err)
	}
	return wf
}

// buildJobs returns the jobs whose matrix runs Packer build tests, keyed by job ID.
func (wf workflow) buildJobs(t *testing.T) map[string]job {
	t.Helper()

	jobs := map[string]job{}
	for id, j := range wf.Jobs {
		if len(j.Strategy.Matrix.Include) > 0 && j.Strategy.Matrix.Include[0]["go_test_name"] != "" {
			jobs[id] = j
		}
	}
	if len(jobs) == 0 {
		t.Fatal("expected workflow to define at least one Packer build matrix job")
	}
	return jobs
}

func (wf workflow) job(t *testing.T, id string) job {
	t.Helper()

	j, ok := wf.Jobs[id]
	if !ok {
		t.Fatalf("expected workflow to define job %q", id)
	}
	return j
}

func (j job) stepIndex(match func(step) bool) int {
	return slices.IndexFunc(j.Steps, match)
}

func (j job) stepByID(t *testing.T, jobID, stepID string) step {
	t.Helper()

	index := j.stepIndex(func(s step) bool { return s.ID == stepID })
	if index == -1 {
		t.Fatalf("expected job %q to define step id %q", jobID, stepID)
	}
	return j.Steps[index]
}

func (j job) stepsUsing(action string) []step {
	var steps []step
	for _, s := range j.Steps {
		if usesAction(s, action) {
			steps = append(steps, s)
		}
	}
	return steps
}

// usesAction reports whether the step uses the given action at any version.
func usesAction(s step, action string) bool {
	return strings.HasPrefix(s.Uses, action+"@")
}

// arch derives the target architecture from the Go test name, which every
// build matrix entry carries.
func (e matrixEntry) arch() string {
	switch name := e["go_test_name"]; {
	case strings.Contains(name, "Arm64"):
		return "arm64"
	case strings.Contains(name, "Amd64"):
		return "amd64"
	default:
		return ""
	}
}

type cacheEntry struct {
	LocalPath string `json:"local-path"`
	Manifest  string `json:"manifest"`
}

// cacheEntries returns the descriptors from the entry's cache_files JSON list.
func (e matrixEntry) cacheEntries(t *testing.T) []cacheEntry {
	t.Helper()

	var files []cacheEntry
	if err := json.Unmarshal([]byte(e["cache_files"]), &files); err != nil {
		t.Fatalf("failed to parse cache_files of matrix entry %s: %v", e["go_test_name"], err)
	}
	return files
}

// cacheFiles returns the local paths from the entry's cache_files JSON list.
func (e matrixEntry) cacheFiles(t *testing.T) []string {
	t.Helper()

	files := e.cacheEntries(t)
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.LocalPath)
	}
	return paths
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

var testFuncRE = regexp.MustCompile(`(?m)^func (Test\w+)\(t \*testing\.T\)`)

// testFuncNames returns the names of the top-level test functions in a package directory.
func testFuncNames(t *testing.T, pkgDir string) map[string]bool {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(repoPath(t, pkgDir), "*_test.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("failed to list test files in %q: %v", pkgDir, err)
	}
	names := map[string]bool{}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("failed to read %q: %v", file, err)
		}
		for _, match := range testFuncRE.FindAllStringSubmatch(string(content), -1) {
			names[match[1]] = true
		}
	}
	return names
}

func repoPath(t *testing.T, relPath string) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine test file path")
	}

	return filepath.Join(filepath.Dir(currentFile), "..", "..", relPath)
}
