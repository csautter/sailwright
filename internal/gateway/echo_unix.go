//go:build linux || darwin

package gateway

import "golang.org/x/sys/unix"

// TerminalInputHider turns off echo on the terminal fd while a password is typed.
// The terminal stays in line mode, so the approval console keeps reading whole lines.
func TerminalInputHider(fd int) InputHider {
	return func() (func(), error) {
		state, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
		if err != nil {
			return nil, err
		}
		hidden := *state
		hidden.Lflag &^= unix.ECHO
		hidden.Lflag |= unix.ICANON | unix.ISIG
		if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &hidden); err != nil {
			return nil, err
		}
		return func() { _ = unix.IoctlSetTermios(fd, ioctlSetTermios, state) }, nil
	}
}
