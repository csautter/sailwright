package build

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertArtifactState(t *testing.T, want ArtifactState, artifacts ...string) {
	t.Helper()
	got, err := artifactsState(artifacts)
	if err != nil {
		t.Fatalf("artifactsState: %v", err)
	}
	if got != want {
		t.Fatalf("expected state %q, got %q", want, got)
	}
}

func TestArtifactStateMissing(t *testing.T) {
	assertArtifactState(t, ArtifactStateMissing, filepath.Join(t.TempDir(), "disk.qcow2"))
}

func TestArtifactStateWithCompletionMarkerExists(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "disk.qcow2")
	writeTestFile(t, artifact, "complete image")
	if err := MarkBuildArtifactsComplete([]string{artifact}); err != nil {
		t.Fatal(err)
	}
	assertArtifactState(t, ArtifactStateExists, artifact)
	if !ArtifactStateExists.Usable() {
		t.Fatal("exists must be usable")
	}
}

func TestArtifactStateWithoutMarkerIsUnverifiedButUsable(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "disk.qcow2")
	writeTestFile(t, artifact, "image from an older version")
	assertArtifactState(t, ArtifactStateUnverified, artifact)
	if !ArtifactStateUnverified.Usable() {
		t.Fatal("unverified artifacts must stay usable")
	}
}

func TestArtifactStateWithMismatchingMarkerIsIncomplete(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "disk.qcow2")
	writeTestFile(t, artifact, "complete image")
	if err := MarkBuildArtifactsComplete([]string{artifact}); err != nil {
		t.Fatal(err)
	}
	// e.g. an older version writing a new build directly to the final path
	writeTestFile(t, artifact, "trunc")
	assertArtifactState(t, ArtifactStateIncomplete, artifact)
	if ArtifactStateIncomplete.Usable() {
		t.Fatal("incomplete must not be usable")
	}
}

func TestArtifactStateWithCorruptMarkerIsIncomplete(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "disk.qcow2")
	writeTestFile(t, artifact, "image")
	writeTestFile(t, artifactCompletionMarkerPath(artifact), "{not json")
	assertArtifactState(t, ArtifactStateIncomplete, artifact)
}

func TestArtifactStateWithOnlyStagingLeftoverIsIncomplete(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "disk.qcow2")
	writeTestFile(t, stagedBuildArtifactPath(artifact, 42), "partial")
	assertArtifactState(t, ArtifactStateIncomplete, artifact)

	if err := removeBuildArtifactLeftovers([]string{artifact}); err != nil {
		t.Fatal(err)
	}
	assertArtifactState(t, ArtifactStateMissing, artifact)
}

func TestArtifactLeftoversIgnoreOtherTargets(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "qemu-ubuntu-server-packer-amd64.qcow2")
	other := filepath.Join(dir, "qemu-ubuntu-server-packer-arm64.qcow2")
	writeTestFile(t, stagedBuildArtifactPath(other, 1), "partial")
	assertArtifactState(t, ArtifactStateMissing, artifact)
}

func TestArtifactsStateCombinesWorstState(t *testing.T) {
	dir := t.TempDir()
	complete := filepath.Join(dir, "a.box")
	writeTestFile(t, complete, "a")
	if err := MarkBuildArtifactsComplete([]string{complete}); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "b.box")
	assertArtifactState(t, ArtifactStateMissing, complete, missing)

	writeTestFile(t, stagedBuildArtifactPath(missing, 7), "partial")
	assertArtifactState(t, ArtifactStateIncomplete, complete, missing)
}

func TestStagedBuildPromotionWritesCompletionMarker(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "disk.qcow2")
	staged := stagedBuildArtifactPath(artifact, 1)

	skip, cleanup, err := prepareBuildArtifactsForBuild(VirtualMachineConfig{
		ExpectedBuildArtifacts: []string{artifact},
		StagedBuildArtifacts:   []string{staged},
	})
	if err != nil || skip {
		t.Fatalf("expected the build to run, skip=%v err=%v", skip, err)
	}
	writeTestFile(t, staged, "new image")
	assertArtifactState(t, ArtifactStateIncomplete, artifact)

	if err := cleanup(true); err != nil {
		t.Fatal(err)
	}
	assertArtifactState(t, ArtifactStateExists, artifact)
}

func TestFailedStagedRebuildKeepsCompleteArtifact(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "disk.qcow2")
	writeTestFile(t, artifact, "good image")
	if err := MarkBuildArtifactsComplete([]string{artifact}); err != nil {
		t.Fatal(err)
	}
	staged := stagedBuildArtifactPath(artifact, 2)

	_, cleanup, err := prepareBuildArtifactsForBuild(VirtualMachineConfig{
		ExpectedBuildArtifacts: []string{artifact},
		StagedBuildArtifacts:   []string{staged},
		NoCache:                true,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, staged, "half")
	if err := cleanup(false); err != nil {
		t.Fatal(err)
	}
	assertArtifactState(t, ArtifactStateExists, artifact)
}

func TestIncompleteArtifactIsRebuiltWithoutNoCache(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "disk.qcow2")
	writeTestFile(t, artifact, "complete image")
	if err := MarkBuildArtifactsComplete([]string{artifact}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, artifact, "x")

	skip, _, err := prepareBuildArtifactsForBuild(VirtualMachineConfig{
		ExpectedBuildArtifacts: []string{artifact},
		StagedBuildArtifacts:   []string{stagedBuildArtifactPath(artifact, 3)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("an incomplete artifact must not be a cache hit")
	}
}

func TestInterruptedReplacementBackupIsRestoredInsteadOfDeleted(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "disk.qcow2")
	// A crash between the two renames of a replacement: the previous artifact exists
	// only as backups, the final path is missing.
	older := artifact + replaceArtifactBackupInfix + "100"
	newer := artifact + ociPullArtifactBackupInfix + "200"
	staged := stagedBuildArtifactPath(artifact, 300)
	writeTestFile(t, older, "older image")
	writeTestFile(t, newer, "previous image")
	writeTestFile(t, staged, "partial")
	assertArtifactState(t, ArtifactStateIncomplete, artifact)

	skip, _, err := prepareBuildArtifactsForBuild(VirtualMachineConfig{
		ExpectedBuildArtifacts: []string{artifact},
		StagedBuildArtifacts:   []string{stagedBuildArtifactPath(artifact, 400)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !skip {
		t.Fatal("the restored previous artifact must be a cache hit")
	}
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("expected the newest backup to be restored: %v", err)
	}
	if string(data) != "previous image" {
		t.Fatalf("expected the newest backup to be restored, got %q", data)
	}
	assertArtifactState(t, ArtifactStateUnverified, artifact)
}

// A promotion that stops after replacing only some artifacts of a set must not leave
// a mix of new and old artifacts that counts as usable.
func TestPartlyPromotedArtifactSetIsIncomplete(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "disk.qcow2")
	second := filepath.Join(dir, "efivars.fd")
	writeTestFile(t, first, "old disk")
	writeTestFile(t, second, "old vars")
	if err := MarkBuildArtifactsComplete([]string{first, second}); err != nil {
		t.Fatal(err)
	}

	stagedFirst := filepath.Join(dir, "disk"+stagedBuildArtifactInfix+"1.qcow2")
	writeTestFile(t, stagedFirst, "new disk")
	missingStagedSecond := filepath.Join(dir, "efivars"+stagedBuildArtifactInfix+"1.fd")
	if err := promoteStagedBuildArtifacts([]string{first, second}, []string{stagedFirst, missingStagedSecond}); err == nil {
		t.Fatal("expected promotion with a missing staged artifact to fail")
	}
	assertArtifactState(t, ArtifactStateIncomplete, first, second)

	if err := MarkBuildArtifactsComplete([]string{first, second}); err != nil {
		t.Fatal(err)
	}
	assertArtifactState(t, ArtifactStateExists, first, second)
}
