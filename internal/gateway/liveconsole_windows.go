//go:build windows

package gateway

import (
	"os"

	"golang.org/x/sys/windows"
)

// EnableVirtualTerminal turns on VT sequence processing for the console behind f.
// It reports false when the console cannot process them (old conhost).
func EnableVirtualTerminal(f *os.File) bool {
	handle := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
