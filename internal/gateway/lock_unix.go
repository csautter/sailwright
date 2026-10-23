//go:build !windows

package gateway

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var errLockHeld = errors.New("lock held")

func lockFile(file *os.File) error {
	// #nosec G115 -- file descriptors fit in int.
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return errLockHeld
		}
		return err
	}
	return nil
}
