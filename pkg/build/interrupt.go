package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// ErrInterrupted is the cancellation cause of a build context after an interrupt
// signal (Ctrl+C, SIGTERM, SIGHUP or SIGQUIT).
var ErrInterrupted = errors.New("interrupted")

// interruptExitCode is the conventional exit code after SIGINT (128 + 2).
const interruptExitCode = 130

// Interrupt stages. Each Ctrl+C moves one stage up; SIGTERM, SIGHUP and SIGQUIT come
// from supervisors (the agent gateway, a closed terminal) that only wait a few seconds,
// so they skip post-processing right away.
const (
	interruptStageNone = iota
	// interruptStageCancel cancels all builds and downloads; no new build starts.
	interruptStageCancel
	// interruptStageSkipPostProcessing also cancels post-processing such as the VNC video.
	interruptStageSkipPostProcessing
	// interruptStageExit kills every tracked child process group and exits immediately.
	interruptStageExit
)

var interruptSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT}

type interruptHandlerKey struct{}

// interruptHandler turns repeated interrupt signals into escalating cancellation for
// everything that runs below one context: builds, downloads and their child processes.
type interruptHandler struct {
	mu          sync.Mutex
	stage       int
	cancelBuild context.CancelCauseFunc
	cancelPost  context.CancelCauseFunc
	postCtx     context.Context
	out         io.Writer
	exit        func(int)
}

func newInterruptHandler(parent context.Context, out io.Writer, exit func(int)) (*interruptHandler, context.Context) {
	h := &interruptHandler{out: out, exit: exit}
	buildCtx, cancelBuild := context.WithCancelCause(parent)
	postCtx, cancelPost := context.WithCancelCause(context.WithoutCancel(parent))
	h.cancelBuild = cancelBuild
	h.cancelPost = cancelPost
	h.postCtx = postCtx
	return h, context.WithValue(buildCtx, interruptHandlerKey{}, h)
}

// HandleInterrupts installs the signal handling for builds. The returned context is
// cancelled with ErrInterrupted on the first interrupt. A second Ctrl+C also cancels
// post-processing, a third one kills all child process groups and exits with code 130.
// The returned stop function restores the default signal behaviour.
func HandleInterrupts(parent context.Context) (context.Context, func()) {
	h, ctx := newInterruptHandler(parent, os.Stderr, os.Exit)
	sigs := make(chan os.Signal, len(interruptSignals))
	signal.Notify(sigs, interruptSignals...)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigs:
				h.escalate(sig)
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			signal.Stop(sigs)
			close(done)
			h.cancelBuild(context.Canceled)
			h.cancelPost(context.Canceled)
		})
	}
}

// ensureInterruptHandling returns ctx unchanged when it already carries interrupt
// handling, and installs it for the duration of the caller otherwise.
func ensureInterruptHandling(ctx context.Context) (context.Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	if interruptHandlerFromContext(ctx) != nil {
		return ctx, func() {}
	}
	return HandleInterrupts(ctx)
}

func interruptHandlerFromContext(ctx context.Context) *interruptHandler {
	if ctx == nil {
		return nil
	}
	h, _ := ctx.Value(interruptHandlerKey{}).(*interruptHandler)
	return h
}

// postProcessingContext returns the context for work that should still run after the
// build itself was cancelled (e.g. finishing the VNC video), but not after a second
// interrupt.
func postProcessingContext(ctx context.Context) context.Context {
	if h := interruptHandlerFromContext(ctx); h != nil {
		return h.postCtx
	}
	return context.Background()
}

// reportInterrupt handles an interrupt that reached a child process instead of this
// process, e.g. Ctrl+C while the child owned the terminal, as if it had arrived here.
func reportInterrupt(ctx context.Context) {
	if h := interruptHandlerFromContext(ctx); h != nil {
		h.escalate(os.Interrupt)
	}
}

// Interrupted reports whether ctx was cancelled because of an interrupt signal.
func Interrupted(ctx context.Context) bool {
	return ctx != nil && errors.Is(context.Cause(ctx), ErrInterrupted)
}

func (h *interruptHandler) escalate(sig os.Signal) {
	h.mu.Lock()
	previous := h.stage
	if sig == os.Interrupt {
		h.stage++
	} else {
		h.stage = max(h.stage+1, interruptStageSkipPostProcessing)
	}
	stage := h.stage
	h.mu.Unlock()

	if stage >= interruptStageCancel && previous < interruptStageCancel {
		if sig == os.Interrupt {
			h.printf("\n⚠️  Received %v: stopping all builds and downloads. Press Ctrl+C again to skip post-processing, a third time to exit immediately.\n", sig)
		} else {
			h.printf("\n⚠️  Received %v: stopping all builds and downloads.\n", sig)
		}
		h.cancelBuild(ErrInterrupted)
	}
	if stage >= interruptStageSkipPostProcessing && previous < interruptStageSkipPostProcessing {
		if previous >= interruptStageCancel {
			h.printf("\n⚠️  Received %v again: skipping post-processing. Press Ctrl+C once more to exit immediately.\n", sig)
		}
		h.cancelPost(ErrInterrupted)
	}
	if stage >= interruptStageExit {
		h.printf("\n⚠️  Received %v again: killing all child processes and exiting.\n", sig)
		killTrackedProcessGroups()
		h.exit(interruptExitCode)
	}
}

func (h *interruptHandler) printf(format string, args ...any) {
	if h.out != nil {
		_, _ = fmt.Fprintf(h.out, format, args...)
	}
}

// trackedProcessGroups holds the child process groups that a forced exit must kill,
// counted because two callers may track the same group.
var trackedProcessGroups = struct {
	sync.Mutex
	groups map[int]int
}{groups: map[int]int{}}

// trackProcessGroup registers a child process group for a forced exit and returns the
// function that removes it again once the group was waited for.
func trackProcessGroup(processGroupID int) func() {
	if processGroupID <= 0 {
		return func() {}
	}
	trackedProcessGroups.Lock()
	trackedProcessGroups.groups[processGroupID]++
	trackedProcessGroups.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			trackedProcessGroups.Lock()
			defer trackedProcessGroups.Unlock()
			trackedProcessGroups.groups[processGroupID]--
			if trackedProcessGroups.groups[processGroupID] <= 0 {
				delete(trackedProcessGroups.groups, processGroupID)
			}
		})
	}
}

func trackedProcessGroupIDs() []int {
	trackedProcessGroups.Lock()
	defer trackedProcessGroups.Unlock()
	ids := make([]int, 0, len(trackedProcessGroups.groups))
	for id := range trackedProcessGroups.groups {
		ids = append(ids, id)
	}
	return ids
}

func killTrackedProcessGroups() {
	for _, id := range trackedProcessGroupIDs() {
		killProcessGroup(id)
	}
}

// sleepContext waits for d or until ctx is cancelled.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
