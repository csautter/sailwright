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

// createStartedFileEnv names the file the CLI's create writes once it has checked,
// under the target lock, that no VM exists and deployment begins.
const createStartedFileEnv = "SAILWRIGHT_CREATE_STARTED_FILE"

const (
	ownershipFileName      = "owned_vms.json"
	ownershipSchemaVersion = 1
	// legacyCreatedState is the `create list` display value for a target whose VM
	// exists. Only listings without the structured `exists` field rely on it.
	legacyCreatedState = "already created"
	riskForeignVM      = "VM_NOT_CREATED_BY_GATEWAY"
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
// gateway made that VM; a failed create counts too once the CLI reports, under its
// target lock, that deployment began for a VM that did not exist. Ownership ends with
// a successful destroy job, or when a target listing shows the VM is gone (the
// operator destroyed it).
//
// Records carry the hypervisor's instance id where the engine has one, so a VM that
// was destroyed and recreated under the same name between two listings is noticed
// (confirm) instead of being taken for the gateway's.
type vmOwnership struct {
	enabled bool
	path    string

	mu      sync.Mutex
	owned   map[string]ownedVM
	granted map[string]grantedVM
	pending map[string]string // target -> approval id
}

type ownedVM struct {
	CreatedAt  time.Time `json:"created_at"`
	JobID      string    `json:"job_id"`
	InstanceID string    `json:"instance_id,omitempty"`
}

type grantedVM struct {
	at         time.Time
	instanceID string
}

type ownershipFile struct {
	SchemaVersion int                `json:"schema_version"`
	VMs           map[string]ownedVM `json:"vms"`
}

func vmKey(osName, osType, arch string) string {
	return osName + "/" + osType + "/" + arch
}

func loadVMOwnership(enabled bool, stateDir string) (*vmOwnership, error) {
	o := &vmOwnership{enabled: enabled, path: filepath.Join(stateDir, ownershipFileName), owned: map[string]ownedVM{}, granted: map[string]grantedVM{}, pending: map[string]string{}}
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

func (o *vmOwnership) created(key, jobID, instanceID string) error {
	if o == nil || !o.enabled {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.owned[key] = ownedVM{CreatedAt: time.Now().UTC(), JobID: jobID, InstanceID: instanceID}
	return o.saveLocked()
}

// knowsInstance reports whether an allowed VM's record holds an instance id that
// confirm can check.
func (o *vmOwnership) knowsInstance(key string) bool {
	if o == nil || !o.enabled {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.owned[key].InstanceID != "" || o.granted[key].instanceID != ""
}

// confirm is allowed for the VM instance currently at key. A record whose instance
// id differs from current belongs to a VM that no longer exists: it is dropped, and
// the VM counts as the operator's. A record without an id adopts current, which only
// happens when the id could not be read when the record was made. An empty current
// (the engine has no id) cannot disprove a record.
func (o *vmOwnership) confirm(key, current string) (bool, error) {
	if o == nil || !o.enabled {
		return true, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	var saveErr error
	owned, isOwned := o.owned[key]
	if isOwned && current != "" {
		switch owned.InstanceID {
		case "":
			owned.InstanceID = current
			o.owned[key] = owned
			saveErr = o.saveLocked()
		case current:
		default:
			delete(o.owned, key)
			isOwned = false
			saveErr = o.saveLocked()
		}
	}
	grant, isGranted := o.granted[key]
	if isGranted && current != "" && grant.instanceID != "" && grant.instanceID != current {
		delete(o.granted, key)
		isGranted = false
	}
	return isOwned || isGranted, saveErr
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
		if !listedAsMissing(target) {
			continue
		}
		if grant, ok := o.granted[key]; ok && grant.at.Before(listedAt) {
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

// listedAsMissing reports whether a `create list` entry says its VM does not exist.
func listedAsMissing(target Target) bool {
	if target.Exists != nil {
		return !*target.Exists
	}
	state := target.Fields["create"]
	return state != "" && state != legacyCreatedState
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

// endRequest settles a pending request. instanceID is the VM the operator was asked
// about, so a grant never carries over to a recreated VM.
func (o *vmOwnership) endRequest(key string, approved bool, instanceID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.pending, key)
	if approved {
		o.granted[key] = grantedVM{at: time.Now().UTC(), instanceID: instanceID}
	}
}

// requestVMAccess asks the operator, in the background, to open a VM this gateway did
// not create to the agent. It returns almost at once (nil only if the request was
// decided as approved within busyProbe): an MCP call cannot wait for a human for the
// full approval TTL without running into client timeouts.
func (g *Gateway) requestVMAccess(tool, osName, osType, arch, instanceID string) error {
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
		g.ownership.endRequest(key, false, "")
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
		g.ownership.endRequest(key, approved, instanceID)
		_ = g.audit.Record(AuditEvent{Event: "vm_access_decision", Tool: tool, JobID: id, Decision: string(approval.Decision), Params: map[string]any{"os": osName, "type": osType, "arch": arch}, Risk: []string{riskForeignVM}, AutoApproved: approval.Auto})
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

// instanceAllowed reports whether the agent may use the VM at target without
// approval. When the record names an instance, the current instance must match; a
// failed identity lookup fails closed (the job then waits for approval). A missing
// VM cannot be someone else's, and the CLI command fails for it anyway.
func (g *Gateway) instanceAllowed(ctx context.Context, target VMTarget) bool {
	key := target.Key()
	if !g.ownership.allowed(key) {
		return false
	}
	if !g.ownership.knowsInstance(key) {
		return true
	}
	identity, err := g.vmResolver.Identify(ctx, target.OS, target.Type, target.Arch)
	if err != nil {
		fmt.Fprintf(g.console, "cannot confirm that VM %s is still the gateway's: %s\n", key, sanitizeForTerminal(err.Error()))
		return false
	}
	if !identity.Exists {
		return true
	}
	ok, err := g.ownership.confirm(key, identity.InstanceID)
	g.recordOwnership(err)
	return ok
}

// watchCreate makes a create job record ownership. The VM is the gateway's when the
// job succeeds, or when it fails after the CLI reported that deployment began, which
// can leave a partly created VM the agent must be able to destroy.
func (g *Gateway) watchCreate(spec *JobSpec, target VMTarget) error {
	token, err := newJobID()
	if err != nil {
		return err
	}
	marker := filepath.Join(g.dirs.Staging, "create-"+token+".started")
	spec.Env = append(spec.Env, createStartedFileEnv+"="+marker)
	spec.OnFinished = func(jobID string, succeeded bool) {
		_, statErr := os.Stat(marker)
		began := statErr == nil
		_ = os.Remove(marker)
		if !succeeded && !began {
			return
		}
		instanceID := ""
		identity, err := g.vmResolver.Identify(g.backgroundCtx, target.OS, target.Type, target.Arch)
		switch {
		case err == nil && identity.Exists:
			instanceID = identity.InstanceID
		case err == nil && !succeeded:
			return // the failed create left nothing behind
		case err != nil:
			// The record adopts the id on first use (see confirm).
			fmt.Fprintf(g.console, "warning: cannot read the instance id of new VM %s: %s\n", target.Key(), sanitizeForTerminal(err.Error()))
		}
		g.recordOwnership(g.ownership.created(target.Key(), jobID, instanceID))
	}
	return nil
}
