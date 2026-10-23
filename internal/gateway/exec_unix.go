//go:build unix

package gateway

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// processTree starts the child in a new session. That gives it its own process group,
// so cancellation reaches every descendant (Packer, Ansible, SSH, ...), and no
// controlling terminal: jobs can neither read the operator's approval console (where
// approvals and sudo passwords are typed) nor take over or reconfigure that terminal.
type processTree struct {
	cmd *exec.Cmd
}

func newProcessTree(cmd *exec.Cmd) *processTree {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return &processTree{cmd: cmd}
}

func (t *processTree) attach() error { return nil }

func (t *processTree) release() {}

func (t *processTree) kill() {
	t.signal(syscall.SIGKILL)
}

func (t *processTree) terminate(grace time.Duration, exited <-chan struct{}) {
	t.signal(syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(grace):
	}
	// Descendants may outlive the direct child; always finish the group.
	t.signal(syscall.SIGKILL)
}

func (t *processTree) signal(sig syscall.Signal) {
	if t.cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-t.cmd.Process.Pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		_ = t.cmd.Process.Signal(sig)
	}
}
