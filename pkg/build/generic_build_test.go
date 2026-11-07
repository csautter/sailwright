package build

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestHostSupportsVncRecording(t *testing.T) {
	testCases := []struct {
		goos string
		want bool
	}{
		{goos: "darwin", want: true},
		{goos: "linux", want: true},
		{goos: "windows", want: false},
	}

	for _, tc := range testCases {
		if got := hostSupportsVncRecording(tc.goos); got != tc.want {
			t.Fatalf("hostSupportsVncRecording(%q) = %v, want %v", tc.goos, got, tc.want)
		}
	}
}

func TestShouldOpenVncViewer(t *testing.T) {
	testCases := []struct {
		name     string
		goos     string
		headless bool
		want     bool
	}{
		{name: "darwin/windowed", goos: "darwin", headless: false, want: true},
		{name: "darwin/headless", goos: "darwin", headless: true, want: false},
		{name: "linux/windowed", goos: "linux", headless: false, want: false},
		{name: "linux/headless", goos: "linux", headless: true, want: false},
		{name: "windows/windowed", goos: "windows", headless: false, want: false},
		{name: "windows/headless", goos: "windows", headless: true, want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldOpenVncViewer(tc.goos, tc.headless); got != tc.want {
				t.Fatalf("shouldOpenVncViewer(%q, %v) = %v, want %v", tc.goos, tc.headless, got, tc.want)
			}
		})
	}
}

func TestBuildScriptTimeoutAllowsFiveHourBuilds(t *testing.T) {
	if buildScriptTimeout != 5*time.Hour {
		t.Fatalf("expected build script timeout to be 5h, got %s", buildScriptTimeout)
	}
}

func TestDetermineBuildCompletionDecisionSuccess(t *testing.T) {
	decision := determineBuildCompletionDecision(nil, false, nil, false)
	if decision.err != nil {
		t.Fatalf("expected nil error, got %v", decision.err)
	}
	if !decision.runFfmpeg {
		t.Fatal("expected ffmpeg post-processing to run on success")
	}
	if !decision.buildSuccess {
		t.Fatal("expected build to be marked successful")
	}
}

func TestDetermineBuildCompletionDecisionFailureKeepsFfmpeg(t *testing.T) {
	waitErr := errors.New("build failed")
	decision := determineBuildCompletionDecision(waitErr, false, nil, false)
	if !errors.Is(decision.err, waitErr) {
		t.Fatalf("expected wait error to be returned, got %v", decision.err)
	}
	if !decision.runFfmpeg {
		t.Fatal("expected ffmpeg post-processing to run on ordinary build failure")
	}
	if decision.buildSuccess {
		t.Fatal("expected failed build to stay unsuccessful")
	}
}

func TestDetermineBuildCompletionDecisionSkipsFfmpegOnContextCancellation(t *testing.T) {
	decision := determineBuildCompletionDecision(nil, true, context.Canceled, false)
	if !errors.Is(decision.err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", decision.err)
	}
	if decision.runFfmpeg {
		t.Fatal("expected ffmpeg post-processing to be skipped after cancellation")
	}
}

func TestDetermineBuildCompletionDecisionKeepsFfmpegOnInterrupt(t *testing.T) {
	decision := determineBuildCompletionDecision(errors.New("exit status 1"), true, context.Canceled, true)
	if !errors.Is(decision.err, ErrInterrupted) {
		t.Fatalf("expected interrupt error, got %v", decision.err)
	}
	if !decision.runFfmpeg {
		t.Fatal("expected ffmpeg post-processing to run after an interrupt")
	}
	if decision.buildSuccess {
		t.Fatal("expected interrupted build to stay unsuccessful")
	}
}

func TestDetermineBuildCompletionDecisionKeepsBuildThatFinishedBeforeInterrupt(t *testing.T) {
	// The build exited 0 on its own; a Ctrl+C right afterwards must not discard it.
	decision := determineBuildCompletionDecision(nil, false, context.Canceled, true)
	if !decision.buildSuccess || decision.err != nil {
		t.Fatalf("expected the finished build to be kept, got %+v", decision)
	}
}

func TestDetermineBuildCompletionDecisionDiscardsStoppedBuildThatExitedZero(t *testing.T) {
	// A build that exits 0 after sailwright asked it to stop may be incomplete (e.g. a
	// script that ignores the failure of the Packer it ran); it must not be promoted.
	decision := determineBuildCompletionDecision(nil, true, context.Canceled, true)
	if decision.buildSuccess || decision.err == nil {
		t.Fatalf("expected the stopped build to fail, got %+v", decision)
	}
}

func TestLogBuildOutputLineEnablesAuxiliaryProcessSilenceOnMarker(t *testing.T) {
	t.Parallel()

	auxiliaryProcessSilent := &atomic.Bool{}

	logBuildOutputLine(auxiliaryLogSilenceStartMarker, "stdout", VirtualMachineConfig{}, auxiliaryProcessSilent)

	if !auxiliaryProcessSilent.Load() {
		t.Fatal("expected auxiliary process silence to be enabled")
	}
}

func TestLogBuildOutputLineDisablesAuxiliaryProcessSilenceOnMarker(t *testing.T) {
	t.Parallel()

	auxiliaryProcessSilent := &atomic.Bool{}
	auxiliaryProcessSilent.Store(true)

	logBuildOutputLine(auxiliaryLogSilenceEndMarker, "stdout", VirtualMachineConfig{}, auxiliaryProcessSilent)

	if auxiliaryProcessSilent.Load() {
		t.Fatal("expected auxiliary process silence to be disabled")
	}
}
