//go:build windows

package gateway

import (
	"os"

	"golang.org/x/sys/windows"
)

// EnableVirtualTerminal turns on VT sequence processing for the console behind f.
// It reports false when the console cannot process them (old conhost). restore
// puts the previous console mode back; call it before the process exits.
func EnableVirtualTerminal(f *os.File) (restore func(), ok bool) {
	handle := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return func() {}, false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return func() {}, true
	}
	if err := windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return func() {}, false
	}
	return func() { _ = windows.SetConsoleMode(handle, mode) }, true
}
