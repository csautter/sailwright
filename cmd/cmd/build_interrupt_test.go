package cmd

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

// TestParallelBuildsStartNothingAfterCancellation checks that a build that fails because
// of Ctrl+C does not free its slot for the next build: with one slot, the first build
// cancels the context and none of the others may start.
func TestParallelBuildsStartNothingAfterCancellation(t *testing.T) {
	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithCancelCause(context.Background())
		var started atomic.Int32
		runner := func(ctx context.Context, vm alchemy_build.VirtualMachineConfig) error {
			started.Add(1)
			cancel(alchemy_build.ErrInterrupted)
			return context.Cause(ctx)
		}

		errs := runParallelBuilds(ctx, testVMs(), 1, runner)
		if got := started.Load(); got != 1 {
			t.Fatalf("run %d: expected exactly one started build after cancellation, got %d", i, got)
		}
		if len(errs) != len(testVMs()) {
			t.Fatalf("run %d: expected an error per VM, got %v", i, errs)
		}
		for _, err := range errs {
			if !errors.Is(err, alchemy_build.ErrInterrupted) {
				t.Fatalf("run %d: expected interrupt errors, got %v", i, err)
			}
		}
	}
}
