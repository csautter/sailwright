//go:build !windows

package gateway

import "os"

// EnableVirtualTerminal reports whether the terminal behind f processes VT
// sequences; every terminal on these hosts does, except TERM=dumb.
func EnableVirtualTerminal(*os.File) bool {
	return os.Getenv("TERM") != "dumb"
}
