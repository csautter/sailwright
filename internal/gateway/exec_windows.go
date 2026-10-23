//go:build windows

package gateway

import (
	"os/exec"
	"time"

	"golang.org/x/sys/windows"
)

// processTree assigns the child to a Windows job object so cancellation terminates every
// descendant. The job is not kill-on-close: VMs started by a successful command keep
// running after the job handle is released.
type processTree struct {
	cmd *exec.Cmd
	job windows.Handle
}

func newProcessTree(cmd *exec.Cmd) *processTree {
	return &processTree{cmd: cmd}
}

func (t *processTree) attach() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(t.cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	t.job = job
	return nil
}

func (t *processTree) release() {
	if t.job != 0 {
		_ = windows.CloseHandle(t.job)
		t.job = 0
	}
}

func (t *processTree) kill() {
	if t.job != 0 {
		_ = windows.TerminateJobObject(t.job, 1)
		return
	}
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
}

func (t *processTree) terminate(_ time.Duration, _ <-chan struct{}) {
	// Windows has no portable graceful signal for console-less children.
	t.kill()
}
