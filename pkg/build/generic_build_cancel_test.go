package build

import (
	"context"
	"errors"
	"os"
	"os/exec"
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

func stubStdinIsTerminal(t *testing.T, terminal bool) {
	t.Helper()
	previous := stdinIsTerminal
	stdinIsTerminal = func() bool { return terminal }
	t.Cleanup(func() { stdinIsTerminal = previous })
}

func TestBuildStopGracePeriod(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
		signals  []os.Signal
		want     time.Duration
	}{
		{name: "Ctrl+C in a terminal", terminal: true, signals: []os.Signal{os.Interrupt}, want: interactiveBuildProcessGracePeriod},
		{name: "SIGINT without a terminal (CI runner)", terminal: false, signals: []os.Signal{os.Interrupt}, want: buildProcessGracePeriod},
		{name: "supervisor SIGTERM", terminal: true, signals: []os.Signal{syscall.SIGTERM}, want: buildProcessGracePeriod},
		{name: "second Ctrl+C", terminal: true, signals: []os.Signal{os.Interrupt, os.Interrupt}, want: buildProcessGracePeriod},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubStdinIsTerminal(t, tc.terminal)
			h, ctx := newInterruptHandler(context.Background(), nil, func(int) {})
			for _, sig := range tc.signals {
				h.escalate(sig)
			}
			if got := buildStopGracePeriod(ctx); got != tc.want {
				t.Fatalf("expected %s, got %s", tc.want, got)
			}
		})
	}
	if got := buildStopGracePeriod(context.Background()); got != buildProcessGracePeriod {
		t.Fatalf("expected %s without interrupt handling (timeouts), got %s", buildProcessGracePeriod, got)
	}
}

func TestStopBuildProcessSecondInterruptShortensWait(t *testing.T) {
	stubStdinIsTerminal(t, true)
	execPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(execPath, "-test.run=^TestHelperProcessIgnoreTermination$", "--")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	configureCommandForCleanup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	h, ctx := newInterruptHandler(context.Background(), nil, func(int) {})
	h.escalate(os.Interrupt)
	start := time.Now()
	stopped := make(chan error, 1)
	go func() { stopped <- stopBuildProcess(ctx, cmd, commandProcessGroupID(cmd), done) }()

	time.Sleep(500 * time.Millisecond)
	h.escalate(os.Interrupt)

	select {
	case <-stopped:
		if elapsed := time.Since(start); elapsed >= interactiveBuildProcessGracePeriod {
			t.Fatalf("a second Ctrl+C must shorten the wait, took %s", elapsed)
		}
	case <-time.After(interactiveBuildProcessGracePeriod):
		t.Fatal("stopBuildProcess still waited for the full interactive grace period")
	}
}
