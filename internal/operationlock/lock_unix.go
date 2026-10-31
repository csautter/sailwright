//go:build !windows

package operationlock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var errLockHeld = errors.New("lock held")

func lockFile(file *os.File, shared bool) error {
	// #nosec G115 -- file descriptors fit in int.
	mode := unix.LOCK_EX
	if shared {
		mode = unix.LOCK_SH
	}
	if err := unix.Flock(int(file.Fd()), mode|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return errLockHeld
		}
		return err
	}
	return nil
}
