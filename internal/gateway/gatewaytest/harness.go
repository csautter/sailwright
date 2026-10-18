// Package gatewaytest starts a real sailwright-gateway against a fake sailwright CLI.
// It is shared by the gateway integration tests and the E2E module's self-test.
package gatewaytest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/csautter/dev-alchemy/internal/gateway/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AgentWorkspace is the agent-side path mapped to Harness.WorkspaceHost.
const AgentWorkspace = "/workspaces/test"

var (
	fakeCLIOnce sync.Once
	fakeCLIPath string
	fakeCLIErr  error
)

// BuildFakeCLI compiles the fake sailwright CLI once per test process.
func BuildFakeCLI(t testing.TB) string {
	t.Helper()
	fakeCLIOnce.Do(func() {
		dir, err := os.MkdirTemp("", "sailwright-fakecli-")
		if err != nil {
			fakeCLIErr = err
			return
		}
		name := "sailwright"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		fakeCLIPath = filepath.Join(dir, name)
		_, self, _, _ := runtime.Caller(0)
		source := filepath.Join(filepath.Dir(self), "..", "testdata", "fakecli")
		// #nosec G204 -- test helper compiling a fixed in-repo program.
		cmd := exec.Command("go", "build", "-o", fakeCLIPath, ".")
		cmd.Dir = source
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeCLIErr = fmt.Errorf("build fake CLI: %v\n%s", err, out)
		}
	})
	if fakeCLIErr != nil {
		t.Fatal(fakeCLIErr)
	}
	return fakeCLIPath
}

// Options configure a test gateway.
type Options struct {
	Approvals     bool
	ApprovalTTL   time.Duration
	PullAllowlist []string
	JobTimeout    string
	FollowOutput  bool
}

// Harness is a running gateway with a fake CLI behind it.
type Harness struct {
	URL           string
	SocketPath    string
	ViewURL       string
	AgentDir      string
	Token         string
	StateDir      string
	RecordFile    string
	FakeStateDir  string
	WorkspaceHost string
	Gateway       *gateway.Gateway
	Console       *SyncBuffer

	approvals io.WriteCloser
}

// Start launches a gateway on a random loopback port.
func Start(t testing.TB, opts Options) *Harness {
	t.Helper()
	fake := BuildFakeCLI(t)
	h := &Harness{
		StateDir:      t.TempDir(),
		FakeStateDir:  t.TempDir(),
		WorkspaceHost: t.TempDir(),
		Console:       &SyncBuffer{},
	}
	h.RecordFile = filepath.Join(t.TempDir(), "invocations.jsonl")

	// Unix socket paths are limited to ~104 bytes, so keep the agent dir short.
	agentDir, err := os.MkdirTemp("", "swgw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(agentDir) })
	h.AgentDir = agentDir

	jobTimeout := opts.JobTimeout
	if jobTimeout == "" {
		jobTimeout = "2m"
	}
	cfg := gateway.Config{
		StateDir:        h.StateDir,
		Listen:          "127.0.0.1:0",
		EnvPassthrough:  []string{"FAKE_CLI_RECORD", "FAKE_CLI_STATE", "FAKE_VM_PASSWORD"},
		PullAllowlist:   opts.PullAllowlist,
		AgentWorkspaces: []gateway.WorkspaceMapping{{Agent: AgentWorkspace, Host: h.WorkspaceHost}},
		JobTimeout:      jobTimeout,
		ApprovalTTL:     durationString(opts.ApprovalTTL),
		MinFreeDiskGB:   -1,
		AgentDir:        agentDir,
	}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}

	token, err := gateway.LoadOrCreateToken(h.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	h.Token = token

	pinned, err := gateway.PinBinary(fake, h.StateDir, "")
	if err != nil {
		t.Fatal(err)
	}

	var approver gateway.Approver = gateway.DisabledApprover{}
	if opts.Approvals {
		reader, writer := io.Pipe()
		h.approvals = writer
		// A pipe has no echo to hide.
		approver = gateway.NewConsoleApprover(bufio.NewReader(reader), h.Console, cfg.ApprovalTTLValue).
			HideInputWith(func() (func(), error) { return func() {}, nil })
		t.Cleanup(func() { _ = writer.Close() })
	}

	// #nosec G101 -- fake values that prove secrets are redacted and host variables dropped.
	env := map[string]string{
		"FAKE_CLI_RECORD":  h.RecordFile,
		"FAKE_CLI_STATE":   h.FakeStateDir,
		"FAKE_VM_PASSWORD": "vm-secret-value",
		"SSH_AUTH_SOCK":    "/tmp/host-agent.sock",
		"GITHUB_TOKEN":     "host-github-token",
	}
	g, err := gateway.New(gateway.Options{
		Config:       cfg,
		Pinned:       pinned,
		Token:        token,
		Approver:     approver,
		Console:      h.Console,
		Version:      "test",
		FollowOutput: opts.FollowOutput,
		Getenv: func(name string) string {
			if value, ok := env[name]; ok {
				return value
			}
			return os.Getenv(name)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.Gateway = g

	ctx, cancel := context.WithCancel(context.Background())
	g.RefreshTargets(ctx)
	listeners, err := g.Listen()
	if err != nil {
		t.Fatal(err)
	}
	h.URL = "http://" + listeners.TCP.Addr().String() + "/mcp"
	h.SocketPath = listeners.SocketPath
	h.ViewURL = listeners.ViewURL
	done := make(chan struct{})
	go func() {
		_ = g.ServeListeners(ctx, listeners)
		listeners.Close()
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = g.Close()
	})
	return h
}

func durationString(d time.Duration) string {
	if d == 0 {
		return ""
	}
	return d.String()
}

// TCPEndpoint returns the TCP endpoint with the harness token.
func (h *Harness) TCPEndpoint() client.Endpoint {
	return client.Endpoint{URL: h.URL, Token: h.Token, Source: "test"}
}

// SocketEndpoint returns the Unix socket endpoint (empty Socket on Windows).
func (h *Harness) SocketEndpoint() client.Endpoint {
	return client.Endpoint{URL: "http://localhost/mcp", Socket: h.SocketPath, Token: h.Token, Source: "test"}
}

// Connect opens an MCP client session over TCP with the harness token.
func (h *Harness) Connect(t testing.TB) *mcp.ClientSession {
	t.Helper()
	return h.ConnectTo(t, h.TCPEndpoint())
}

// ConnectTo opens an MCP client session to the given endpoint.
func (h *Harness) ConnectTo(t testing.TB, endpoint client.Endpoint) *mcp.ClientSession {
	t.Helper()
	session, err := client.Connect(context.Background(), endpoint, &mcp.Implementation{Name: "gatewaytest", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// Approve answers the pending approval for jobID on the gateway console.
func (h *Harness) Approve(t testing.TB, jobID string) { h.decide(t, "approve", jobID) }

// ApproveSudo approves jobID with a sudo password, as the operator would type it.
func (h *Harness) ApproveSudo(t testing.TB, jobID string, password string) {
	t.Helper()
	h.decide(t, "approve-sudo", jobID)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(h.Console.String(), "sudo password for job "+jobID) {
		if time.Now().After(deadline) {
			t.Fatalf("no password prompt for %s; console:\n%s", jobID, h.Console.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := fmt.Fprintf(h.approvals, "%s\n", password); err != nil {
		t.Fatal(err)
	}
}

// Deny rejects the pending approval for jobID on the gateway console.
func (h *Harness) Deny(t testing.TB, jobID string) { h.decide(t, "deny", jobID) }

// WaitForPrompt blocks until the console shows the approval prompt for jobID.
func (h *Harness) WaitForPrompt(t testing.TB, jobID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(h.Console.String(), "approve "+jobID) {
		if time.Now().After(deadline) {
			t.Fatalf("approval prompt for %s not shown; console:\n%s", jobID, h.Console.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *Harness) decide(t testing.TB, verb string, jobID string) {
	t.Helper()
	if h.approvals == nil {
		t.Fatal("harness was started without approvals")
	}
	h.WaitForPrompt(t, jobID)
	if _, err := fmt.Fprintf(h.approvals, "%s %s\n", verb, jobID); err != nil {
		t.Fatal(err)
	}
}

// Invocation is one recorded fake CLI call.
type Invocation struct {
	Argv []string `json:"argv"`
	Cwd  string   `json:"cwd"`
	Env  []string `json:"env"`
}

// Invocations returns every recorded fake CLI call.
func (h *Harness) Invocations(t testing.TB) []Invocation {
	t.Helper()
	data, err := os.ReadFile(h.RecordFile)
	if err != nil {
		t.Fatal(err)
	}
	var out []Invocation
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var inv Invocation
		if err := json.Unmarshal([]byte(line), &inv); err != nil {
			t.Fatal(err)
		}
		out = append(out, inv)
	}
	return out
}

// SyncBuffer is a goroutine-safe text buffer.
type SyncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

// Write implements io.Writer.
func (b *SyncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns the buffered text.
func (b *SyncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
