package gateway

import (
	"context"
	"encoding/json"
	"errors"
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
// /tunnel endpoint only connects to addresses returned here (never to addresses
// supplied by the agent), so the gateway cannot become a proxy into the host network.
type ManagedVMResolver interface {
	Resolve(ctx context.Context, osName, osType, arch string) (ManagedVM, error)
}

// ErrVMAccessUnavailable reports that a managed guest cannot currently be reached.
var ErrVMAccessUnavailable = errors.New("managed VM access is unavailable")

// CLIManagedVMResolver asks only the pinned CLI for guest connection details.
type CLIManagedVMResolver struct {
	Runner   *Runner
	Work     string
	Redactor *Redactor
	mu       sync.Mutex
	cache    map[string]cachedVM
}

type cachedVM struct {
	vm      ManagedVM
	expires time.Time
}

func (r *CLIManagedVMResolver) Resolve(ctx context.Context, osName, osType, arch string) (ManagedVM, error) {
	return r.resolve(ctx, osName, osType, arch, true)
}

func (r *CLIManagedVMResolver) resolve(ctx context.Context, osName, osType, arch string, cached bool) (ManagedVM, error) {
	if !identifierPattern.MatchString(osName) || (osType != "" && !identifierPattern.MatchString(osType)) || !identifierPattern.MatchString(arch) {
		return ManagedVM{}, errors.New("invalid VM target")
	}
	key := osName + "/" + osType + "/" + arch
	r.mu.Lock()
	defer r.mu.Unlock()
	if value, ok := r.cache[key]; cached && ok && time.Now().Before(value.expires) {
		return value.vm, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"vm", "info", osName, "--arch", arch, "--json"}
	if osType != "" {
		args = append(args, "--type", osType)
	}
	var output limitedBuffer
	output.limit = maxInfoOutputBytes
	code, err := r.Runner.Run(ctx, args, r.Work, &output)
	if err != nil || code != 0 {
		return ManagedVM{}, errors.New("managed VM is unavailable or not running")
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
	if r.Redactor != nil {
		r.Redactor.AddRequired(value.Password)
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
	vm := ManagedVM{OS: osName, Type: osType, Arch: arch, Engine: value.Engine, Address: address.Unmap(), NetworkMode: value.NetworkMode, Protocol: value.Protocol, Port: value.Port, Username: value.Username, Password: value.Password}
	if r.cache == nil {
		r.cache = map[string]cachedVM{}
	}
	r.cache[key] = cachedVM{vm, time.Now().Add(2 * time.Second)}
	return vm, nil
}
