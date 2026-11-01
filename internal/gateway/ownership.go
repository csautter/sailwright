package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	ownershipFileName      = "owned_vms.json"
	ownershipSchemaVersion = 1
	// createdState is the `create list` value for a target whose VM exists.
	createdState  = "already created"
	riskForeignVM = "VM_NOT_CREATED_BY_GATEWAY"
	// busyProbe is how long requestVMAccess waits for an immediate "busy" answer.
	busyProbe = 200 * time.Millisecond
)

// ErrVMAccessPending reports that the operator has not yet decided an access request.
var ErrVMAccessPending = errors.New("VM access waits for operator approval")

// vmOwnership decides which VMs the agent may use without asking. With shared state
// the gateway and the operator manage the same VMs, so only VMs this gateway created
// (persisted across restarts) and VMs the operator granted (for this gateway run) are
// open to the agent. Isolated state holds only gateway VMs, so nothing is gated; a
// nil *vmOwnership is the same as disabled.
//
// `sailwright create` fails for an existing VM, so a successful create job proves the
// gateway made that VM. Ownership ends with a successful destroy job, or when a target
// listing shows the VM is gone (the operator destroyed it).
type vmOwnership struct {
	enabled bool
	path    string

	mu      sync.Mutex
	owned   map[string]ownedVM
	granted map[string]time.Time
	pending map[string]string // target -> approval id
}

type ownedVM struct {
	CreatedAt time.Time `json:"created_at"`
	JobID     string    `json:"job_id"`
}

type ownershipFile struct {
	SchemaVersion int                `json:"schema_version"`
	VMs           map[string]ownedVM `json:"vms"`
}

func vmKey(osName, osType, arch string) string {
	return osName + "/" + osType + "/" + arch
}

func loadVMOwnership(enabled bool, stateDir string) (*vmOwnership, error) {
	o := &vmOwnership{enabled: enabled, path: filepath.Join(stateDir, ownershipFileName), owned: map[string]ownedVM{}, granted: map[string]time.Time{}, pending: map[string]string{}}
	if !enabled {
		return o, nil
	}
	data, err := os.ReadFile(o.path)
	if errors.Is(err, os.ErrNotExist) {
		return o, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", o.path, err)
	}
	var file ownershipFile
	if err := json.Unmarshal(data, &file); err != nil || file.SchemaVersion != ownershipSchemaVersion {
		return nil, fmt.Errorf("%s is not a gateway VM ownership file (schema %d); remove it to forget which VMs the gateway created", o.path, ownershipSchemaVersion)
	}
	for key, vm := range file.VMs {
		o.owned[key] = vm
	}
	return o, nil
}

// allowed reports whether the agent may use the VM without operator approval.
func (o *vmOwnership) allowed(key string) bool {
	if o == nil || !o.enabled {
		return true
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	_, owned := o.owned[key]
	_, granted := o.granted[key]
	return owned || granted
}

func (o *vmOwnership) created(key, jobID string) error {
	if o == nil || !o.enabled {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.owned[key] = ownedVM{CreatedAt: time.Now().UTC(), JobID: jobID}
	return o.saveLocked()
}

func (o *vmOwnership) destroyed(key string) error {
	if o == nil || !o.enabled {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.granted, key)
	if _, ok := o.owned[key]; !ok {
		return nil
	}
	delete(o.owned, key)
	return o.saveLocked()
}

// prune forgets VMs that a `create list` listing, started at listedAt, reports as
// not created. Targets missing from the listing are kept, since a failed or partial
// listing proves nothing, and so are records newer than the listing, which may have
// been taken before the VM was created.
func (o *vmOwnership) prune(createTargets []Target, listedAt time.Time) error {
	if o == nil || !o.enabled {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	changed := false
	for _, target := range createTargets {
		key := vmKey(target.OS, target.Type, target.Arch)
		// Only a listing that says so proves the VM is gone.
		if state := target.Fields["create"]; state == "" || state == createdState {
			continue
		}
		if at, ok := o.granted[key]; ok && at.Before(listedAt) {
			delete(o.granted, key)
		}
		if vm, ok := o.owned[key]; ok && vm.CreatedAt.Before(listedAt) {
			delete(o.owned, key)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return o.saveLocked()
}

func (o *vmOwnership) saveLocked() error {
	data, err := json.MarshalIndent(ownershipFile{SchemaVersion: ownershipSchemaVersion, VMs: o.owned}, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFileAtomic(o.path, append(data, '\n'))
}

// beginRequest marks an access request as pending. It returns the id of a request
// that is already pending, or ok=true when the caller should ask the operator.
func (o *vmOwnership) beginRequest(key, id string) (pendingID string, ok bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if existing, found := o.pending[key]; found {
		return existing, false
	}
	o.pending[key] = id
	return id, true
}

func (o *vmOwnership) endRequest(key string, approved bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.pending, key)
	if approved {
		o.granted[key] = time.Now().UTC()
	}
}

// requestVMAccess asks the operator, in the background, to open a VM this gateway did
// not create to the agent. It returns almost at once (nil only if the request was
// decided as approved within busyProbe): an MCP call cannot wait for a human for the
// full approval TTL without running into client timeouts.
func (g *Gateway) requestVMAccess(tool, osName, osType, arch string) error {
	key := vmKey(osName, osType, arch)
	if !g.approver.Enabled() {
		return fmt.Errorf("VM %s was not created by this gateway and may belong to the operator; using it needs host approval, but the gateway has no approval console (start it in a foreground terminal, or create the VM through the gateway)", key)
	}
	id, err := newJobID()
	if err != nil {
		return err
	}
	if pending, ok := g.ownership.beginRequest(key, id); !ok {
		return fmt.Errorf("%w: request %s for VM %s is waiting on the host terminal; call %s again after the operator approves it", ErrVMAccessPending, pending, key, tool)
	}
	g.backgroundMu.Lock()
	if g.closed {
		g.backgroundMu.Unlock()
		g.ownership.endRequest(key, false)
		return errors.New("gateway is closing")
	}
	g.background.Add(1)
	g.backgroundMu.Unlock()
	argv := []string{"vm", "info", osName, "--arch", arch}
	if osType != "" {
		argv = append(argv, "--type", osType)
	}
	decided := make(chan Decision, 1)
	go func() {
		defer g.background.Done()
		ctx, cancel := context.WithCancel(g.backgroundCtx)
		defer cancel()
		approval := g.approver.Request(ctx, ApprovalRequest{
			ID: id, Tool: tool, Argv: argv, Dir: g.dirs.Work, BinarySHA: g.pinned.SHA256,
			Risk:    []string{riskForeignVM},
			NotSafe: fmt.Sprintf("VM %s was not created by this gateway and may be your own. Approving gives the agent its credentials and SSH/WinRM tunnels, and lets it start, stop, provision and destroy it until the gateway stops or the VM is destroyed", key),
		})
		approved := approval.Decision == DecisionApproved
		g.ownership.endRequest(key, approved)
		_ = g.audit.Record(AuditEvent{Event: "vm_access_decision", Tool: tool, JobID: id, Decision: string(approval.Decision), Params: map[string]any{"os": osName, "type": osType, "arch": arch}, Risk: []string{riskForeignVM}})
		decided <- approval.Decision
	}()
	// The console answers "busy" at once while another approval is open.
	select {
	case decision := <-decided:
		if decision == DecisionBusy {
			return fmt.Errorf("%w: VM %s was not created by this gateway and needs operator approval, but another approval is open on the host terminal; call %s again later", ErrVMAccessPending, key, tool)
		}
		if decision == DecisionApproved {
			return nil
		}
		return fmt.Errorf("operator access to VM %s: %s", key, decision)
	case <-time.After(busyProbe):
	}
	return fmt.Errorf("%w: VM %s was not created by this gateway, so the operator must approve request %s on the host terminal; call %s again after approval", ErrVMAccessPending, key, id, tool)
}
