package gateway

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

const (
	terminateGracePeriod = 10 * time.Second
	// outputWaitDelay bounds how long Wait keeps reading output after the process
	// exited, e.g. when a detached VM process inherited stdout.
	outputWaitDelay = 30 * time.Second
)

// Runner executes the pinned sailwright binary. It never uses a shell: the argv slice
// is passed to the OS as-is, stdin is closed, and the environment is built from scratch.
type Runner struct {
	Binary PinnedBinary
	Env    []string
}

// Run executes argv in dir, streaming combined output to out. Cancelling ctx
// terminates the whole process tree. It returns the exit code (or -1 when the process
// could not be started or was killed by a signal).
func (r *Runner) Run(ctx context.Context, argv []string, dir string, out io.Writer) (int, error) {
	if err := r.Binary.Verify(); err != nil {
		return -1, err
	}

	// #nosec G204 -- the binary is the pinned copy and argv is passed without a shell.
	cmd := exec.Command(r.Binary.Path, argv...)
	cmd.Dir = dir
	cmd.Env = r.Env
	cmd.Stdin = nil
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = outputWaitDelay
	tree := newProcessTree(cmd)

	if err := cmd.Start(); err != nil {
		return -1, err
	}
	if err := tree.attach(); err != nil {
		tree.kill()
		_ = cmd.Wait()
		return -1, err
	}

	var waitErr error
	exited := make(chan struct{})
	go func() {
		waitErr = cmd.Wait()
		close(exited)
	}()

	select {
	case <-exited:
		tree.release()
		return exitCodeOf(waitErr), nil
	case <-ctx.Done():
		tree.terminate(terminateGracePeriod, exited)
		<-exited
		tree.release()
		return exitCodeOf(waitErr), ctx.Err()
	}
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
