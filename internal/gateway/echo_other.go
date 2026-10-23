//go:build !linux && !darwin && !windows

package gateway

import "errors"

// TerminalInputHider is not available on this OS; approve-sudo is then refused.
func TerminalInputHider(int) InputHider {
	return func() (func(), error) {
		return nil, errors.New("hiding terminal input is not supported on this OS")
	}
}
