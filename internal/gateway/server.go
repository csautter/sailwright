package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	infoTimeout        = defaultInfoTimeout
	shutdownTimeout    = 5 * time.Second
	readHeaderTimeout  = 10 * time.Second
	idleTimeout        = 2 * time.Minute
	maxHeaderBytes     = 64 << 10
	maxInfoOutputBytes = 1 << 20
	auditFileName      = "audit.jsonl"
)

// Options configures a Gateway.
type Options struct {
	Config   Config
	Pinned   PinnedBinary
	Token    string
	Approver Approver
	Console  io.Writer
	// FollowOutput mirrors job output to Console.
	FollowOutput bool
	Version      string
	Getenv       func(string) string
}

// Gateway wires policy, execution, jobs and the MCP server together.
type Gateway struct {
	cfg        Config
	dirs       ManagedDirs
	pinned     PinnedBinary
	runner     *Runner
	catalog    *TargetCatalog
	jobs       *JobManager
	approver   Approver
	audit      *AuditLog
	redactor   *Redactor
	vmResolver ManagedVMResolver
	token      string
	console    io.Writer
	version    string
	server     *mcp.Server

	playbooksMu sync.RWMutex
	playbooks   []string
}

// New builds a gateway. The config must already be resolved.
func New(opts Options) (*Gateway, error) {
	cfg := opts.Config
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.Console == nil {
		opts.Console = io.Discard
	}
	if opts.Approver == nil {
		opts.Approver = DisabledApprover{}
	}
	if opts.Token == "" {
		return nil, errors.New("a bearer token is required")
	}

	if err := ensurePrivateDir(cfg.StateDir); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	dirs := NewManagedDirs(cfg)
	if err := dirs.Ensure(); err != nil {
		return nil, fmt.Errorf("create managed dirs: %w", err)
	}
	if root := findGitRoot(cfg.StateDir); root != "" {
		return nil, fmt.Errorf("the gateway state dir %s is inside the git checkout %s; sailwright would use that checkout's playbooks. Choose a state dir outside any repository", cfg.StateDir, root)
	}

	redactor := NewRedactor(append(PassthroughSecrets(cfg.EnvPassthrough, opts.Getenv), opts.Token)...)
	audit, err := OpenAuditLog(filepath.Join(cfg.StateDir, auditFileName), redactor)
	if err != nil {
		return nil, err
	}

	g := &Gateway{
		cfg:        cfg,
		dirs:       dirs,
		pinned:     opts.Pinned,
		runner:     &Runner{Binary: opts.Pinned, Env: BuildChildEnv(dirs, cfg.EnvPassthrough, opts.Getenv)},
		catalog:    NewTargetCatalog(),
		approver:   opts.Approver,
		audit:      audit,
		redactor:   redactor,
		vmResolver: noVMResolver{},
		token:      opts.Token,
		console:    opts.Console,
		version:    opts.Version,
	}
	g.jobs = NewJobManager(JobManagerOptions{
		Runner:      g.runner,
		Approver:    g.approver,
		Audit:       audit,
		Redactor:    redactor,
		LogsDir:     dirs.Logs,
		DataDir:     cfg.DataDir,
		MinFreeGB:   cfg.MinFreeDiskGB,
		OutputLimit: cfg.OutputLimitBytes,
		BinarySHA:   opts.Pinned.SHA256,
		OnFinish:    g.refreshTargetsAsync,
		Console:     opts.Console,
		Follow:      followWriter(opts.FollowOutput, opts.Console),
	})
	g.server = g.newMCPServer()
	return g, nil
}

// Close releases resources.
func (g *Gateway) Close() error {
	g.jobs.CancelAll()
	return g.audit.Close()
}

// Jobs exposes the job manager (tests and shutdown).
func (g *Gateway) Jobs() *JobManager { return g.jobs }

// Handler returns the HTTP handler with all security checks applied.
func (g *Gateway) Handler() http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return g.server }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// The security middleware checks Host against an explicit allowlist, which also
		// covers host.docker.internal; the SDK's localhost-only check would reject it.
		DisableLocalhostProtection: true,
		MaxRequestBodyBytes:        maxRequestBodyBytes,
	})
	return NewHTTPHandler(SecurityPolicy{
		Token:          g.token,
		SourcePrefixes: g.cfg.SourcePrefixes,
		AllowedHosts:   g.cfg.AllowedHosts,
		AllowedOrigins: g.cfg.AllowedOrigins,
		Audit:          g.audit,
	}, mcpHandler)
}

// Serve opens all listeners and serves until ctx is cancelled.
func (g *Gateway) Serve(ctx context.Context) error {
	listeners, err := g.Listen()
	if err != nil {
		return err
	}
	defer listeners.Close()
	return g.ServeListeners(ctx, listeners)
}

// ServeListener serves on a single TCP listener (tests).
func (g *Gateway) ServeListener(ctx context.Context, listener net.Listener) error {
	return g.ServeListeners(ctx, &Listeners{TCP: listener})
}

// RefreshTargets asks the pinned binary for every `<command> list --json`.
func (g *Gateway) RefreshTargets(ctx context.Context) {
	for _, command := range listCommands {
		targets, err := g.listTargets(ctx, command)
		g.catalog.Set(command, targets, err)
	}
	g.refreshPlaybooks()
}

func (g *Gateway) refreshTargetsAsync() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), infoTimeout)
		defer cancel()
		g.RefreshTargets(ctx)
	}()
}

func (g *Gateway) listTargets(ctx context.Context, command string) ([]Target, error) {
	output, code, err := g.runInfo(ctx, []string{command, "list", "--json"})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("`sailwright %s list --json` exited with %d: %s", command, code, errorSummary(output))
	}
	return ParseTargetListing([]byte(output))
}

// runInfo runs a short, read-only command outside the job slot.
func (g *Gateway) runInfo(ctx context.Context, argv []string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, infoTimeout)
	defer cancel()
	var buf limitedBuffer
	buf.limit = maxInfoOutputBytes
	writer := newLineRedactingWriter(g.redactor, &buf)
	code, err := g.runner.Run(ctx, argv, g.dirs.Work, writer)
	_ = writer.Flush()
	_ = g.audit.Record(AuditEvent{Event: "info_command", Argv: argv, ExitCode: &code, BinarySHA256: g.pinned.SHA256})
	return buf.String(), code, err
}

func (g *Gateway) refreshPlaybooks() {
	entries, err := os.ReadDir(g.dirs.PlaybookDir())
	var names []string
	if err == nil {
		for _, entry := range entries {
			name := strings.TrimSuffix(entry.Name(), ".yml")
			if entry.Type().IsRegular() && name != entry.Name() && playbookPattern.MatchString(name) {
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	g.playbooksMu.Lock()
	g.playbooks = names
	g.playbooksMu.Unlock()
}

func (g *Gateway) grammar() GrammarContext {
	g.playbooksMu.RLock()
	defer g.playbooksMu.RUnlock()
	return GrammarContext{
		Catalog:       g.catalog,
		PlaybookDir:   g.dirs.PlaybookDir(),
		Playbooks:     append([]string(nil), g.playbooks...),
		PullAllowlist: g.cfg.PullAllowlist,
	}
}

// limitedBuffer keeps at most limit bytes and drops the rest.
type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		if len(p) > room {
			_, _ = b.Buffer.Write(p[:room])
		} else {
			_, _ = b.Buffer.Write(p)
		}
	}
	return len(p), nil
}

// errorSummary returns the cobra "Error:" line of CLI output, or its last lines.
func errorSummary(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Error: ") {
			return strings.TrimPrefix(line, "Error: ")
		}
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.Join(lines, " | ")
}

func followWriter(enabled bool, console io.Writer) io.Writer {
	if !enabled {
		return nil
	}
	return console
}
