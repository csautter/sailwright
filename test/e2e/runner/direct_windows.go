//go:build windows

package runner

import "os"

func terminateProcess(process *os.Process) error {
	return process.Kill()
}
