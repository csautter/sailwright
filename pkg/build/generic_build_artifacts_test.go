package build

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildArtifactsExistReturnsTrueWhenAllArtifactsExist(t *testing.T) {
	tempDir := t.TempDir()
	artifactA := filepath.Join(tempDir, "artifact-a")
	artifactB := filepath.Join(tempDir, "artifact-b")

	if err := os.WriteFile(artifactA, []byte("a"), 0644); err != nil {
		t.Fatalf("failed to create artifact A: %v", err)
	}
	if err := os.WriteFile(artifactB, []byte("b"), 0644); err != nil {
		t.Fatalf("failed to create artifact B: %v", err)
	}

	exists, err := BuildArtifactsExist(VirtualMachineConfig{
		ExpectedBuildArtifacts: []string{artifactA, artifactB},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !exists {
		t.Fatal("expected artifacts to exist")
	}
}

func TestBuildArtifactsExistReturnsFalseWhenArtifactIsMissing(t *testing.T) {
	tempDir := t.TempDir()
	artifactA := filepath.Join(tempDir, "artifact-a")
	missingArtifact := filepath.Join(tempDir, "missing-artifact")

	if err := os.WriteFile(artifactA, []byte("a"), 0644); err != nil {
		t.Fatalf("failed to create artifact A: %v", err)
	}

	exists, err := BuildArtifactsExist(VirtualMachineConfig{
		ExpectedBuildArtifacts: []string{artifactA, missingArtifact},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if exists {
		t.Fatal("expected artifacts to be reported missing")
	}
}

func TestBuildArtifactsExistReturnsErrorWhenNoArtifactsAreDefined(t *testing.T) {
	_, err := BuildArtifactsExist(VirtualMachineConfig{
		OS:   "does-not-exist",
		Arch: "amd64",
	})
	if err == nil {
		t.Fatal("expected error when no build artifacts are defined")
	}
}

func TestBuildArtifactsExistQuietDoesNotLog(t *testing.T) {
	tempDir := t.TempDir()
	artifact := filepath.Join(tempDir, "artifact-a")

	if err := os.WriteFile(artifact, []byte("a"), 0644); err != nil {
		t.Fatalf("failed to create artifact: %v", err)
	}

	var buf bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(originalWriter)

	exists, err := BuildArtifactsExistQuiet(VirtualMachineConfig{
		ExpectedBuildArtifacts: []string{artifact},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !exists {
		t.Fatal("expected artifact to exist")
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no log output, got %q", buf.String())
	}
}

func TestPrepareBuildArtifactsForBuildSkipsWhenArtifactsExistAndNoCacheDisabled(t *testing.T) {
	tempDir := t.TempDir()
	artifact := filepath.Join(tempDir, "artifact-a")

	if err := os.WriteFile(artifact, []byte("existing"), 0644); err != nil {
		t.Fatalf("failed to create artifact: %v", err)
	}

	skip, cleanup, err := prepareBuildArtifactsForBuild(VirtualMachineConfig{
		ExpectedBuildArtifacts: []string{artifact},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !skip {
		t.Fatal("expected build to be skipped when artifact exists and no-cache is disabled")
	}

	cleanup(false)

	content, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("failed to read artifact after cleanup: %v", err)
	}
	if string(content) != "existing" {
		t.Fatalf("expected artifact content to remain unchanged, got %q", string(content))
	}
}

func TestPrepareBuildArtifactsForBuildRequiresStagedArtifacts(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "artifact-a.qcow2")
	if _, _, err := prepareBuildArtifactsForBuild(VirtualMachineConfig{
		ExpectedBuildArtifacts: []string{artifact},
		NoCache:                true,
	}); err == nil {
		t.Fatal("expected an unstaged build to be rejected")
	}
}

func TestPrepareBuildArtifactsForBuildPromotesStagedArtifactOnSuccess(t *testing.T) {
	tempDir := t.TempDir()
	artifact := filepath.Join(tempDir, "artifact-a.qcow2")
	stagedArtifact := filepath.Join(tempDir, "artifact-a.dev-alchemy-build-1.qcow2")

	if err := os.WriteFile(artifact, []byte("existing"), 0644); err != nil {
		t.Fatalf("failed to create artifact: %v", err)
	}

	skip, cleanup, err := prepareBuildArtifactsForBuild(VirtualMachineConfig{
		ExpectedBuildArtifacts: []string{artifact},
		StagedBuildArtifacts:   []string{stagedArtifact},
		NoCache:                true,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if skip {
		t.Fatal("expected staged no-cache build to proceed")
	}

	content, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("failed to read original artifact during build: %v", err)
	}
	if string(content) != "existing" {
		t.Fatalf("expected original artifact to remain available during build, got %q", string(content))
	}

	if err := os.WriteFile(stagedArtifact, []byte("rebuilt"), 0644); err != nil {
		t.Fatalf("failed to create staged artifact: %v", err)
	}

	if err := cleanup(true); err != nil {
		t.Fatalf("expected cleanup to promote staged artifact, got %v", err)
	}

	content, err = os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("failed to read promoted artifact: %v", err)
	}
	if string(content) != "rebuilt" {
		t.Fatalf("expected staged artifact to replace original on success, got %q", string(content))
	}
	if _, err := os.Stat(stagedArtifact); !os.IsNotExist(err) {
		t.Fatalf("expected staged artifact to be moved into final location, got err=%v", err)
	}
}

func TestPrepareBuildArtifactsForBuildLeavesOriginalArtifactOnStagedFailure(t *testing.T) {
	tempDir := t.TempDir()
	artifact := filepath.Join(tempDir, "artifact-a.qcow2")
	stagedArtifact := filepath.Join(tempDir, "artifact-a.dev-alchemy-build-1.qcow2")

	if err := os.WriteFile(artifact, []byte("existing"), 0644); err != nil {
		t.Fatalf("failed to create artifact: %v", err)
	}

	skip, cleanup, err := prepareBuildArtifactsForBuild(VirtualMachineConfig{
		ExpectedBuildArtifacts: []string{artifact},
		StagedBuildArtifacts:   []string{stagedArtifact},
		NoCache:                true,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if skip {
		t.Fatal("expected staged no-cache build to proceed")
	}

	if err := os.WriteFile(stagedArtifact, []byte("partial"), 0644); err != nil {
		t.Fatalf("failed to create staged artifact: %v", err)
	}

	if err := cleanup(false); err != nil {
		t.Fatalf("expected cleanup to remove staged artifact, got %v", err)
	}

	content, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("failed to read original artifact after failed build cleanup: %v", err)
	}
	if string(content) != "existing" {
		t.Fatalf("expected original artifact to remain after failed staged build, got %q", string(content))
	}
	if _, err := os.Stat(stagedArtifact); !os.IsNotExist(err) {
		t.Fatalf("expected failed staged artifact to be removed, got err=%v", err)
	}
}

func TestRemoveBuildArtifactsForConfigUsesResolvedArtifacts(t *testing.T) {
	tempDir := t.TempDir()

	config := VirtualMachineConfig{
		OS:                   "windows11",
		Arch:                 "arm64",
		HostOs:               HostOsDarwin,
		VirtualizationEngine: VirtualizationEngineUtm,
	}
	config.ExpectedBuildArtifacts = nil

	dirs := GetDirectoriesInstance()
	originalCacheDir := dirs.CacheDir
	dirs.CacheDir = tempDir
	defer func() {
		dirs.CacheDir = originalCacheDir
	}()

	// Sanity check that the resolved artifact path uses the test cache directory.
	resolved, err := resolveExpectedBuildArtifacts(config)
	if err != nil {
		t.Fatalf("failed to resolve artifacts: %v", err)
	}
	if len(resolved) != 1 || !strings.HasPrefix(resolved[0], tempDir) {
		t.Fatalf("expected resolved artifact inside temp dir, got %v", resolved)
	}

	if err := os.MkdirAll(filepath.Dir(resolved[0]), 0755); err != nil {
		t.Fatalf("failed to create resolved artifact directory: %v", err)
	}

	if err := os.WriteFile(resolved[0], []byte("existing"), 0644); err != nil {
		t.Fatalf("failed to create resolved artifact: %v", err)
	}

	RemoveBuildArtifactsForConfig(config)

	if _, err := os.Stat(resolved[0]); !os.IsNotExist(err) {
		t.Fatalf("expected resolved artifact to be removed, got err=%v", err)
	}
}
