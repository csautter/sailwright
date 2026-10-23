//go:build windows

package gateway

import "golang.org/x/sys/windows"

// freeDiskBytes returns the bytes available to the current user on dir's volume.
func freeDiskBytes(dir string) (uint64, error) {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(path, &available, &total, &free); err != nil {
		return 0, err
	}
	return available, nil
}
