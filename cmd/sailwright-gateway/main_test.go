package main

import (
	"bytes"
	"net/netip"
	"strings"
	"testing"

	"github.com/csautter/dev-alchemy/internal/gateway"
)

func TestBannerShowsTCPState(t *testing.T) {
	var off bytes.Buffer
	writeBanner(&off, gateway.Config{}, gateway.PinnedBinary{}, "/run/agent/gateway.sock", "", false)
	if !strings.Contains(off.String(), "TCP off (agent socket only; set listen: to enable)") {
		t.Fatalf("expected the TCP-off hint, got:\n%s", off.String())
	}

	var on bytes.Buffer
	cfg := gateway.Config{ListenAddr: netip.MustParseAddrPort("127.0.0.1:7391")}
	writeBanner(&on, cfg, gateway.PinnedBinary{}, "", "", false)
	if !strings.Contains(on.String(), "http://127.0.0.1:7391/mcp") || strings.Contains(on.String(), "TCP off") {
		t.Fatalf("expected the TCP URL, got:\n%s", on.String())
	}
}

func runClean(t *testing.T, stateDir string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := newRootCommand()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--state-dir", stateDir, "clean", "--yes"})
	err := root.Execute()
	return out.String(), err
}

func TestCleanRefusesWhileStateDirIsLocked(t *testing.T) {
	stateDir := t.TempDir()
	lock, err := gateway.AcquireStateLock(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runClean(t, stateDir); err == nil || !strings.Contains(err.Error(), "stop it before cleaning") {
		t.Fatalf("expected clean to refuse while a gateway runs, got %v", err)
	}

	lock.Release()
	out, err := runClean(t, stateDir)
	if err != nil {
		t.Fatalf("clean must work once the lock is free: %v", err)
	}
	if !strings.Contains(out, "cache is empty") {
		t.Fatalf("unexpected output %q", out)
	}
}
