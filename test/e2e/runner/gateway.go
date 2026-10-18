package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/csautter/dev-alchemy/internal/gateway/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultPollInterval = 2 * time.Second
	outputChunkBytes    = 256 << 10
)

// GatewayExecutor runs steps through sailwright-gateway over MCP. Every step passes the
// gateway policy: safe steps run directly, everything else waits for host approval.
type GatewayExecutor struct {
	Endpoint     client.Endpoint
	PollInterval time.Duration
	// Stream receives live job output (optional).
	Stream io.Writer

	session *mcp.ClientSession
}

// gatewayJob mirrors the gateway's job view.
type gatewayJob struct {
	ID       string   `json:"job_id"`
	State    string   `json:"state"`
	Tier     string   `json:"tier"`
	Argv     []string `json:"argv"`
	ExitCode *int     `json:"exit_code"`
	Error    string   `json:"error"`
}

func (j gatewayJob) done() bool {
	return j.State != "pending_approval" && j.State != "running"
}

// Connect opens the MCP session.
func (g *GatewayExecutor) Connect(ctx context.Context) error {
	session, err := client.Connect(ctx, g.Endpoint, &mcp.Implementation{Name: "sailwright-e2e", Version: "1"})
	if err != nil {
		return err
	}
	g.session = session
	return nil
}

// Close closes the MCP session.
func (g *GatewayExecutor) Close() error {
	if g.session == nil {
		return nil
	}
	return g.session.Close()
}

// Name implements Executor.
func (g *GatewayExecutor) Name() string { return ExecutorGateway }

// Describe implements Executor.
func (g *GatewayExecutor) Describe(ctx context.Context) (Environment, error) {
	var info struct {
		HostOS       string `json:"host_os"`
		HostArch     string `json:"host_arch"`
		BinarySHA256 string `json:"binary_sha256"`
		Version      string `json:"gateway_version"`
	}
	if err := g.call(ctx, "gateway_info", map[string]any{}, &info); err != nil {
		return Environment{}, err
	}
	return Environment{Executor: ExecutorGateway, HostOS: info.HostOS, HostArch: info.HostArch, BinarySHA256: info.BinarySHA256, Gateway: info.Version + " via " + g.Endpoint.String()}, nil
}

// HostChangesAllowed implements Executor.
func (g *GatewayExecutor) HostChangesAllowed(ctx context.Context) (bool, string) {
	var info struct {
		ApprovalEnabled bool `json:"approval_enabled"`
	}
	if err := g.call(ctx, "gateway_info", map[string]any{}, &info); err != nil {
		return false, err.Error()
	}
	if !info.ApprovalEnabled {
		return false, "the gateway has no approval console"
	}
	return true, ""
}

// List implements Executor.
func (g *GatewayExecutor) List(ctx context.Context, command string) ([]ListedTarget, error) {
	var out struct {
		Targets []ListedTarget `json:"targets"`
	}
	if err := g.call(ctx, "list_targets", map[string]any{"command": command}, &out); err != nil {
		return nil, err
	}
	return out.Targets, nil
}

// Run implements Executor.
func (g *GatewayExecutor) Run(ctx context.Context, req StepRequest) (Result, error) {
	tool, args, err := gatewayCall(req)
	if err != nil {
		return Result{}, err
	}

	var job gatewayJob
	if err := g.call(ctx, tool, args, &job); err != nil {
		return Result{}, err
	}
	if job.State == "pending_approval" && req.Logf != nil {
		req.Logf("job %s waits for approval on the gateway terminal: type \"approve %s\" there", job.ID, job.ID)
	}

	interval := g.PollInterval
	if interval == 0 {
		interval = defaultPollInterval
	}
	follower := &outputFollower{executor: g, jobID: job.ID, stream: g.Stream}
	for !job.done() {
		select {
		case <-ctx.Done():
			_ = g.call(context.Background(), "job_cancel", map[string]any{"job_id": job.ID}, nil)
			return Result{Argv: job.Argv, Output: follower.output.String()}, ctx.Err()
		case <-time.After(interval):
		}
		if err := g.call(ctx, "job_status", map[string]any{"job_id": job.ID}, &job); err != nil {
			return Result{Argv: job.Argv, Output: follower.output.String()}, err
		}
		if err := follower.fetch(ctx); err != nil {
			return Result{Argv: job.Argv, Output: follower.output.String()}, err
		}
	}

	if err := follower.fetch(ctx); err != nil {
		return Result{Argv: job.Argv, Output: follower.output.String()}, err
	}
	result := Result{Argv: job.Argv, Output: follower.output.String(), ExitCode: -1, Note: job.Tier}
	if job.ExitCode != nil {
		result.ExitCode = *job.ExitCode
	}
	if job.State == "denied" || job.State == "expired" || job.State == "cancelled" {
		return result, fmt.Errorf("job %s %s: %s", job.ID, job.State, job.Error)
	}
	return result, nil
}

// outputFollower reads new job output while the job runs and mirrors it to stream.
type outputFollower struct {
	executor *GatewayExecutor
	jobID    string
	stream   io.Writer
	offset   int64
	output   strings.Builder
}

func (f *outputFollower) fetch(ctx context.Context) error {
	for {
		var chunk struct {
			Data       string `json:"data"`
			NextOffset int64  `json:"next_offset"`
		}
		if err := f.executor.call(ctx, "job_output", map[string]any{"job_id": f.jobID, "offset": f.offset, "max_bytes": outputChunkBytes}, &chunk); err != nil {
			return err
		}
		if chunk.Data == "" || chunk.NextOffset <= f.offset {
			return nil
		}
		f.output.WriteString(chunk.Data)
		if f.stream != nil {
			_, _ = io.WriteString(f.stream, chunk.Data)
		}
		f.offset = chunk.NextOffset
	}
}

// gatewayCall maps a step to a gateway tool call.
func gatewayCall(req StepRequest) (string, map[string]any, error) {
	step := req.Step
	getenv := req.Getenv
	target := map[string]any{"os": req.Target.OS}
	if req.Target.Type != "" {
		target["type"] = req.Target.Type
	}
	if req.Target.Arch != "" {
		target["arch"] = req.Target.Arch
	}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range target {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	switch step.Op {
	case OpBuild:
		return "vm_build", with(map[string]any{"engine": step.String("engine", getenv), "headless": step.Bool("headless")}), nil
	case OpCreate, OpStart, OpStop, OpDestroy:
		return "vm_" + step.Op, with(nil), nil
	case OpProvision:
		extra := map[string]any{"check": step.Bool("check")}
		if verbosity, ok := step.Int("verbosity"); ok {
			extra["verbosity"] = verbosity
		}
		if playbook := step.String("playbook", getenv); playbook != "" {
			extra["playbook"] = playbook
		}
		return "vm_provision", with(extra), nil
	case OpPull:
		ref := step.String("ref", getenv)
		if ref == "" {
			return "", nil, errors.New("pull needs with.ref")
		}
		return "artifact_pull", with(map[string]any{"ref": ref, "engine": step.String("engine", getenv)}), nil
	case OpProvisionLocal:
		args := map[string]any{"check": step.Bool("check"), "reason": "E2E scenario step: " + step.Name}
		if playbook := step.String("playbook", getenv); playbook != "" {
			args["playbook"] = playbook
		}
		return "provision_local", args, nil
	case OpList:
		return "run_cli", map[string]any{"args": []string{step.String("command", getenv), "list", "--json"}}, nil
	case OpCLI:
		return "run_cli", map[string]any{"args": step.Args(getenv), "reason": "E2E scenario step: " + step.Name}, nil
	}
	return "", nil, fmt.Errorf("%w: %s", ErrStepNotSupported, step.Op)
}

func (g *GatewayExecutor) call(ctx context.Context, tool string, args map[string]any, out any) error {
	if g.session == nil {
		return errors.New("gateway executor is not connected")
	}
	result, err := g.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return fmt.Errorf("%s: %w", tool, err)
	}
	if result.IsError {
		var parts []string
		for _, content := range result.Content {
			if text, ok := content.(*mcp.TextContent); ok {
				parts = append(parts, text.Text)
			}
		}
		return fmt.Errorf("%s: %s", tool, strings.Join(parts, " "))
	}
	if out == nil {
		return nil
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
