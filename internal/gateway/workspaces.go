package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	workspacesFileName      = "agent_workspaces.json"
	workspacesLockName      = "agent_workspaces.lock"
	workspacesLockTimeout   = 10 * time.Second
	workspacesSchemaVersion = 1
	// WorkspaceProbeName is the file the agent writes into its workspace to prove
	// which host directory backs it.
	WorkspaceProbeName  = ".sailwright-gateway-probe"
	maxProbeBytes       = 256
	riskAgentWorkspace  = "AGENT_WORKSPACE"
	workspaceSourceYAML = "gateway.yaml"
	workspaceSourceFile = workspacesFileName
)

// ErrApprovalPending reports that a request waits for the operator.
var ErrApprovalPending = errors.New("waits for operator approval")

var probeTokenPattern = regexp.MustCompile(`^[0-9a-f]{32,128}$`)

// RegisteredWorkspace is a workspace mapping added at runtime, either by the operator
// with `sailwright-gateway workspace add` or by an approved workspace_register call.
// Registered workspaces live in <state dir>/agent_workspaces.json, never in gateway.yaml.
type RegisteredWorkspace struct {
	Agent        string    `json:"agent"`
	Host         string    `json:"host"`
	RegisteredAt time.Time `json:"registered_at"`
	ApprovalID   string    `json:"approval_id,omitempty"`
}

type workspacesFile struct {
	SchemaVersion int                   `json:"schema_version"`
	Workspaces    []RegisteredWorkspace `json:"workspaces"`
}

// WorkspacesPath returns the file that holds the registered workspaces.
func WorkspacesPath(stateDir string) string {
	return filepath.Join(stateDir, workspacesFileName)
}

// LoadRegisteredWorkspaces reads and validates the registered workspaces of cfg's
// state dir. A missing file means none.
func LoadRegisteredWorkspaces(cfg Config) ([]RegisteredWorkspace, error) {
	data, err := readWorkspacesFile(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	return parseRegisteredWorkspaces(cfg, data)
}

// readWorkspacesFile returns the raw file, or nil when it does not exist.
func readWorkspacesFile(stateDir string) ([]byte, error) {
	path := WorkspacesPath(stateDir)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if info.Size() > maxConfigFileSizeBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxConfigFileSizeBytes)
	}
	// #nosec G304 -- the path is in the operator's gateway state dir.
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if data == nil {
		data = []byte{}
	}
	return data, nil
}

func parseRegisteredWorkspaces(cfg Config, data []byte) ([]RegisteredWorkspace, error) {
	if data == nil {
		return nil, nil
	}
	path := WorkspacesPath(cfg.StateDir)
	var file workspacesFile
	if err := json.Unmarshal(data, &file); err != nil || file.SchemaVersion != workspacesSchemaVersion {
		return nil, fmt.Errorf("%s is not a gateway workspaces file (schema %d); fix or remove it", path, workspacesSchemaVersion)
	}
	for i, workspace := range file.Workspaces {
		mapping, err := normalizeWorkspaceMapping(WorkspaceMapping{Agent: workspace.Agent, Host: workspace.Host})
		if err == nil {
			err = cfg.checkWorkspaceAgainstGatewayDirs(mapping)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: workspace %d: %w", path, i, err)
		}
		file.Workspaces[i].Agent, file.Workspaces[i].Host = mapping.Agent, mapping.Host
	}
	return file.Workspaces, nil
}

func saveRegisteredWorkspaces(stateDir string, workspaces []RegisteredWorkspace) error {
	if workspaces == nil {
		workspaces = []RegisteredWorkspace{}
	}
	data, err := json.MarshalIndent(workspacesFile{SchemaVersion: workspacesSchemaVersion, Workspaces: workspaces}, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFileAtomic(WorkspacesPath(stateDir), append(data, '\n'))
}

// lockRegisteredWorkspaces serializes read-modify-write cycles of the workspaces file
// across processes, so a `sailwright-gateway workspace` edit and an approval in a
// running gateway cannot overwrite each other. The OS drops the lock if the holder dies.
func lockRegisteredWorkspaces(stateDir string) (func(), error) {
	if err := ensurePrivateDir(stateDir); err != nil {
		return nil, err
	}
	path := filepath.Join(stateDir, workspacesLockName)
	// #nosec G304 -- the path is in the operator's gateway state dir.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, privateFilePermission)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	deadline := time.Now().Add(workspacesLockTimeout)
	for {
		err := lockFile(file)
		if err == nil {
			return func() { _ = file.Close() }, nil
		}
		if !errors.Is(err, errLockHeld) || time.Now().After(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func cleanAgentPath(agentPath string) string {
	agentPath = strings.TrimSpace(agentPath)
	if agentPath == "" {
		return ""
	}
	return path.Clean(agentPath)
}

// AddRegisteredWorkspace validates mapping as a new agent-writable workspace and
// appends it to the registered workspaces. The host path is stored resolved.
func AddRegisteredWorkspace(cfg Config, mapping WorkspaceMapping, approvalID string) (RegisteredWorkspace, error) {
	return addRegisteredWorkspace(cfg, mapping, approvalID, "")
}

// addRegisteredWorkspace adds mapping. A non-empty approvedHost is the resolved host
// path the operator approved; the mapping is refused if the host path resolves
// elsewhere now, e.g. because the agent swapped a directory for a symlink since.
func addRegisteredWorkspace(cfg Config, mapping WorkspaceMapping, approvalID string, approvedHost string) (RegisteredWorkspace, error) {
	unlock, err := lockRegisteredWorkspaces(cfg.StateDir)
	if err != nil {
		return RegisteredWorkspace{}, err
	}
	defer unlock()
	registered, err := LoadRegisteredWorkspaces(cfg)
	if err != nil {
		return RegisteredWorkspace{}, err
	}
	mapping, err = ValidateNewWorkspace(cfg, mapping)
	if err != nil {
		return RegisteredWorkspace{}, err
	}
	if approvedHost != "" && !samePathString(mapping.Host, approvedHost) {
		return RegisteredWorkspace{}, fmt.Errorf("host path %s now resolves to %s, not the approved %s; register it again", approvedHost, mapping.Host, approvedHost)
	}
	if existing, _, ok := findWorkspace(mapping.Agent, cfg.AgentWorkspaces, registered); ok {
		if sameHostPath(existing.Host, mapping.Host) {
			return RegisteredWorkspace{}, fmt.Errorf("%s is already mapped to %s", mapping.Agent, existing.Host)
		}
		return RegisteredWorkspace{}, fmt.Errorf("%s is already mapped to %s; remove that mapping first", mapping.Agent, existing.Host)
	}
	workspace := RegisteredWorkspace{Agent: mapping.Agent, Host: mapping.Host, RegisteredAt: time.Now().UTC(), ApprovalID: approvalID}
	if err := saveRegisteredWorkspaces(cfg.StateDir, append(registered, workspace)); err != nil {
		return RegisteredWorkspace{}, fmt.Errorf("save %s: %w", WorkspacesPath(cfg.StateDir), err)
	}
	return workspace, nil
}

// RemoveRegisteredWorkspace removes the registered workspace for an agent path. It
// reports false when none is registered; gateway.yaml entries are not touched.
func RemoveRegisteredWorkspace(cfg Config, agentPath string) (bool, error) {
	unlock, err := lockRegisteredWorkspaces(cfg.StateDir)
	if err != nil {
		return false, err
	}
	defer unlock()
	registered, err := LoadRegisteredWorkspaces(cfg)
	if err != nil {
		return false, err
	}
	agentPath = cleanAgentPath(agentPath)
	kept := registered[:0]
	removed := false
	for _, workspace := range registered {
		if workspace.Agent == agentPath {
			removed = true
			continue
		}
		kept = append(kept, workspace)
	}
	if !removed {
		return false, nil
	}
	if err := saveRegisteredWorkspaces(cfg.StateDir, kept); err != nil {
		return false, fmt.Errorf("save %s: %w", WorkspacesPath(cfg.StateDir), err)
	}
	return true, nil
}

// ValidateNewWorkspace checks a mapping that is added at runtime. Besides the
// gateway.yaml rules, the host directory must exist and must not be a filesystem root
// or contain the user's home directory. The returned host path is resolved.
func ValidateNewWorkspace(cfg Config, mapping WorkspaceMapping) (WorkspaceMapping, error) {
	mapping, err := normalizeWorkspaceMapping(mapping)
	if err != nil {
		return WorkspaceMapping{}, err
	}
	resolved, err := filepath.EvalSymlinks(mapping.Host)
	if err != nil {
		return WorkspaceMapping{}, fmt.Errorf("host path %s: %w", mapping.Host, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return WorkspaceMapping{}, fmt.Errorf("host path %s: %w", mapping.Host, err)
	}
	if !info.IsDir() {
		return WorkspaceMapping{}, fmt.Errorf("host path %s is not a directory", mapping.Host)
	}
	if filepath.Dir(resolved) == resolved {
		return WorkspaceMapping{}, fmt.Errorf("host path %s is a filesystem root", mapping.Host)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if resolvedHome, err := filepath.EvalSymlinks(home); err == nil && isWithin(resolved, resolvedHome) {
			return WorkspaceMapping{}, fmt.Errorf("host path %s contains your home directory", mapping.Host)
		}
	}
	for _, host := range []string{mapping.Host, resolved} {
		if err := cfg.checkWorkspaceAgainstGatewayDirs(WorkspaceMapping{Agent: mapping.Agent, Host: host}); err != nil {
			return WorkspaceMapping{}, err
		}
	}
	return WorkspaceMapping{Agent: mapping.Agent, Host: resolved}, nil
}

// VerifyWorkspaceProbe checks that hostDir holds the probe file the agent wrote into
// the directory it sees, which proves hostDir is the agent's mount and not a host
// directory it merely named.
func VerifyWorkspaceProbe(hostDir string, token string) error {
	if !probeTokenPattern.MatchString(token) {
		return errors.New("probe_token must be 32 to 128 lowercase hex characters (e.g. from `openssl rand -hex 16`)")
	}
	root, err := os.OpenRoot(hostDir)
	if err != nil {
		return fmt.Errorf("open host path %s: %w", hostDir, err)
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(WorkspaceProbeName)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("host path %s has no %s; write probe_token into %s in agent_path first, and check that host_path is the directory mounted there", hostDir, WorkspaceProbeName, WorkspaceProbeName)
	}
	if err != nil {
		return fmt.Errorf("read probe in %s: %w", hostDir, err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxProbeBytes {
		return fmt.Errorf("%s in %s must be a regular file of at most %d bytes", WorkspaceProbeName, hostDir, maxProbeBytes)
	}
	file, err := root.Open(WorkspaceProbeName)
	if err != nil {
		return fmt.Errorf("read probe in %s: %w", hostDir, err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxProbeBytes+1))
	if err != nil {
		return fmt.Errorf("read probe in %s: %w", hostDir, err)
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(string(data))), []byte(token)) != 1 {
		return fmt.Errorf("%s in host path %s does not hold probe_token, so host_path is not the directory mounted at agent_path", WorkspaceProbeName, hostDir)
	}
	return nil
}

func findWorkspace(agentPath string, static []WorkspaceMapping, registered []RegisteredWorkspace) (WorkspaceMapping, string, bool) {
	for _, mapping := range static {
		if mapping.Agent == agentPath {
			return mapping, workspaceSourceYAML, true
		}
	}
	for _, workspace := range registered {
		if workspace.Agent == agentPath {
			return WorkspaceMapping{Agent: workspace.Agent, Host: workspace.Host, Registered: true}, workspaceSourceFile, true
		}
	}
	return WorkspaceMapping{}, "", false
}

func sameHostPath(a, b string) bool {
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	return samePathString(a, b)
}

// workspaceRegistry holds the gateway.yaml workspaces plus the registered ones. It
// rereads the registered file on every use and reparses it when its content changed,
// so `sailwright-gateway workspace` edits reach a running gateway at the next call.
type workspaceRegistry struct {
	cfg     Config
	console io.Writer

	mu         sync.Mutex
	registered []RegisteredWorkspace
	data       []byte // file content the registered list was parsed from
	parsed     bool
	loadErr    string
	pending    map[string]string // agent path -> approval id
}

func loadWorkspaceRegistry(cfg Config, console io.Writer) (*workspaceRegistry, error) {
	data, err := readWorkspacesFile(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	registered, err := parseRegisteredWorkspaces(cfg, data)
	if err != nil {
		return nil, err
	}
	if console == nil {
		console = io.Discard
	}
	return &workspaceRegistry{cfg: cfg, console: console, registered: registered, data: data, parsed: true, pending: map[string]string{}}, nil
}

// All returns every workspace; a gateway.yaml entry wins over a registered one with
// the same agent path.
func (r *workspaceRegistry) All() []WorkspaceMapping {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloadLocked()
	all := append([]WorkspaceMapping(nil), r.cfg.AgentWorkspaces...)
	for _, workspace := range r.registered {
		if _, _, ok := findWorkspace(workspace.Agent, r.cfg.AgentWorkspaces, nil); ok {
			continue
		}
		all = append(all, WorkspaceMapping{Agent: workspace.Agent, Host: workspace.Host, Registered: true})
	}
	return all
}

func (r *workspaceRegistry) lookup(agentPath string) (WorkspaceMapping, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloadLocked()
	return findWorkspace(agentPath, r.cfg.AgentWorkspaces, r.registered)
}

// reloadLocked rereads the file and reparses it when the content changed. Comparing
// content instead of modification time also catches same-size rewrites within one
// timestamp tick. An invalid file fails closed: only the gateway.yaml workspaces stay
// usable until it is fixed.
func (r *workspaceRegistry) reloadLocked() {
	data, err := readWorkspacesFile(r.cfg.StateDir)
	if err == nil && r.parsed && bytes.Equal(data, r.data) && (data == nil) == (r.data == nil) {
		return
	}
	var registered []RegisteredWorkspace
	if err == nil {
		registered, err = parseRegisteredWorkspaces(r.cfg, data)
		r.data, r.parsed = data, true
	} else {
		r.data, r.parsed = nil, false
	}
	if err != nil {
		r.registered = nil
		if msg := err.Error(); msg != r.loadErr {
			r.loadErr = msg
			fmt.Fprintf(r.console, "registered workspaces ignored: %s\n", sanitizeForTerminal(msg))
		}
		return
	}
	r.loadErr = ""
	r.registered = registered
	fmt.Fprintf(r.console, "registered workspaces reloaded from %s: %d\n", workspacesFileName, len(registered))
}

// add registers mapping after the operator approved approvedHost.
func (r *workspaceRegistry) add(mapping WorkspaceMapping, approvalID string, approvedHost string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := addRegisteredWorkspace(r.cfg, mapping, approvalID, approvedHost); err != nil {
		return err
	}
	r.reloadLocked()
	return nil
}

func (r *workspaceRegistry) beginRequest(agentPath, id string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.pending[agentPath]; ok {
		return existing, false
	}
	r.pending[agentPath] = id
	return id, true
}

func (r *workspaceRegistry) endRequest(agentPath string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pending, agentPath)
}

// ---- MCP tool ----

type workspaceRegisterInput struct {
	AgentPath  string `json:"agent_path" jsonschema:"absolute directory as you see it, e.g. /workspaces/my-roles (usually your bind-mounted workspace)"`
	HostPath   string `json:"host_path" jsonschema:"absolute host directory backing agent_path, e.g. C:\\Users\\me\\Projects\\my-roles; ask the user if you do not know it"`
	ProbeToken string `json:"probe_token" jsonschema:"32+ random lowercase hex characters you wrote into <agent_path>/.sailwright-gateway-probe before the call"`
	Reason     string `json:"reason,omitempty" jsonschema:"why this is needed; shown to the human approver"`
}

// WorkspaceRegisterOutput is the result of workspace_register.
type WorkspaceRegisterOutput struct {
	Status    string `json:"status"`
	AgentPath string `json:"agent_path"`
	HostPath  string `json:"host_path"`
	Source    string `json:"source"`
}

const workspaceRegisterDescription = "Map a directory you see (agent_path) to the host directory that backs it (host_path), so run_cli can use it as bundle_root. " +
	"First write probe_token (32+ random hex chars) into <agent_path>/" + WorkspaceProbeName + "; the gateway reads it on the host to prove the mapping. " +
	"Needs one human approval on the host; until then the call fails with a request id, call it again after approval. " +
	"The mapping persists across gateway restarts and takes effect without one. Delete the probe file afterwards."

func (g *Gateway) toolWorkspaceRegister(_ context.Context, _ *mcp.CallToolRequest, in workspaceRegisterInput) (*mcp.CallToolResult, WorkspaceRegisterOutput, error) {
	mapping, err := normalizeWorkspaceMapping(WorkspaceMapping{Agent: in.AgentPath, Host: in.HostPath})
	if err != nil {
		return nil, WorkspaceRegisterOutput{}, err
	}
	if existing, source, ok := g.workspaces.lookup(mapping.Agent); ok {
		if !sameHostPath(existing.Host, mapping.Host) {
			return nil, WorkspaceRegisterOutput{}, fmt.Errorf("%s is already mapped to host path %s (%s); the operator can change it", mapping.Agent, existing.Host, source)
		}
		return nil, WorkspaceRegisterOutput{Status: "registered", AgentPath: existing.Agent, HostPath: existing.Host, Source: source}, nil
	}
	resolved, err := ValidateNewWorkspace(g.cfg, mapping)
	if err != nil {
		return nil, WorkspaceRegisterOutput{}, err
	}
	if err := VerifyWorkspaceProbe(resolved.Host, in.ProbeToken); err != nil {
		return nil, WorkspaceRegisterOutput{}, err
	}
	if !g.approver.Enabled() {
		return nil, WorkspaceRegisterOutput{}, fmt.Errorf("registering workspace %s needs host approval, but the gateway has no approval console; ask the operator to run `%s` or to add it to agent_workspaces in gateway.yaml", resolved.Agent, operatorCommand("workspace", "add", resolved.Agent, resolved.Host))
	}
	id, err := newJobID()
	if err != nil {
		return nil, WorkspaceRegisterOutput{}, err
	}
	if pending, ok := g.workspaces.beginRequest(resolved.Agent, id); !ok {
		return nil, WorkspaceRegisterOutput{}, fmt.Errorf("%w: request %s for workspace %s is waiting on the host terminal; call workspace_register again after the operator approves it", ErrApprovalPending, pending, resolved.Agent)
	}
	req := ApprovalRequest{
		ID:        id,
		Tool:      "workspace_register",
		Command:   operatorCommand("workspace", "add", resolved.Agent, resolved.Host),
		Risk:      []string{riskAgentWorkspace},
		AgentNote: truncate(in.Reason, maxAgentNoteLength),
		NotSafe: fmt.Sprintf("maps the agent's %s to host directory %s. The gateway treats that directory as agent-writable and copies files from it for run_cli calls you approve. "+
			"The mapping is saved in %s; remove it with `%s`", resolved.Agent, resolved.Host, workspacesFileName, operatorCommand("workspace", "remove", resolved.Agent)),
	}
	var addErr error
	decision, decided, err := g.askOperatorAsync(req, func(approval Approval) {
		if approval.Decision == DecisionApproved {
			if addErr = g.workspaces.add(resolved, id, resolved.Host); addErr != nil {
				fmt.Fprintf(g.console, "workspace %s was approved but not registered: %s\n", sanitizeForTerminal(resolved.Agent), sanitizeForTerminal(addErr.Error()))
			}
		}
		g.workspaces.endRequest(resolved.Agent)
		event := AuditEvent{Event: "workspace_register_decision", Tool: "workspace_register", JobID: id, Decision: string(approval.Decision), Params: map[string]any{"agent_path": resolved.Agent, "host_path": resolved.Host}, Risk: []string{riskAgentWorkspace}, AutoApproved: approval.Auto}
		if addErr != nil {
			event.Error = addErr.Error()
		}
		_ = g.audit.Record(event)
	})
	if err != nil {
		g.workspaces.endRequest(resolved.Agent)
		return nil, WorkspaceRegisterOutput{}, err
	}
	if decided {
		switch decision {
		case DecisionApproved:
			if addErr != nil {
				return nil, WorkspaceRegisterOutput{}, fmt.Errorf("workspace %s was approved but could not be saved: %w", resolved.Agent, addErr)
			}
			return nil, WorkspaceRegisterOutput{Status: "registered", AgentPath: resolved.Agent, HostPath: resolved.Host, Source: workspaceSourceFile}, nil
		case DecisionBusy:
			return nil, WorkspaceRegisterOutput{}, fmt.Errorf("%w: another approval is open on the host terminal; call workspace_register again later", ErrApprovalPending)
		default:
			return nil, WorkspaceRegisterOutput{}, fmt.Errorf("operator decision on workspace %s: %s", resolved.Agent, decision)
		}
	}
	return nil, WorkspaceRegisterOutput{}, fmt.Errorf("%w: the operator must approve request %s for workspace %s on the host terminal; call workspace_register again after approval", ErrApprovalPending, id, resolved.Agent)
}

// operatorCommand renders a sailwright-gateway command line the operator can paste
// into a shell. Arguments with spaces or shell metacharacters are quoted: double
// quotes on Windows (cmd and PowerShell; Windows paths cannot contain them), single
// quotes elsewhere.
func operatorCommand(args ...string) string {
	parts := []string{"sailwright-gateway"}
	for _, arg := range args {
		parts = append(parts, quoteShellArg(arg, runtime.GOOS == "windows"))
	}
	return strings.Join(parts, " ")
}

func quoteShellArg(arg string, windows bool) string {
	special := " \t\"'$`;&|<>()*?[]{}!#~"
	if !windows {
		special += "\\"
	}
	if arg != "" && !strings.ContainsAny(arg, special) {
		return arg
	}
	if windows {
		return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}
