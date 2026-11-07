package build

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func availableDiskBytes(dir string) (uint64, error) {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free, total, available uint64
	err = windows.GetDiskFreeSpaceEx(path, &available, &total, &free)
	return available, err
}

func availableMemoryMB() (uint64, error) {
	var mem memoryStatusEx
	mem.dwLength = uint32(unsafe.Sizeof(mem))
	ret, _, err := procGlobalMemStatus.Call(uintptr(unsafe.Pointer(&mem)))
	if ret == 0 {
		return 0, fmt.Errorf("GlobalMemoryStatusEx failed: %w", err)
	}
	return mem.ullAvailPhys / (1 << 20), nil
}
