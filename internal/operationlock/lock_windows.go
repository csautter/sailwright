//go:build windows

package operationlock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockOffsetHigh places the locked byte at 2^62, far beyond any holder description.
// Windows byte-range locks are mandatory, so locking byte 0 would make the holder
// description unreadable to the processes it is meant for.
const lockOffsetHigh = 1 << 30

func lockRange() *windows.Overlapped {
	return &windows.Overlapped{OffsetHigh: lockOffsetHigh}
}

func lockFile(file *os.File, shared bool) error {
	mode := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if !shared {
		mode |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err := windows.LockFileEx(windows.Handle(file.Fd()), mode, 0, 1, 0, lockRange())
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrBusy
	}
	return err
}

// unlockFile releases the lock explicitly: Windows may release a lock some time
// after the handle is closed, which would refuse the next process spuriously.
func unlockFile(file *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, lockRange())
}
