//go:build windows

package build

import (
	"os/exec"
	"time"
)

func configureCommandForCleanup(cmd *exec.Cmd) {}

func commandProcessGroupID(cmd *exec.Cmd) int {
	return 0
}

func terminateProcessGroup(processGroupID int, gracePeriod time.Duration) {}

func restoreInteractiveTerminal() {}

func attachCommandToInteractiveTerminal(cmd *exec.Cmd) func() {
	return func() {}
}

func killProcessGroup(processGroupID int) {}

type commandTerminal struct {
	attached bool
	restore  func()
}

func newBuildCommand(executable string, args []string, detached bool) (*exec.Cmd, commandTerminal) {
	// #nosec G204 -- executable and args are constructed by internal build flows; no shell is invoked.
	return exec.Command(executable, args...), commandTerminal{restore: func() {}}
}

func terminalCommandExit(err error) (waitErr error, interrupted bool) {
	return err, false
}

// requestProcessGroupStop does nothing on Windows: there is no process group to signal,
// and a Ctrl+C in the console reaches the build directly.
func requestProcessGroupStop(processGroupID int) {}
