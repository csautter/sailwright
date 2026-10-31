// Package operationlock coordinates CLI operations across processes.
package operationlock

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// Acquire locks a target, or all targets when target is empty. Lock files remain
// in place so another process cannot lock a replacement inode during release.
func Acquire(appData, target string) (func(), error) {
	dir := filepath.Join(appData, "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var files []*os.File
	release := func() {
		for i := len(files) - 1; i >= 0; i-- {
			_ = files[i].Close()
		}
	}
	take := func(name string, shared bool) error {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		if err := lockFile(f, shared); err != nil {
			_ = f.Close()
			return fmt.Errorf("resource busy (%s): %w", target, err)
		}
		files = append(files, f)
		return nil
	}
	if err := take("operations.lock", target != ""); err != nil {
		release()
		return nil, err
	}
	if target != "" {
		if err := take(fmt.Sprintf("%x.lock", sha256.Sum256([]byte(target))), false); err != nil {
			release()
			return nil, err
		}
	}
	return release, nil
}
