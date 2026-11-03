package build

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestRunBuildScriptTimeoutStopsBuildThatIgnoresTermination runs on every OS, Windows
// included: a build that ignores the polite stop request must still be killed once the
// grace period is over, so a timed-out or cancelled build never waits for it forever.
func TestRunBuildScriptTimeoutStopsBuildThatIgnoresTermination(t *testing.T) {
	root := useTestDirectories(t)
	execPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(root, "cache", "fake", "disk.qcow2")
	config := VirtualMachineConfig{
		OS:                     "fake-os",
		Arch:                   "amd64",
		VncPort:                5990,
		ExpectedBuildArtifacts: []string{artifact},
		StagedBuildArtifacts:   []string{stagedBuildArtifactPath(artifact, time.Now().UnixNano())},
		DetachedFromTerminal:   true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- RunBuildScript(ctx, config, execPath,
			[]string{"-test.run=^TestHelperProcessIgnoreTermination$", "--"},
			"GO_WANT_HELPER_PROCESS=1")
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected the build to end with the deadline, got %v", err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second+3*buildProcessGracePeriod {
			t.Fatalf("build took %s to stop", elapsed)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("RunBuildScript did not return after its context deadline")
	}
	assertArtifactState(t, ArtifactStateMissing, artifact)
}

// TestHelperProcessIgnoreTermination acts as a build that ignores SIGTERM and Ctrl+C.
func TestHelperProcessIgnoreTermination(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	signal.Ignore(os.Interrupt, syscall.SIGTERM)
	time.Sleep(60 * time.Second)
	os.Exit(0)
}
