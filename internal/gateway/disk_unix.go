//go:build unix

package gateway

import "golang.org/x/sys/unix"

// freeDiskBytes returns the bytes available to the current user on dir's filesystem.
func freeDiskBytes(dir string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil // #nosec G115 -- block size is positive.
}
