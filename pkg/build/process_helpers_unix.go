//go:build unix

package build

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"sync"
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

// terminateProcessGroup sends SIGTERM to the process group and SIGKILL once the grace
// period is over and the group still has members.
func terminateProcessGroup(processGroupID int, gracePeriod time.Duration) {
	if processGroupID <= 0 {
		return
	}
	if !signalProcessGroup(processGroupID, syscall.SIGTERM) {
		return
	}
	// A stopped process (e.g. after SIGTTIN) only acts on SIGTERM once it continues.
	_ = signalProcessGroup(processGroupID, syscall.SIGCONT)
	deadline := time.Now().Add(gracePeriod)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		if !signalProcessGroup(processGroupID, 0) {
			return
		}
	}
	_ = signalProcessGroup(processGroupID, syscall.SIGKILL)
}

func killProcessGroup(processGroupID int) {
	if processGroupID > 0 {
		_ = signalProcessGroup(processGroupID, syscall.SIGKILL)
	}
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

// terminalInterruptWrapper runs a build command that owns the terminal. Ctrl+C then
// reaches only the command's process group, and Packer exits with status 1 after
// handling SIGINT, so the wrapper remembers the SIGINT and exits with 130 instead. The
// shell runs the trap only after the foreground command finished (POSIX), so the
// command still handles the signal itself.
const terminalInterruptWrapper = `trap 'interrupted=1' INT
"$@"
status=$?
if [ -n "$interrupted" ]; then exit 130; fi
exit "$status"`

// commandTerminal tells how a build command uses the terminal.
type commandTerminal struct {
	// attached is true when the command owns the terminal and Ctrl+C reaches it
	// instead of this process.
	attached bool
	restore  func()
}

// newBuildCommand creates the command of a build. A single build gets the terminal,
// so prompts such as sudo work. With detached (parallel builds) the command runs in a
// session of its own without a terminal: Ctrl+C reaches this process, and a prompt
// fails instead of stopping the build with SIGTTIN.
func newBuildCommand(executable string, args []string, detached bool) (*exec.Cmd, commandTerminal) {
	noTerminal := commandTerminal{restore: func() {}}
	if detached {
		// #nosec G204 -- executable and args are constructed by internal build flows; no shell is invoked.
		cmd := exec.Command(executable, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		return cmd, noTerminal
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err == nil && isForegroundProcessGroup(tty) {
		wrapperArgs := append([]string{"-c", terminalInterruptWrapper, "sailwright-build", executable}, args...)
		// #nosec G204 -- the wrapper script is a constant; executable and args are passed as positional parameters.
		cmd := exec.Command("/bin/sh", wrapperArgs...)
		restore := configureCommandForInteractiveTerminal(cmd, tty, syscall.Getpgrp())
		var once sync.Once
		return cmd, commandTerminal{attached: true, restore: func() { once.Do(restore) }}
	}
	if tty != nil {
		_ = tty.Close()
	}

	// #nosec G204 -- executable and args are constructed by internal build flows; no shell is invoked.
	cmd := exec.Command(executable, args...)
	configureCommandForCleanup(cmd)
	cmd.Stdin = os.Stdin
	return cmd, noTerminal
}

// isTerminalInterruptExit reports whether a command that owned the terminal ended
// because of Ctrl+C.
func isTerminalInterruptExit(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() && status.Signal() == syscall.SIGINT {
		return true
	}
	return exitErr.ExitCode() == interruptExitCode
}

// requestProcessGroupStop asks a process group to stop with SIGTERM. A stopped process
// (e.g. after SIGTTIN) only acts on SIGTERM once it continues, hence the SIGCONT.
func requestProcessGroupStop(processGroupID int) {
	if processGroupID > 0 && signalProcessGroup(processGroupID, syscall.SIGTERM) {
		_ = signalProcessGroup(processGroupID, syscall.SIGCONT)
	}
}
