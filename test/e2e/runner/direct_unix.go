//go:build unix

package runner

import (
	"os"
	"syscall"
)

func terminateProcess(process *os.Process) error {
	return process.Signal(syscall.SIGTERM)
}
