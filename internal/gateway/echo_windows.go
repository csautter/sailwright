//go:build windows

package gateway

import "golang.org/x/sys/windows"

// TerminalInputHider turns off console echo while a password is typed.
func TerminalInputHider(fd int) InputHider {
	return func() (func(), error) {
		handle := windows.Handle(fd)
		var mode uint32
		if err := windows.GetConsoleMode(handle, &mode); err != nil {
			return nil, err
		}
		if err := windows.SetConsoleMode(handle, (mode&^windows.ENABLE_ECHO_INPUT)|windows.ENABLE_LINE_INPUT); err != nil {
			return nil, err
		}
		return func() { _ = windows.SetConsoleMode(handle, mode) }, nil
	}
}
