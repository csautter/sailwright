package build

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
)

func newTestInterruptHandler(t *testing.T) (*interruptHandler, context.Context, *[]int) {
	t.Helper()
	var exits []int
	h, ctx := newInterruptHandler(context.Background(), nil, func(code int) { exits = append(exits, code) })
	return h, ctx, &exits
}

func TestInterruptEscalatesWithEachCtrlC(t *testing.T) {
	h, ctx, exits := newTestInterruptHandler(t)
	post := postProcessingContext(ctx)

	h.escalate(os.Interrupt)
	if !Interrupted(ctx) {
		t.Fatalf("first Ctrl+C must cancel the build context with ErrInterrupted, got %v", context.Cause(ctx))
	}
	if post.Err() != nil {
		t.Fatal("first Ctrl+C must keep post-processing running")
	}

	h.escalate(os.Interrupt)
	if !errors.Is(context.Cause(post), ErrInterrupted) {
		t.Fatalf("second Ctrl+C must cancel post-processing, got %v", context.Cause(post))
	}
	if len(*exits) != 0 {
		t.Fatalf("second Ctrl+C must not exit, got exits %v", *exits)
	}

	h.escalate(os.Interrupt)
	if len(*exits) != 1 || (*exits)[0] != interruptExitCode {
		t.Fatalf("third Ctrl+C must exit with %d, got %v", interruptExitCode, *exits)
	}
}

func TestInterruptBySupervisorSignalSkipsPostProcessing(t *testing.T) {
	for _, sig := range []os.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		h, ctx, exits := newTestInterruptHandler(t)
		h.escalate(sig)
		if !Interrupted(ctx) {
			t.Fatalf("%v must cancel the build context", sig)
		}
		if postProcessingContext(ctx).Err() == nil {
			t.Fatalf("%v must cancel post-processing right away", sig)
		}
		if len(*exits) != 0 {
			t.Fatalf("a single %v must not exit, got %v", sig, *exits)
		}
		h.escalate(sig)
		if len(*exits) != 1 {
			t.Fatalf("a second %v must exit, got %v", sig, *exits)
		}
	}
}

func TestReportInterruptCountsAsCtrlC(t *testing.T) {
	_, ctx, _ := newTestInterruptHandler(t)
	reportInterrupt(ctx)
	if !Interrupted(ctx) {
		t.Fatal("an interrupt reported by a build command must cancel all builds")
	}
}

func TestEnsureInterruptHandlingReusesHandler(t *testing.T) {
	_, ctx, _ := newTestInterruptHandler(t)
	got, stop := ensureInterruptHandling(ctx)
	defer stop()
	if got != ctx {
		t.Fatal("expected the context with interrupt handling to be reused")
	}

	plain, stopPlain := ensureInterruptHandling(context.Background())
	defer stopPlain()
	if interruptHandlerFromContext(plain) == nil {
		t.Fatal("expected interrupt handling to be installed for a plain context")
	}
}

func TestPostProcessingContextWithoutHandlerIsNotCancelled(t *testing.T) {
	if postProcessingContext(context.Background()).Err() != nil {
		t.Fatal("expected a live post-processing context without interrupt handling")
	}
}

func TestPostProcessingContextReusesHandler(t *testing.T) {
	h, ctx := newInterruptHandler(context.Background(), nil, func(int) {})
	postCtx := postProcessingContext(ctx)
	if got := interruptHandlerFromContext(postCtx); got != h {
		t.Fatal("expected the post-processing context to carry the build's interrupt handler")
	}
	// Helper processes started for post-processing (ffmpeg) must not install a second
	// signal handler.
	reused, stop := ensureInterruptHandling(postCtx)
	defer stop()
	if reused != postCtx {
		t.Fatal("expected ensureInterruptHandling to reuse the post-processing context")
	}
}

func TestTrackProcessGroupCountsRegistrations(t *testing.T) {
	const id = 987654321
	untrackA := trackProcessGroup(id)
	untrackB := trackProcessGroup(id)
	untrackA()
	untrackA()
	if !containsInt(trackedProcessGroupIDs(), id) {
		t.Fatal("process group must stay tracked while one registration is left")
	}
	untrackB()
	if containsInt(trackedProcessGroupIDs(), id) {
		t.Fatal("process group must be untracked after the last registration")
	}
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
