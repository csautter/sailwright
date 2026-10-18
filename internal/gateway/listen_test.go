package gateway

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestListenAgentSocketHandlesExistingFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no agent socket on Windows")
	}
	dir, err := os.MkdirTemp("", "swl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, AgentSocketName)

	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	listener, _, err := ListenAgentSocket(dir)
	if err != nil {
		t.Fatalf("a stale socket must be replaced: %v", err)
	}

	if _, _, err := ListenAgentSocket(dir); err == nil || !strings.Contains(err.Error(), "already listening") {
		t.Fatalf("a live socket must not be replaced, got %v", err)
	}
	_ = listener.Close()

	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ListenAgentSocket(dir); err == nil {
		t.Fatal("a regular file must not be deleted")
	}
}

func TestMiddlewareTrustsOnlyTheSocketForNetworkChecks(t *testing.T) {
	policy := newTestPolicy(t)
	handler := policy.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Header.Get(peerHeader)))
	}))

	request := func(local bool, auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
		req.RemoteAddr = "@"
		req.Host = "anything"
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		if local {
			req = req.WithContext(context.WithValue(req.Context(), localSocketKey{}, true))
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	if rec := request(true, "Bearer "+policy.Token); rec.Code != http.StatusOK || rec.Body.String() != "unix-socket" {
		t.Fatalf("socket request with token: %d %q", rec.Code, rec.Body.String())
	}
	if rec := request(true, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("socket requests must still need the token, got %d", rec.Code)
	}
	if rec := request(false, "Bearer "+policy.Token); rec.Code != http.StatusForbidden {
		t.Fatalf("network requests must keep the source check, got %d", rec.Code)
	}
}

func TestConfigRejectsUnsafeAgentDir(t *testing.T) {
	state := t.TempDir()
	workspace := t.TempDir()
	cases := []Config{
		{AgentDir: state},
		{AgentDir: filepath.Join(state, "agent")},
		{AgentDir: filepath.Join(workspace, ".gateway"), AgentWorkspaces: []WorkspaceMapping{{Agent: "/workspaces/x", Host: workspace}}},
	}
	for i, cfg := range cases {
		cfg.StateDir = state
		if err := cfg.Resolve(); err == nil {
			t.Fatalf("case %d: expected agent_dir %s to be rejected", i, cfg.AgentDir)
		}
	}
	ok := Config{StateDir: state}
	if err := ok.Resolve(); err != nil || !strings.HasSuffix(ok.AgentDir, defaultAgentDirName) {
		t.Fatalf("default agent dir: %q, %v", ok.AgentDir, err)
	}
}
