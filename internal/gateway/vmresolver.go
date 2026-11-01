package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
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
	// Context bounds all lookups to the owner lifetime; nil uses Background.
	Context  context.Context
	Runner   *Runner
	Work     string
	Redactor *Redactor
	// Console receives the reason of failed lookups for the operator; may be nil.
	Console  io.Writer
	mu       sync.Mutex
	cache    map[string]cachedVM
	inflight map[string]*vmLookup
	lifetime context.Context
	cancel   context.CancelFunc
	closed   bool
	workers  sync.WaitGroup
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
	if err := ctx.Err(); err != nil {
		return ManagedVM{}, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ManagedVM{}, context.Canceled
	}
	if r.lifetime == nil {
		parent := r.Context
		if parent == nil {
			parent = context.Background()
		}
		r.lifetime, r.cancel = context.WithCancel(parent)
	}
	if err := r.lifetime.Err(); err != nil {
		r.mu.Unlock()
		return ManagedVM{}, err
	}
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
		r.workers.Add(1)
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

// Close prevents new lookups, cancels CLI processes and waits for their exit.
func (r *CLIManagedVMResolver) Close() error {
	r.mu.Lock()
	r.closed = true
	if r.cancel != nil {
		r.cancel()
	}
	r.mu.Unlock()
	// Add happens under mu before closed is set, so no Add can race with Wait.
	r.workers.Wait()
	return nil
}

// query runs detached from any single caller so a cancelled request does not fail
// the other callers sharing the lookup; the owner lifetime and timeout bound it.
func (r *CLIManagedVMResolver) query(key string, lookup *vmLookup, osName, osType, arch string) {
	defer r.workers.Done()
	lookup.vm, lookup.err = r.run(r.lifetime, osName, osType, arch)
	if lookup.err != nil && r.Console != nil {
		fmt.Fprintf(r.Console, "vm info %s failed: %s\n", key, sanitizeForTerminal(lookup.err.Error()))
	}
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

func (r *CLIManagedVMResolver) run(parent context.Context, osName, osType, arch string) (ManagedVM, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	args := []string{"vm", "info", osName, "--arch", arch, "--json"}
	if osType != "" {
		args = append(args, "--type", osType)
	}
	var output limitedBuffer
	output.limit = maxInfoOutputBytes
	code, err := r.Runner.Run(ctx, args, r.Work, &output)
	if err != nil {
		return ManagedVM{}, fmt.Errorf("%w: vm info did not run: %s", ErrVMAccessUnavailable, r.redact(err.Error()))
	}
	if code != 0 {
		return ManagedVM{}, fmt.Errorf("%w: vm info exited with %d: %s", ErrVMAccessUnavailable, code, r.redact(errorSummary(output.String())))
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
	if err := json.Unmarshal([]byte(lastJSONLine(output.String())), &value); err != nil {
		return ManagedVM{}, errors.New("invalid VM info response (no JSON document)")
	}
	if value.SchemaVersion != 1 {
		return ManagedVM{}, fmt.Errorf("unsupported VM info schema %d", value.SchemaVersion)
	}
	if value.OS != osName || value.Type != osType || value.Arch != arch {
		return ManagedVM{}, fmt.Errorf("VM info answered for %s/%s/%s", value.OS, value.Type, value.Arch)
	}
	if value.State != "running" {
		return ManagedVM{}, fmt.Errorf("%w: VM state is %q", ErrVMAccessUnavailable, value.State)
	}
	address, err := netip.ParseAddr(value.Address)
	if err != nil {
		return ManagedVM{}, fmt.Errorf("VM has no valid address (%q)", truncate(value.Address, 64))
	}
	if value.Protocol != "ssh" && value.Protocol != "winrm" {
		return ManagedVM{}, fmt.Errorf("unsupported guest protocol %q", value.Protocol)
	}
	if value.Port < 1 || value.Port > 65535 {
		return ManagedVM{}, fmt.Errorf("invalid guest port %d", value.Port)
	}
	// Register the password only for a validated response, before anyone can see it.
	if r.Redactor != nil {
		r.Redactor.AddRequired(value.Password)
	}
	return ManagedVM{OS: osName, Type: osType, Arch: arch, Engine: value.Engine, Address: address.Unmap(), NetworkMode: value.NetworkMode, Protocol: value.Protocol, Port: value.Port, Username: value.Username, Password: value.Password}, nil
}

// redact hides known secrets and bounds a CLI error for agent and console.
func (r *CLIManagedVMResolver) redact(value string) string {
	if r.Redactor != nil {
		value = r.Redactor.String(value)
	}
	return truncate(value, maxLookupErrorLength)
}

// maxLookupErrorLength bounds the CLI error text passed on from a failed lookup.
const maxLookupErrorLength = 300

// lastJSONLine returns the last line that holds a JSON object; the CLI may print
// warnings (merged from stderr) before the document.
func lastJSONLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); strings.HasPrefix(line, "{") {
			return line
		}
	}
	return ""
}
