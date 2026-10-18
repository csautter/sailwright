package client_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/internal/gateway/client"
	"github.com/csautter/dev-alchemy/internal/gateway/gatewaytest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func envFrom(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "swc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestDiscover(t *testing.T) {
	dir := shortTempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	endpoint, err := client.Discover(envFrom(map[string]string{client.EnvDir: dir}))
	if err != nil || endpoint.URL != client.DefaultTCPURL || endpoint.Socket != "" || endpoint.Token != "file-token" {
		t.Fatalf("expected the TCP fallback with the file token, got %+v, %v", endpoint, err)
	}

	endpoint, err = client.Discover(envFrom(map[string]string{client.EnvDir: dir, client.EnvURL: "http://10.0.0.1:1/mcp", client.EnvToken: "env-token"}))
	if err != nil || endpoint.URL != "http://10.0.0.1:1/mcp" || endpoint.Token != "env-token" {
		t.Fatalf("explicit URL and token must win, got %+v, %v", endpoint, err)
	}

	endpoint, err = client.Discover(envFrom(map[string]string{client.EnvDir: dir, client.EnvURL: "unix:///tmp/x.sock"}))
	if err != nil || endpoint.Socket != "/tmp/x.sock" {
		t.Fatalf("expected a unix URL to select the socket, got %+v, %v", endpoint, err)
	}

	if _, err := client.Discover(envFrom(map[string]string{client.EnvDir: filepath.Join(dir, "missing")})); err == nil {
		t.Fatal("a missing token must be an error")
	}

	if runtime.GOOS == "windows" {
		return
	}
	// A socket that is not accepting connections (e.g. Docker Desktop) is skipped.
	stale := filepath.Join(dir, "gateway.sock")
	listener, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = listener.Close()
	endpoint, _ = client.Discover(envFrom(map[string]string{client.EnvDir: dir}))
	if endpoint.Socket != "" {
		t.Fatalf("a dead socket must fall back to TCP, got %+v", endpoint)
	}
	_ = os.Remove(stale)

	live, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	endpoint, err = client.Discover(envFrom(map[string]string{client.EnvDir: dir}))
	if err != nil || endpoint.Socket != stale {
		t.Fatalf("expected the live socket, got %+v, %v", endpoint, err)
	}
}

func TestDiscoverFindsRealGatewaySocket(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{})
	if h.SocketPath == "" {
		t.Skip("no agent socket on this OS")
	}
	endpoint, err := client.Discover(envFrom(map[string]string{client.EnvDir: h.AgentDir}))
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Socket != h.SocketPath || endpoint.Token != h.Token {
		t.Fatalf("expected the harness socket and token, got %+v", endpoint)
	}
	session, err := client.Connect(context.Background(), endpoint, &mcp.Implementation{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	_ = session.Close()
}

func TestBridgeForwardsToolsAndCalls(t *testing.T) {
	for _, viaSocket := range []bool{false, true} {
		h := gatewaytest.Start(t, gatewaytest.Options{})
		endpoint := h.TCPEndpoint()
		if viaSocket {
			if h.SocketPath == "" {
				continue
			}
			endpoint = h.SocketEndpoint()
		}

		ctx, cancel := context.WithCancel(context.Background())
		serverTransport, clientTransport := mcp.NewInMemoryTransports()
		bridge := &client.Bridge{Endpoint: endpoint, Version: "test"}
		done := make(chan error, 1)
		go func() { done <- bridge.Run(ctx, serverTransport) }()

		downstream, err := mcp.NewClient(&mcp.Implementation{Name: "agent"}, nil).Connect(ctx, clientTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(downstream.InitializeResult().Instructions, "human") {
			t.Fatalf("gateway instructions must be forwarded, got %q", downstream.InitializeResult().Instructions)
		}

		tools, err := downstream.ListTools(ctx, nil)
		if err != nil || len(tools.Tools) != 17 {
			t.Fatalf("expected the 17 gateway tools, got %d (%v)", len(tools.Tools), err)
		}

		result, err := downstream.CallTool(ctx, &mcp.CallToolParams{Name: "vm_create", Arguments: map[string]any{"os": "ubuntu"}})
		if err != nil || result.IsError {
			t.Fatalf("vm_create through the bridge failed: %v %+v", err, result)
		}
		var job struct {
			ID   string `json:"job_id"`
			Tier string `json:"tier"`
		}
		data, _ := json.Marshal(result.StructuredContent)
		if err := json.Unmarshal(data, &job); err != nil || job.ID == "" || job.Tier != "safe" {
			t.Fatalf("unexpected job %s", data)
		}

		deadline := time.Now().Add(10 * time.Second)
		for {
			status, err := downstream.CallTool(ctx, &mcp.CallToolParams{Name: "job_status", Arguments: map[string]any{"job_id": job.ID}})
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(status.StructuredContent)
			if strings.Contains(string(data), `"state":"succeeded"`) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("job did not finish: %s", data)
			}
			time.Sleep(20 * time.Millisecond)
		}

		bad, err := downstream.CallTool(ctx, &mcp.CallToolParams{Name: "vm_create", Arguments: map[string]any{"os": "local"}})
		if err != nil || !bad.IsError {
			t.Fatalf("policy errors must reach the agent as tool errors: %v %+v", err, bad)
		}

		_ = downstream.Close()
		cancel()
		<-done
	}
}
