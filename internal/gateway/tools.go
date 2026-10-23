package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	tierSafe           = "safe"
	tierApproval       = "approval"
	defaultOutputChunk = 64 << 10
	maxOutputChunk     = 1 << 20
	maxAgentNoteLength = 2000
	serverInstructions = `This server drives the sailwright CLI on the host machine through a fixed policy.

- Safe tools (vm_*, artifact_pull, list_targets, command_help) run immediately.
- provision_local, install and run_cli calls outside the safe set wait for a human to approve them on the host terminal. You cannot approve them yourself. Explain why you need the call in the "reason" field.
- Long operations are jobs: poll job_status and read job_output with the returned next_offset. Only one job runs at a time.
- Use list_targets first to see which os/type/arch combinations exist on this host.
- For run_cli with files from your workspace, set bundle_root to the directory that contains them and use absolute paths below it; the gateway runs a read-only snapshot of that directory.
- Builds can print nothing for a long time while the installer runs in the VM. job_status shows last_output_at, screen_changed_at (visible console changes) and stuck_suspected (a hint only). Call job_screenshot only when the user asks to see the VM console; a human on the host can watch the live view shown in the gateway terminal.`
)

func (g *Gateway) newMCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "sailwright-gateway", Version: g.version}, &mcp.ServerOptions{
		Instructions: serverInstructions,
	})

	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(server, &mcp.Tool{Name: "gateway_info", Description: "Describe the host, the pinned sailwright binary, available tools and current targets.", Annotations: readOnly}, g.toolGatewayInfo)
	mcp.AddTool(server, &mcp.Tool{Name: "list_targets", Description: "Run `sailwright <command> list --json` and return the targets with their current state.", Annotations: readOnly}, g.toolListTargets)
	mcp.AddTool(server, &mcp.Tool{Name: "command_help", Description: "Show `sailwright [command] [list] --help`.", Annotations: readOnly}, g.toolCommandHelp)

	mcp.AddTool(server, &mcp.Tool{Name: "vm_build", Description: "Build a VM image (safe set, starts a job)."}, g.toolBuild)
	mcp.AddTool(server, &mcp.Tool{Name: "vm_create", Description: "Create a VM from a built image (safe set, starts a job)."}, g.vmTool(OpCreate))
	mcp.AddTool(server, &mcp.Tool{Name: "vm_start", Description: "Start a VM (safe set, starts a job)."}, g.vmTool(OpStart))
	mcp.AddTool(server, &mcp.Tool{Name: "vm_stop", Description: "Stop a VM (safe set, starts a job)."}, g.vmTool(OpStop))
	mcp.AddTool(server, &mcp.Tool{Name: "vm_destroy", Description: "Destroy a VM (safe set, starts a job)."}, g.vmTool(OpDestroy))
	mcp.AddTool(server, &mcp.Tool{Name: "vm_provision", Description: "Provision a VM with an embedded playbook, optionally in check mode (safe set, starts a job)."}, g.toolProvision)
	mcp.AddTool(server, &mcp.Tool{Name: "artifact_pull", Description: "Pull a prebuilt VM artifact from an allowlisted OCI registry (safe set, starts a job)."}, g.toolPull)

	mcp.AddTool(server, &mcp.Tool{Name: "provision_local", Description: "Provision THIS HOST with an embedded playbook. Always needs human approval on the host."}, g.toolProvisionLocal)
	mcp.AddTool(server, &mcp.Tool{Name: "install", Description: "Install sailwright host dependencies. Always needs human approval on the host."}, g.toolInstall)
	mcp.AddTool(server, &mcp.Tool{Name: "run_cli", Description: "Run any sailwright command line. Calls inside the safe set run directly; everything else (paths, args after --, other commands) waits for human approval on the host."}, g.toolRunCLI)

	mcp.AddTool(server, &mcp.Tool{Name: "job_status", Description: "Get the state of a job.", Annotations: readOnly}, g.toolJobStatus)
	mcp.AddTool(server, &mcp.Tool{Name: "job_output", Description: "Read job output from an offset; continue with next_offset.", Annotations: readOnly}, g.toolJobOutput)
	mcp.AddTool(server, &mcp.Tool{Name: "job_cancel", Description: "Cancel a pending or running job."}, g.toolJobCancel)
	mcp.AddTool(server, &mcp.Tool{Name: "job_list", Description: "List recent jobs.", Annotations: readOnly}, g.toolJobList)
	mcp.AddTool(server, &mcp.Tool{Name: "job_screenshot", Description: "Show the VM console of a build job as an image (recorded during QEMU builds). Use it when the user asks to see the VM; job_status already reports whether the console still changes.", Annotations: readOnly}, g.toolJobScreenshot)
	return server
}

// ---- inputs and outputs ----

type emptyInput struct{}

// GatewayInfo describes the gateway to agents.
type GatewayInfo struct {
	GatewayVersion  string              `json:"gateway_version"`
	HostOS          string              `json:"host_os"`
	HostArch        string              `json:"host_arch"`
	BinarySHA256    string              `json:"binary_sha256"`
	BinarySource    string              `json:"binary_source"`
	DataDir         string              `json:"data_dir"`
	ApprovalEnabled bool                `json:"approval_enabled"`
	SafeTools       []string            `json:"safe_tools"`
	ApprovalTools   []string            `json:"approval_tools"`
	Playbooks       []string            `json:"playbooks"`
	PullAllowlist   []string            `json:"pull_allowlist"`
	AgentWorkspaces []string            `json:"agent_workspaces"`
	ActiveJob       string              `json:"active_job,omitempty"`
	Targets         map[string][]string `json:"targets"`
	TargetErrors    map[string]string   `json:"target_errors,omitempty"`
	VMAccess        string              `json:"vm_access"`
	Limits          GatewayLimits       `json:"limits"`
}

// GatewayLimits are the operator-configured limits an agent can run into.
type GatewayLimits struct {
	JobTimeoutSeconds  int64 `json:"job_timeout_seconds"`
	ApprovalTTLSeconds int64 `json:"approval_ttl_seconds"`
	OutputLimitBytes   int   `json:"output_limit_bytes"`
	SnapshotMaxBytes   int64 `json:"snapshot_max_bytes"`
	SnapshotMaxFiles   int   `json:"snapshot_max_files"`
	// MinFreeDiskGB is the free space required before build, create and pull; 0 means
	// the check is disabled.
	MinFreeDiskGB int `json:"min_free_disk_gb"`
}

type listTargetsInput struct {
	Command string `json:"command" jsonschema:"one of build, create, start, stop, destroy, provision, pull, push"`
}

// ListTargetsOutput is the result of list_targets.
type ListTargetsOutput struct {
	Command string   `json:"command"`
	Targets []Target `json:"targets"`
}

type commandHelpInput struct {
	Command string `json:"command,omitempty" jsonschema:"sailwright command, empty for the root help"`
	List    bool   `json:"list,omitempty" jsonschema:"show help for '<command> list'"`
}

// HelpOutput is the result of command_help.
type HelpOutput struct {
	Text string `json:"text"`
}

type vmTargetInput struct {
	OS   string `json:"os" jsonschema:"target OS as listed by list_targets, e.g. ubuntu or windows11"`
	Type string `json:"type,omitempty" jsonschema:"OS type, e.g. server or desktop (only for OSes that list a type)"`
	Arch string `json:"arch,omitempty" jsonschema:"architecture, defaults to amd64"`
}

type buildInput struct {
	OS       string `json:"os" jsonschema:"target OS as listed by list_targets"`
	Type     string `json:"type,omitempty" jsonschema:"OS type, e.g. server or desktop"`
	Arch     string `json:"arch,omitempty" jsonschema:"architecture, defaults to amd64"`
	Engine   string `json:"engine,omitempty" jsonschema:"virtualization engine when several are listed"`
	Headless bool   `json:"headless,omitempty"`
	Verbose  bool   `json:"verbose,omitempty"`
	NoCache  bool   `json:"no_cache,omitempty"`
}

type provisionInput struct {
	OS        string `json:"os" jsonschema:"target VM OS as listed by list_targets"`
	Type      string `json:"type,omitempty"`
	Arch      string `json:"arch,omitempty"`
	Check     bool   `json:"check,omitempty" jsonschema:"run ansible-playbook in check mode"`
	Verbosity *int   `json:"verbosity,omitempty" jsonschema:"Ansible verbosity 0-4"`
	Playbook  string `json:"playbook,omitempty" jsonschema:"name of an embedded playbook (see gateway_info.playbooks); empty uses the default"`
}

type pullInput struct {
	Ref    string `json:"ref" jsonschema:"OCI reference inside the gateway pull_allowlist"`
	OS     string `json:"os"`
	Type   string `json:"type,omitempty"`
	Arch   string `json:"arch,omitempty"`
	Engine string `json:"engine,omitempty"`
}

type provisionLocalInput struct {
	Check    bool   `json:"check,omitempty" jsonschema:"run in check mode (still runs Ansible on the host)"`
	Playbook string `json:"playbook,omitempty" jsonschema:"name of an embedded playbook; empty uses the default"`
	Reason   string `json:"reason,omitempty" jsonschema:"why this is needed; shown to the human approver"`
}

type installInput struct {
	WithGo     bool   `json:"with_go,omitempty"`
	VirtualBox bool   `json:"virtualbox,omitempty"`
	Reason     string `json:"reason,omitempty" jsonschema:"why this is needed; shown to the human approver"`
}

type runCLIInput struct {
	Args       []string `json:"args" jsonschema:"sailwright arguments without the program name"`
	BundleRoot string   `json:"bundle_root,omitempty" jsonschema:"absolute directory in your workspace that holds files referenced by args; it is snapshotted read-only"`
	Reason     string   `json:"reason,omitempty" jsonschema:"why this is needed; shown to the human approver"`
}

type jobIDInput struct {
	JobID string `json:"job_id"`
}

type jobOutputInput struct {
	JobID    string `json:"job_id"`
	Offset   int64  `json:"offset,omitempty" jsonschema:"absolute byte offset; use next_offset from the previous call"`
	MaxBytes int    `json:"max_bytes,omitempty" jsonschema:"maximum bytes to return (default 65536)"`
}

// JobOutputResult is the result of job_output.
type JobOutputResult struct {
	OutputChunk
	State    JobState `json:"state"`
	ExitCode *int     `json:"exit_code,omitempty"`
}

// JobListOutput is the result of job_list.
type JobListOutput struct {
	Jobs []JobView `json:"jobs"`
}

type jobScreenshotInput struct {
	JobID string `json:"job_id"`
	Index int    `json:"index,omitempty" jsonschema:"0 is the newest frame, 1 the one before, up to frames_available-1"`
}

// ScreenshotInfo describes the frame returned by job_screenshot, or why there is none.
type ScreenshotInfo struct {
	JobID                  string    `json:"job_id"`
	State                  JobState  `json:"state"`
	Available              bool      `json:"available"`
	Reason                 string    `json:"reason,omitempty"`
	Index                  int       `json:"index"`
	FramesAvailable        int       `json:"frames_available"`
	CapturedAt             time.Time `json:"captured_at,omitzero"`
	AgeSeconds             int64     `json:"age_seconds,omitempty"`
	ScreenChangedAt        time.Time `json:"screen_changed_at,omitzero"`
	ScreenUnchangedSeconds int64     `json:"screen_unchanged_seconds,omitempty"`
	StuckSuspected         bool      `json:"stuck_suspected,omitempty"`
	LiveView               string    `json:"live_view,omitempty"`
}

// ---- handlers ----

func (g *Gateway) toolGatewayInfo(_ context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, GatewayInfo, error) {
	grammar := g.grammar()
	var workspaces []string
	for _, mapping := range g.cfg.AgentWorkspaces {
		workspaces = append(workspaces, mapping.Agent)
	}
	return nil, GatewayInfo{
		GatewayVersion:  g.version,
		HostOS:          runtime.GOOS,
		HostArch:        runtime.GOARCH,
		BinarySHA256:    g.pinned.SHA256,
		BinarySource:    g.pinned.Source,
		DataDir:         g.cfg.DataDir,
		ApprovalEnabled: g.approver.Enabled(),
		SafeTools:       []string{"list_targets", "command_help", "vm_build", "vm_create", "vm_start", "vm_stop", "vm_destroy", "vm_provision", "artifact_pull", "job_screenshot", "run_cli (safe subset)"},
		ApprovalTools:   []string{"provision_local", "install", "run_cli (everything else)"},
		Playbooks:       grammar.Playbooks,
		PullAllowlist:   g.cfg.PullAllowlist,
		AgentWorkspaces: workspaces,
		ActiveJob:       g.jobs.Active(),
		Targets:         g.catalog.Summary(),
		TargetErrors:    g.catalog.Errors(),
		VMAccess:        "not available yet",
		Limits: GatewayLimits{
			JobTimeoutSeconds:  int64(g.cfg.JobTimeoutValue.Seconds()),
			ApprovalTTLSeconds: int64(g.cfg.ApprovalTTLValue.Seconds()),
			OutputLimitBytes:   g.cfg.OutputLimitBytes,
			SnapshotMaxBytes:   g.cfg.SnapshotMaxBytes,
			SnapshotMaxFiles:   g.cfg.SnapshotMaxFiles,
			MinFreeDiskGB:      max(g.cfg.MinFreeDiskGB, 0),
		},
	}, nil
}

func (g *Gateway) toolListTargets(ctx context.Context, _ *mcp.CallToolRequest, in listTargetsInput) (*mcp.CallToolResult, ListTargetsOutput, error) {
	if !containsString(listCommands, in.Command) {
		return nil, ListTargetsOutput{}, fmt.Errorf("command must be one of %s", strings.Join(listCommands, ", "))
	}
	targets, err := g.listTargets(ctx, in.Command)
	g.catalog.Set(in.Command, targets, err)
	g.refreshPlaybooks()
	if err != nil {
		return nil, ListTargetsOutput{}, err
	}
	if targets == nil {
		targets = []Target{}
	}
	return nil, ListTargetsOutput{Command: in.Command, Targets: targets}, nil
}

func (g *Gateway) toolCommandHelp(ctx context.Context, _ *mcp.CallToolRequest, in commandHelpInput) (*mcp.CallToolResult, HelpOutput, error) {
	op := Operation{Kind: OpHelp}
	if in.Command != "" {
		op.HelpPath = []string{in.Command}
		if in.List {
			op.HelpPath = append(op.HelpPath, "list")
		}
	}
	if err := op.Validate(g.grammar()); err != nil {
		return nil, HelpOutput{}, err
	}
	output, _, err := g.runInfo(ctx, op.Argv(g.grammar()))
	if err != nil {
		return nil, HelpOutput{}, err
	}
	return nil, HelpOutput{Text: output}, nil
}

func (g *Gateway) toolBuild(_ context.Context, req *mcp.CallToolRequest, in buildInput) (*mcp.CallToolResult, JobView, error) {
	op := Operation{Kind: OpBuild, OS: in.OS, Type: in.Type, Arch: in.Arch, Engine: in.Engine, Headless: in.Headless, Verbose: in.Verbose, NoCache: in.NoCache, Verbosity: -1}
	return g.submitSafe("vm_build", op, req, structParams(in))
}

func (g *Gateway) vmTool(kind OpKind) func(context.Context, *mcp.CallToolRequest, vmTargetInput) (*mcp.CallToolResult, JobView, error) {
	return func(_ context.Context, req *mcp.CallToolRequest, in vmTargetInput) (*mcp.CallToolResult, JobView, error) {
		op := Operation{Kind: kind, OS: in.OS, Type: in.Type, Arch: in.Arch, Verbosity: -1}
		return g.submitSafe("vm_"+string(kind), op, req, structParams(in))
	}
}

func (g *Gateway) toolProvision(_ context.Context, req *mcp.CallToolRequest, in provisionInput) (*mcp.CallToolResult, JobView, error) {
	verbosity := -1
	if in.Verbosity != nil {
		verbosity = *in.Verbosity
		if verbosity < 0 {
			return nil, JobView{}, errors.New("verbosity must be between 0 and 4")
		}
	}
	op := Operation{Kind: OpProvision, OS: in.OS, Type: in.Type, Arch: in.Arch, Check: in.Check, Verbosity: verbosity, Playbook: in.Playbook}
	return g.submitSafe("vm_provision", op, req, structParams(in))
}

func (g *Gateway) toolPull(_ context.Context, req *mcp.CallToolRequest, in pullInput) (*mcp.CallToolResult, JobView, error) {
	op := Operation{Kind: OpPull, Ref: in.Ref, OS: in.OS, Type: in.Type, Arch: in.Arch, Engine: in.Engine, Verbosity: -1}
	return g.submitSafe("artifact_pull", op, req, structParams(in))
}

func (g *Gateway) toolProvisionLocal(_ context.Context, req *mcp.CallToolRequest, in provisionLocalInput) (*mcp.CallToolResult, JobView, error) {
	argv := []string{"provision", "local", "--yes"}
	if in.Check {
		argv = append(argv, "--check")
	}
	if in.Playbook != "" {
		grammar := g.grammar()
		if !containsString(grammar.Playbooks, in.Playbook) {
			return nil, JobView{}, fmt.Errorf("playbook %q is not an embedded playbook (available: %s)", in.Playbook, strings.Join(grammar.Playbooks, ", "))
		}
		argv = append(argv, "--playbook", embeddedPlaybookPath(grammar.PlaybookDir, in.Playbook))
	}
	return g.submitApproval("provision_local", argv, g.dirs.Work, nil, "local provisioning runs Ansible against this host", in.Reason, becomeSupported(argv), req, structParams(in))
}

func (g *Gateway) toolInstall(_ context.Context, req *mcp.CallToolRequest, in installInput) (*mcp.CallToolResult, JobView, error) {
	argv := []string{"install"}
	if in.WithGo {
		argv = append(argv, "--with-go")
	}
	if in.VirtualBox {
		argv = append(argv, "--virtualbox")
	}
	return g.submitApproval("install", argv, g.dirs.Work, nil, "install changes host packages", in.Reason, false, req, structParams(in))
}

func (g *Gateway) toolRunCLI(_ context.Context, req *mcp.CallToolRequest, in runCLIInput) (*mcp.CallToolResult, JobView, error) {
	if err := validateRawArgs(in.Args); err != nil {
		return nil, JobView{}, err
	}
	params := structParams(in)

	op, notSafe := ParseSafe(in.Args, g.grammar())
	if notSafe == nil {
		return g.submitSafe("run_cli", op, req, params)
	}

	if g.jobs.Active() != "" {
		return nil, JobView{}, fmt.Errorf("%w (active job %s)", ErrBusy, g.jobs.Active())
	}

	argv := append([]string(nil), in.Args...)
	dir := g.dirs.Work
	var snapshot *Snapshot
	bundleRoot := ""
	if in.BundleRoot != "" {
		hostRoot, _, err := TranslateAgentPath(in.BundleRoot, g.cfg.AgentWorkspaces)
		if err != nil {
			return nil, JobView{}, err
		}
		// Canonicalize once so snapshot, path rewrite and risk assessment agree.
		bundleRoot = path.Clean(in.BundleRoot)
		created, err := CreateSnapshot(bundleRoot, hostRoot, g.dirs.Staging, SnapshotLimits{MaxBytes: g.cfg.SnapshotMaxBytes, MaxFiles: g.cfg.SnapshotMaxFiles})
		if err != nil {
			return nil, JobView{}, fmt.Errorf("snapshot bundle_root: %w", err)
		}
		snapshot = &created
		argv = RewriteBundlePaths(argv, created)
		dir = created.Dir
	}
	view, err := g.submitApprovalWithRisk("run_cli", argv, dir, snapshot, notSafe.Error(), in.Reason, AssessRisk(in.Args, bundleRoot), becomeSupported(in.Args), req, params)
	if err != nil && snapshot != nil {
		_ = RemoveSnapshot(snapshot.Dir)
	}
	return nil, view, err
}

func (g *Gateway) toolJobStatus(_ context.Context, _ *mcp.CallToolRequest, in jobIDInput) (*mcp.CallToolResult, JobView, error) {
	view, ok := g.jobs.Get(in.JobID)
	if !ok {
		return nil, JobView{}, fmt.Errorf("unknown job %q", in.JobID)
	}
	return nil, view, nil
}

func (g *Gateway) toolJobOutput(_ context.Context, _ *mcp.CallToolRequest, in jobOutputInput) (*mcp.CallToolResult, JobOutputResult, error) {
	maxBytes := in.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultOutputChunk
	}
	if maxBytes > maxOutputChunk {
		maxBytes = maxOutputChunk
	}
	chunk, ok := g.jobs.Output(in.JobID, in.Offset, maxBytes)
	if !ok {
		return nil, JobOutputResult{}, fmt.Errorf("unknown job %q", in.JobID)
	}
	view, _ := g.jobs.Get(in.JobID)
	return nil, JobOutputResult{OutputChunk: chunk, State: view.State, ExitCode: view.ExitCode}, nil
}

func (g *Gateway) toolJobCancel(_ context.Context, _ *mcp.CallToolRequest, in jobIDInput) (*mcp.CallToolResult, JobView, error) {
	view, ok := g.jobs.Cancel(in.JobID)
	if !ok {
		return nil, JobView{}, fmt.Errorf("unknown job %q", in.JobID)
	}
	return nil, view, nil
}

func (g *Gateway) toolJobList(_ context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, JobListOutput, error) {
	return nil, JobListOutput{Jobs: g.jobs.List()}, nil
}

func (g *Gateway) toolJobScreenshot(_ context.Context, _ *mcp.CallToolRequest, in jobScreenshotInput) (*mcp.CallToolResult, ScreenshotInfo, error) {
	shot, err := g.jobs.Screenshot(in.JobID, in.Index)
	if err != nil {
		return nil, ScreenshotInfo{}, err
	}
	now := time.Now().UTC()
	info := ScreenshotInfo{
		JobID:           in.JobID,
		State:           shot.View.State,
		Available:       shot.Data != nil,
		Reason:          shot.Reason,
		Index:           in.Index,
		FramesAvailable: shot.Status.Frames,
		CapturedAt:      shot.Frame.CapturedAt,
		ScreenChangedAt: shot.Status.ChangedAt,
		StuckSuspected:  shot.Status.StuckSuspected,
	}
	if !info.CapturedAt.IsZero() {
		info.AgeSeconds = int64(now.Sub(info.CapturedAt).Seconds())
	}
	if !info.ScreenChangedAt.IsZero() {
		info.ScreenUnchangedSeconds = int64(now.Sub(info.ScreenChangedAt).Seconds())
	}
	if g.cfg.ViewAddr.IsValid() {
		info.LiveView = "a human on the host can watch the console live; the address is shown in the gateway terminal"
	}
	if shot.Data == nil {
		return nil, info, nil
	}
	// With Content set the SDK adds no JSON text of its own, so describe the frame here.
	summary, err := json.Marshal(info)
	if err != nil {
		return nil, ScreenshotInfo{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.ImageContent{Data: shot.Data, MIMEType: "image/jpeg"},
		&mcp.TextContent{Text: string(summary)},
	}}, info, nil
}

// ---- submission helpers ----

func (g *Gateway) submitSafe(tool string, op Operation, req *mcp.CallToolRequest, params map[string]any) (*mcp.CallToolResult, JobView, error) {
	grammar := g.grammar()
	if err := op.Validate(grammar); err != nil {
		return nil, JobView{}, err
	}
	view, err := g.jobs.Submit(JobSpec{
		Tool:             tool,
		Tier:             tierSafe,
		Argv:             op.Argv(grammar),
		Dir:              g.dirs.Work,
		Timeout:          g.cfg.JobTimeoutValue,
		Params:           params,
		Peer:             peerOf(req),
		RequiresFreeDisk: op.Kind == OpBuild || op.Kind == OpCreate || op.Kind == OpPull,
		Target:           &JobTarget{Kind: op.Kind, OS: op.OS, Type: op.Type, Arch: op.Arch},
	})
	return nil, view, err
}

func (g *Gateway) submitApproval(tool string, argv []string, dir string, snapshot *Snapshot, notSafe string, note string, become bool, req *mcp.CallToolRequest, params map[string]any) (*mcp.CallToolResult, JobView, error) {
	view, err := g.submitApprovalWithRisk(tool, argv, dir, snapshot, notSafe, note, AssessRisk(argv, ""), become, req, params)
	return nil, view, err
}

func (g *Gateway) submitApprovalWithRisk(tool string, argv []string, dir string, snapshot *Snapshot, notSafe string, note string, risk []string, become bool, req *mcp.CallToolRequest, params map[string]any) (JobView, error) {
	return g.jobs.Submit(JobSpec{
		Tool:          tool,
		Tier:          tierApproval,
		Argv:          argv,
		Dir:           dir,
		Timeout:       g.cfg.JobTimeoutValue,
		Params:        params,
		Peer:          peerOf(req),
		Risk:          risk,
		Snapshot:      snapshot,
		NeedsApproval: true,
		NotSafe:       notSafe,
		AgentNote:     truncate(note, maxAgentNoteLength),
		Become:        become,
	})
}

// becomeSupported reports whether a command runs Ansible against this host with sudo
// (local provisioning on Linux and macOS), so the operator may supply a sudo password.
// Windows local provisioning uses a temporary administrator account instead.
func becomeSupported(args []string) bool {
	if runtime.GOOS == "windows" || len(args) < 2 || args[0] != "provision" {
		return false
	}
	for _, arg := range args[1:] {
		if arg == "-i" || arg == "--inventory" || arg == "--inventory-path" || strings.HasPrefix(arg, "--inventory-path=") || strings.HasPrefix(arg, "--inventory=") {
			// A custom inventory can point "local" at a remote host, so the sudo
			// password must not be offered.
			return false
		}
	}
	for i, arg := range args {
		if i == 0 {
			continue
		}
		if arg == "--" {
			return false
		}
		if !strings.HasPrefix(arg, "-") && !isFlagValue(args, i) {
			return arg == "local"
		}
	}
	return false
}

func validateRawArgs(args []string) error {
	if len(args) == 0 {
		return errors.New("args must not be empty")
	}
	if len(args) > maxArgs {
		return fmt.Errorf("at most %d arguments are allowed", maxArgs)
	}
	for _, arg := range args {
		if len(arg) > maxArgLength {
			return fmt.Errorf("arguments must be shorter than %d bytes", maxArgLength)
		}
		if strings.ContainsRune(arg, 0) {
			return errors.New("arguments must not contain NUL bytes")
		}
	}
	return nil
}

func peerOf(req *mcp.CallToolRequest) string {
	if req == nil || req.Extra == nil || req.Extra.Header == nil {
		return ""
	}
	return req.Extra.Header.Get(peerHeader)
}

// structParams records tool parameters for the audit log.
func structParams(in any) map[string]any {
	return map[string]any{"input": in}
}
