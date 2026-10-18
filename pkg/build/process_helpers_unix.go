//go:build unix

package build

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func configureCommandForCleanup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func commandProcessGroupID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}

func terminateProcessGroup(processGroupID int, gracePeriod time.Duration) {
	if processGroupID <= 0 {
		return
	}
	if !signalProcessGroup(processGroupID, syscall.SIGTERM) {
		return
	}
	if gracePeriod > 0 {
		time.Sleep(gracePeriod)
	}
	_ = signalProcessGroup(processGroupID, syscall.SIGKILL)
}

func signalProcessGroup(processGroupID int, signal syscall.Signal) bool {
	err := syscall.Kill(-processGroupID, signal)
	return err == nil || !errors.Is(err, syscall.ESRCH)
}

func restoreInteractiveTerminal() {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer tty.Close()
	// Changing terminal settings from the background stops the process (SIGTTOU),
	// e.g. under `sailwright build ... &` or when started by another program.
	if !isForegroundProcessGroup(tty) {
		return
	}

	cmd := exec.Command("stty", "sane", "echo")
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	_ = cmd.Run()
}

func attachCommandToInteractiveTerminal(cmd *exec.Cmd) func() {
	if cmd == nil {
		return func() {}
	}

	if cmd.Stdin == nil {
		cmd.Stdin = os.Stdin
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return func() {}
	}
	// Only hand the terminal to the child when this process owns it; a background
	// process would be stopped by SIGTTOU when it takes the terminal back.
	if !isForegroundProcessGroup(tty) {
		_ = tty.Close()
		return func() {}
	}

	return configureCommandForInteractiveTerminal(cmd, tty, syscall.Getpgrp())
}

// isForegroundProcessGroup reports whether this process group is the foreground
// process group of tty.
var isForegroundProcessGroup = func(tty *os.File) bool {
	ttyFD, ok := fileDescriptorAsInt(tty)
	if !ok {
		return false
	}
	foreground, err := unix.IoctlGetInt(ttyFD, unix.TIOCGPGRP)
	return err == nil && foreground == syscall.Getpgrp()
}

func configureCommandForInteractiveTerminal(cmd *exec.Cmd, tty *os.File, parentProcessGroupID int) func() {
	if cmd == nil || tty == nil {
		return func() {}
	}

	ttyFD, ok := fileDescriptorAsInt(tty)
	if !ok {
		_ = tty.Close()
		return func() {}
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.Stdin = tty
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Foreground = true
	cmd.SysProcAttr.Ctty = ttyFD

	return func() {
		if parentProcessGroupID > 0 {
			// After the child's group took the terminal, this process is in the
			// background; like a shell, ignore SIGTTOU while taking the terminal back,
			// otherwise the kernel stops this process.
			signal.Ignore(syscall.SIGTTOU)
			_ = setTerminalForegroundProcessGroup(tty, parentProcessGroupID)
			signal.Reset(syscall.SIGTTOU)
		}
		_ = tty.Close()
	}
}

func setTerminalForegroundProcessGroup(tty *os.File, processGroupID int) error {
	if tty == nil || processGroupID <= 0 {
		return nil
	}

	ttyFD, ok := fileDescriptorAsInt(tty)
	if !ok {
		return errors.New("tty file descriptor exceeds int range")
	}

	return unix.IoctlSetPointerInt(ttyFD, unix.TIOCSPGRP, processGroupID)
}

func fileDescriptorAsInt(file *os.File) (int, bool) {
	if file == nil {
		return 0, false
	}

	fd := file.Fd()
	maxInt := int(^uint(0) >> 1)
	if fd > uintptr(maxInt) {
		return 0, false
	}

	// #nosec G115 -- os.File.Fd returns the process' native file descriptor width; the bounds check above keeps the cast explicit.
	return int(fd), true
}
