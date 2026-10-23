package gateway

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// StateLockName is the lock file in the state dir that a running gateway holds.
const StateLockName = "gateway.lock"

// ErrStateLocked means another process holds the state dir lock.
var ErrStateLocked = errors.New("another gateway is using this state dir")

// StateLock is an exclusive advisory lock on a gateway state dir. The operating system
// releases it when the process exits, so a crash never leaves a stale lock.
type StateLock struct {
	file *os.File
}

// AcquireStateLock takes the lock without blocking. It returns ErrStateLocked if
// another process holds it.
func AcquireStateLock(stateDir string) (*StateLock, error) {
	if err := ensurePrivateDir(stateDir); err != nil {
		return nil, err
	}
	path := filepath.Join(stateDir, StateLockName)
	// #nosec G304 -- path is inside the operator-configured state dir.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open state lock: %w", err)
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, errLockHeld) {
			return nil, fmt.Errorf("%w (%s)", ErrStateLocked, path)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return &StateLock{file: file}, nil
}

// Release drops the lock. It is safe on a nil lock.
func (l *StateLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = l.file.Close() // closing the handle releases the lock on every platform
	l.file = nil
}
