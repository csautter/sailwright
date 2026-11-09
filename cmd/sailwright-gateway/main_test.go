package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/internal/gateway"
)

func TestBannerShowsTCPState(t *testing.T) {
	var off bytes.Buffer
	writeBanner(&off, gateway.Config{}, gateway.PinnedBinary{}, "/run/agent/gateway.sock", "", gateway.DisabledApprover{})
	if !strings.Contains(off.String(), "TCP off (agent socket only; set listen: to enable)") {
		t.Fatalf("expected the TCP-off hint, got:\n%s", off.String())
	}

	var on bytes.Buffer
	cfg := gateway.Config{ListenAddr: netip.MustParseAddrPort("127.0.0.1:7391")}
	writeBanner(&on, cfg, gateway.PinnedBinary{}, "", "", gateway.DisabledApprover{})
	if !strings.Contains(on.String(), "http://127.0.0.1:7391/mcp") || strings.Contains(on.String(), "TCP off") {
		t.Fatalf("expected the TCP URL, got:\n%s", on.String())
	}
}

func TestBannerShowsApprovalState(t *testing.T) {
	// Keep the console readable: an empty reader reaches EOF asynchronously and
	// disables approvals before the banner can reliably observe the enabled state.
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		_ = writer.Close()
		_ = reader.Close()
	})
	console := gateway.NewConsoleApprover(bufio.NewReader(reader), io.Discard, time.Minute)
	cases := []struct {
		name     string
		approver gateway.Approver
		want     string
	}{
		{"nil", nil, "approvals:  disabled (safe set only)"},
		{"disabled", gateway.DisabledApprover{}, "approvals:  disabled (safe set only)"},
		{"bypass", gateway.AutoApprover{}, "approvals:  BYPASSED (--bypass-approvals approves everything automatically)"},
		{"bypass pointer", &gateway.AutoApprover{}, "approvals:  BYPASSED (--bypass-approvals approves everything automatically)"},
		{"open console", console, "approvals:  enabled on this terminal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			writeBanner(&out, gateway.Config{}, gateway.PinnedBinary{}, "", "", tc.approver)
			if !strings.Contains(out.String(), tc.want+"\n") {
				t.Fatalf("expected %q, got:\n%s", tc.want, out.String())
			}
		})
	}
}

func TestBannerShowsClosedConsoleApprovalState(t *testing.T) {
	console := gateway.NewConsoleApprover(bufio.NewReader(strings.NewReader("")), io.Discard, time.Minute)
	// EOF is consumed by the console goroutine. Wait for that state change instead
	// of assuming which goroutine runs first on a particular OS.
	deadline := time.Now().Add(5 * time.Second)
	for console.Enabled() {
		if time.Now().After(deadline) {
			t.Fatal("approval console did not close after EOF")
		}
		time.Sleep(time.Millisecond)
	}
	var out bytes.Buffer
	writeBanner(&out, gateway.Config{}, gateway.PinnedBinary{}, "", "", console)
	if want := "approvals:  disabled (safe set only)\n"; !strings.Contains(out.String(), want) {
		t.Fatalf("expected %q after console EOF, got:\n%s", want, out.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if got := console.Request(ctx, gateway.ApprovalRequest{ID: "closed-console"}); got.Decision != gateway.DecisionDenied {
		t.Fatalf("closed console must deny approval, got %s", got.Decision)
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
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
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

func TestLiveTailState(t *testing.T) {
	tests := []struct {
		name       string
		flags      serveFlags
		terminal   bool
		vt         bool
		wantShow   bool
		wantReason string
		wantVT     bool
	}{
		{name: "shown on a VT terminal", flags: serveFlags{liveLines: 8}, terminal: true, vt: true, wantShow: true, wantVT: true},
		{name: "off with zero lines", flags: serveFlags{liveLines: 0}, terminal: true, vt: true},
		{name: "follow wins", flags: serveFlags{liveLines: 8, follow: true}, terminal: true, vt: true, wantReason: "--follow"},
		{name: "not a terminal", flags: serveFlags{liveLines: 8}, terminal: false, vt: true, wantReason: "not a terminal"},
		{name: "no VT support", flags: serveFlags{liveLines: 8}, terminal: true, vt: false, wantReason: "VT sequences", wantVT: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vtCalled, restored := false, false
			show, restore, reason := liveTailState(tt.flags,
				func() bool { return tt.terminal },
				func() (func(), bool) {
					vtCalled = true
					return func() { restored = true }, tt.vt
				})
			if show != tt.wantShow {
				t.Fatalf("show = %v, want %v", show, tt.wantShow)
			}
			if tt.wantReason == "" && reason != "" || !strings.Contains(reason, tt.wantReason) {
				t.Fatalf("reason = %q, want it to mention %q", reason, tt.wantReason)
			}
			if vtCalled != tt.wantVT {
				t.Fatalf("VT probed = %v, want %v", vtCalled, tt.wantVT)
			}
			restore()
			if restored != tt.wantVT {
				t.Fatalf("restore ran = %v, want %v", restored, tt.wantVT)
			}
		})
	}
}
