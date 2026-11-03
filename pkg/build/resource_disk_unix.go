//go:build unix

package build

import "golang.org/x/sys/unix"

func availableDiskBytes(dir string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil // #nosec G115 -- filesystem sizes are positive.
}
