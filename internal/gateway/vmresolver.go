package gateway

import (
	"context"
	"errors"
	"net/netip"
)

// ManagedVM is a VM that sailwright created and that an agent may reach.
type ManagedVM struct {
	OS      string
	Type    string
	Arch    string
	Engine  string
	Address netip.Addr
}

// ManagedVMResolver resolves sailwright-managed VMs and their addresses. A future
// /tunnel endpoint will only connect to addresses returned here (never to addresses
// supplied by the agent), so the gateway cannot become a proxy into the host network.
type ManagedVMResolver interface {
	Resolve(ctx context.Context, osName, osType, arch string) (ManagedVM, error)
}

// ErrVMAccessUnavailable is returned until VM access is implemented.
var ErrVMAccessUnavailable = errors.New("VM access is not available in this gateway version")

// noVMResolver is the phase-1 resolver: VM access is not offered yet.
type noVMResolver struct{}

func (noVMResolver) Resolve(context.Context, string, string, string) (ManagedVM, error) {
	return ManagedVM{}, ErrVMAccessUnavailable
}
