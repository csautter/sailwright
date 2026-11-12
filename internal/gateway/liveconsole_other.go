//go:build !windows

package gateway

import "os"

// EnableVirtualTerminal reports whether the terminal behind f processes VT
// sequences; every terminal on these hosts does, except TERM=dumb. Nothing needs
// to be restored.
func EnableVirtualTerminal(*os.File) (restore func(), ok bool) {
	return func() {}, os.Getenv("TERM") != "dumb"
}
