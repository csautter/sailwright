//go:build windows

package operationlock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

var errLockHeld = errors.New("lock held")

func lockFile(file *os.File, shared bool) error {
	overlapped := new(windows.Overlapped)
	mode := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if !shared {
		mode |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err := windows.LockFileEx(windows.Handle(file.Fd()), mode, 0, 1, 0, overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errLockHeld
	}
	return err
}
