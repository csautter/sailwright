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

func TestHostSupportsVncViewer(t *testing.T) {
	testCases := []struct {
		goos string
		want bool
	}{
		{goos: "darwin", want: true},
		{goos: "linux", want: false},
		{goos: "windows", want: false},
	}

	for _, tc := range testCases {
		if got := hostSupportsVncViewer(tc.goos); got != tc.want {
			t.Fatalf("hostSupportsVncViewer(%q) = %v, want %v", tc.goos, got, tc.want)
		}
	}
}

func TestBuildScriptTimeoutAllowsFiveHourBuilds(t *testing.T) {
	if buildScriptTimeout != 5*time.Hour {
		t.Fatalf("expected build script timeout to be 5h, got %s", buildScriptTimeout)
	}
}

func TestDetermineBuildCompletionDecisionSuccess(t *testing.T) {
	decision := determineBuildCompletionDecision(nil, nil, false)
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
	decision := determineBuildCompletionDecision(waitErr, nil, false)
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
	decision := determineBuildCompletionDecision(nil, context.Canceled, false)
	if !errors.Is(decision.err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", decision.err)
	}
	if decision.runFfmpeg {
		t.Fatal("expected ffmpeg post-processing to be skipped after cancellation")
	}
}

func TestDetermineBuildCompletionDecisionKeepsFfmpegOnInterrupt(t *testing.T) {
	decision := determineBuildCompletionDecision(errors.New("exit status 1"), context.Canceled, true)
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

func TestDetermineBuildCompletionDecisionInterruptWinsOverSuccess(t *testing.T) {
	// The build may exit 0 although Ctrl+C was pressed; it must not be promoted.
	decision := determineBuildCompletionDecision(nil, nil, true)
	if decision.buildSuccess || decision.err == nil {
		t.Fatalf("expected interrupted build to fail, got %+v", decision)
	}
}

func TestDeferBuildArtifactCleanupUsesFinalBuildSuccessValue(t *testing.T) {
	got := false

	func() {
		buildSucceeded := false
		defer deferBuildArtifactCleanup(func(success bool) error {
			got = success
			return nil
		}, &buildSucceeded)()

		buildSucceeded = true
	}()

	if !got {
		t.Fatal("expected deferred cleanup to observe final successful build state")
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
