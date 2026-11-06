package gateway

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestGatewayCloseWaitsForVMResolver(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fakecli")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	// #nosec G204 -- compile a fixed in-repo test helper.
	if out, err := exec.Command("go", "build", "-o", binary, "./testdata/fakecli").CombinedOutput(); err != nil {
		t.Fatalf("build fake CLI: %v: %s", err, out)
	}
	state, record := t.TempDir(), filepath.Join(t.TempDir(), "record.jsonl")
	if err := os.WriteFile(filepath.Join(state, "ubuntu-server-amd64"), []byte("running"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLI_STATE", state)
	t.Setenv("FAKE_CLI_RECORD", record)
	t.Setenv("FAKE_VM_INFO_DELAY", "20s")
	cfg := Config{StateIsolation: "isolated", StateDir: t.TempDir(), AgentDir: t.TempDir(), EnvPassthrough: []string{"FAKE_CLI_STATE", "FAKE_CLI_RECORD", "FAKE_VM_INFO_DELAY"}}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	pinned, err := PinBinary(binary, cfg.StateDir, "")
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(Options{Config: cfg, Pinned: pinned, Token: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	resolver := g.vmResolver.(*CLIManagedVMResolver)
	result := make(chan error, 1)
	// A request independent of the gateway context must still be cancelled by Close.
	go func() {
		_, err := resolver.ResolveFresh(context.Background(), "ubuntu", "server", "amd64")
		result <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(record) // #nosec G304 -- test temp file.
		if len(data) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake CLI did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	closed := make(chan error, 1)
	go func() { closed <- g.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gateway shutdown did not cancel lookup")
	}
	resolver.mu.Lock()
	remaining := len(resolver.inflight)
	resolver.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d CLI workers survived gateway shutdown", remaining)
	}
	if err := <-result; err == nil {
		t.Fatal("cancelled CLI lookup succeeded")
	}
	if _, err := resolver.ResolveFresh(context.Background(), "ubuntu", "server", "amd64"); err == nil {
		t.Fatal("closed resolver accepted another lookup")
	}
}
