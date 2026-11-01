package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"
)

// ManagedVM is a VM that sailwright created and that an agent may reach.
type ManagedVM struct {
	OS          string
	Type        string
	Arch        string
	Engine      string
	Address     netip.Addr
	NetworkMode string
	Protocol    string
	Port        int
	Username    string
	Password    string
}

// ManagedVMResolver resolves sailwright-managed VMs and their addresses. The
// /tunnel endpoint only connects to addresses returned here, never to addresses
// supplied by the agent. Some engines report addresses through the guest (for
// example the libvirt guest agent or Hyper-V integration services), so a guest
// controlled by the agent can influence them; allowedTunnelAddress and the port
// allowlist bound what such an address can reach.
type ManagedVMResolver interface {
	// Resolve may return a briefly cached result (repeated MCP calls).
	Resolve(ctx context.Context, osName, osType, arch string) (ManagedVM, error)
	// ResolveFresh always queries current state (tunnel creation).
	ResolveFresh(ctx context.Context, osName, osType, arch string) (ManagedVM, error)
}

// ErrVMAccessUnavailable reports that a managed guest cannot currently be reached.
var ErrVMAccessUnavailable = errors.New("managed VM access is unavailable")

// CLIManagedVMResolver asks only the pinned CLI for guest connection details.
// Lookups for different targets run concurrently; concurrent lookups for the same
// target share one CLI invocation.
type CLIManagedVMResolver struct {
	Runner   *Runner
	Work     string
	Redactor *Redactor
	mu       sync.Mutex
	cache    map[string]cachedVM
	inflight map[string]*vmLookup
}

type cachedVM struct {
	vm      ManagedVM
	expires time.Time
}

type vmLookup struct {
	done chan struct{}
	vm   ManagedVM
	err  error
}

const vmInfoCacheTTL = 2 * time.Second

func (r *CLIManagedVMResolver) Resolve(ctx context.Context, osName, osType, arch string) (ManagedVM, error) {
	return r.resolve(ctx, osName, osType, arch, true)
}

func (r *CLIManagedVMResolver) ResolveFresh(ctx context.Context, osName, osType, arch string) (ManagedVM, error) {
	return r.resolve(ctx, osName, osType, arch, false)
}

func (r *CLIManagedVMResolver) resolve(ctx context.Context, osName, osType, arch string, cached bool) (ManagedVM, error) {
	if !identifierPattern.MatchString(osName) || (osType != "" && !identifierPattern.MatchString(osType)) || !identifierPattern.MatchString(arch) {
		return ManagedVM{}, errors.New("invalid VM target")
	}
	key := osName + "/" + osType + "/" + arch
	r.mu.Lock()
	if value, ok := r.cache[key]; cached && ok && time.Now().Before(value.expires) {
		r.mu.Unlock()
		return value.vm, nil
	}
	// A lookup already in flight started no earlier than this request would, so
	// fresh callers may share it as well.
	lookup, ok := r.inflight[key]
	if !ok {
		lookup = &vmLookup{done: make(chan struct{})}
		if r.inflight == nil {
			r.inflight = map[string]*vmLookup{}
		}
		r.inflight[key] = lookup
		go r.query(key, lookup, osName, osType, arch)
	}
	r.mu.Unlock()
	select {
	case <-lookup.done:
		return lookup.vm, lookup.err
	case <-ctx.Done():
		return ManagedVM{}, ctx.Err()
	}
}

// query runs detached from any single caller so a cancelled request does not fail
// the other callers sharing the lookup; the CLI call has its own timeout.
func (r *CLIManagedVMResolver) query(key string, lookup *vmLookup, osName, osType, arch string) {
	lookup.vm, lookup.err = r.run(osName, osType, arch)
	r.mu.Lock()
	delete(r.inflight, key)
	if lookup.err == nil {
		if r.cache == nil {
			r.cache = map[string]cachedVM{}
		}
		r.cache[key] = cachedVM{lookup.vm, time.Now().Add(vmInfoCacheTTL)}
	} else {
		delete(r.cache, key)
	}
	r.mu.Unlock()
	close(lookup.done)
}

func (r *CLIManagedVMResolver) run(osName, osType, arch string) (ManagedVM, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"vm", "info", osName, "--arch", arch, "--json"}
	if osType != "" {
		args = append(args, "--type", osType)
	}
	var output limitedBuffer
	output.limit = maxInfoOutputBytes
	code, err := r.Runner.Run(ctx, args, r.Work, &output)
	if err != nil || code != 0 {
		return ManagedVM{}, fmt.Errorf("%w: VM is not running or vm info failed (exit %d)", ErrVMAccessUnavailable, code)
	}
	var value struct {
		SchemaVersion int    `json:"schema_version"`
		OS            string `json:"os"`
		Type          string `json:"type"`
		Arch          string `json:"arch"`
		Engine        string `json:"engine"`
		State         string `json:"state"`
		Address       string `json:"address"`
		NetworkMode   string `json:"network_mode"`
		Protocol      string `json:"protocol"`
		Port          int    `json:"port"`
		Username      string `json:"username"`
		Password      string `json:"password"`
	}
	if err := json.Unmarshal([]byte(output.String()), &value); err != nil {
		return ManagedVM{}, errors.New("invalid VM info response")
	}
	if value.SchemaVersion != 1 || value.OS != osName || value.Type != osType || value.Arch != arch || value.State != "running" {
		return ManagedVM{}, errors.New("unsupported VM info schema or mismatched target/state")
	}
	address, err := netip.ParseAddr(value.Address)
	if err != nil {
		return ManagedVM{}, errors.New("VM has no valid address")
	}
	if value.Protocol != "ssh" && value.Protocol != "winrm" {
		return ManagedVM{}, errors.New("unsupported guest protocol")
	}
	if value.Port < 1 || value.Port > 65535 {
		return ManagedVM{}, errors.New("invalid guest port")
	}
	// Register the password only for a validated response, before anyone can see it.
	if r.Redactor != nil {
		r.Redactor.AddRequired(value.Password)
	}
	return ManagedVM{OS: osName, Type: osType, Arch: arch, Engine: value.Engine, Address: address.Unmap(), NetworkMode: value.NetworkMode, Protocol: value.Protocol, Port: value.Port, Username: value.Username, Password: value.Password}, nil
}
