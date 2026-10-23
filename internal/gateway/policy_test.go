package gateway

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestPolicy(t *testing.T) SecurityPolicy {
	t.Helper()
	audit, err := OpenAuditLog(filepath.Join(t.TempDir(), "audit.jsonl"), NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })
	return SecurityPolicy{
		Token:          strings.Repeat("ab", 32),
		SourcePrefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("172.17.0.0/16")},
		AllowedHosts:   []string{"localhost", "127.0.0.1", "host.docker.internal"},
		Audit:          audit,
	}
}

func TestSecurityMiddleware(t *testing.T) {
	policy := newTestPolicy(t)
	var sawPeer string
	handler := policy.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPeer = r.Header.Get(peerHeader)
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name   string
		remote string
		host   string
		auth   string
		origin string
		want   int
	}{
		{"valid loopback", "127.0.0.1:5555", "localhost:7391", "Bearer " + policy.Token, "", http.StatusOK},
		{"valid docker host", "172.17.0.2:5555", "host.docker.internal:7391", "bearer " + policy.Token, "", http.StatusOK},
		{"missing token", "127.0.0.1:5555", "localhost", "", "", http.StatusUnauthorized},
		{"wrong token", "127.0.0.1:5555", "localhost", "Bearer " + strings.Repeat("cd", 32), "", http.StatusUnauthorized},
		{"token prefix only", "127.0.0.1:5555", "localhost", "Bearer " + policy.Token[:10], "", http.StatusUnauthorized},
		{"basic auth", "127.0.0.1:5555", "localhost", "Basic " + policy.Token, "", http.StatusUnauthorized},
		{"rebinding host", "127.0.0.1:5555", "evil.example:7391", "Bearer " + policy.Token, "", http.StatusForbidden},
		{"browser origin", "127.0.0.1:5555", "localhost", "Bearer " + policy.Token, "https://evil.example", http.StatusForbidden},
		{"vm network source", "192.168.122.10:5555", "localhost", "Bearer " + policy.Token, "", http.StatusForbidden},
		{"lan source", "10.0.0.5:5555", "localhost", "Bearer " + policy.Token, "", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
			req.RemoteAddr = tc.remote
			req.Host = tc.host
			req.Header.Set(peerHeader, "spoofed")
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			sawPeer = ""
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d", rec.Code, tc.want)
			}
			if tc.want == http.StatusOK && sawPeer != tc.remote {
				t.Fatalf("peer header %q, want the verified remote address %q", sawPeer, tc.remote)
			}
		})
	}
}

func TestConfigResolveRejectsUnsafeSettings(t *testing.T) {
	state := t.TempDir()
	workspace := t.TempDir()
	cases := []Config{
		{AgentWorkspaces: []WorkspaceMapping{{Agent: "/workspaces/x", Host: workspace}}, SharedCacheDir: filepath.Join(workspace, "cache")},
		{Listen: "0.0.0.0:7391"},
		{Listen: "[::]:7391"},
		{Listen: "localhost:7391"},
		{AllowedSourceCIDRs: []string{"0.0.0.0/0"}},
		{EnvPassthrough: []string{"BAD NAME"}},
		{PullAllowlist: []string{"ghcr.io"}},
		{AgentWorkspaces: []WorkspaceMapping{{Agent: "relative", Host: state}}},
		{AgentWorkspaces: []WorkspaceMapping{{Agent: "/workspaces/x", Host: filepath.Dir(state)}}},
		{BinarySHA256: "XYZ"},
		{JobTimeout: "-1s"},
	}
	for i, cfg := range cases {
		cfg.StateDir = state
		if err := cfg.Resolve(); err == nil {
			t.Fatalf("case %d: expected %+v to be rejected", i, cfg)
		}
	}

	ok := Config{StateDir: state}
	if err := ok.Resolve(); err != nil {
		t.Fatalf("defaults must resolve: %v", err)
	}
	if ok.Listen != defaultListenFor(runtime.GOOS) || ok.JobTimeoutValue != defaultJobTimeout {
		t.Fatalf("unexpected defaults %+v", ok)
	}
}

func TestLoadConfigIsStrict(t *testing.T) {
	state := t.TempDir()
	if _, err := LoadConfig(state, ""); err != nil {
		t.Fatalf("a missing default config must be fine: %v", err)
	}
	writeFile(t, filepath.Join(state, configFileName), "")
	if _, err := LoadConfig(state, ""); err != nil {
		t.Fatalf("an empty config must be fine: %v", err)
	}
	writeFile(t, filepath.Join(state, configFileName), "listen: 127.0.0.1:1\nunknown_field: true\n")
	if _, err := LoadConfig(state, ""); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
	if _, err := LoadConfig(state, filepath.Join(state, "missing.yaml")); err == nil {
		t.Fatal("an explicit missing config must be an error")
	}
}

func TestBuildChildEnvDropsHostEnvironment(t *testing.T) {
	host := map[string]string{
		"PATH": "/usr/bin", "Path": `C:\Windows`, "HOME": "/home/u", "SSH_AUTH_SOCK": "/tmp/agent.sock",
		"AWS_SECRET_ACCESS_KEY": "aws", "GITHUB_TOKEN": "gh", "LIBVIRT_UBUNTU_ANSIBLE_PASSWORD": "vmpass",
		envAppDataDir: "/home/u/real-data",
	}
	dirs := NewManagedDirs(Config{StateDir: "/state", DataDir: "/state/data"})
	env := BuildChildEnv(dirs, []string{"LIBVIRT_UBUNTU_ANSIBLE_PASSWORD"}, func(name string) string { return host[name] })
	joined := strings.Join(env, "\n")
	for _, forbidden := range []string{"SSH_AUTH_SOCK", "AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "/home/u/real-data"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("child env leaks %q:\n%s", forbidden, joined)
		}
	}
	for _, required := range []string{"LIBVIRT_UBUNTU_ANSIBLE_PASSWORD=vmpass", envAppDataDir + "=" + dirs.AppData, envConfigDir + "=" + dirs.Config} {
		if !strings.Contains(joined, required) {
			t.Fatalf("child env misses %q:\n%s", required, joined)
		}
	}
}

func TestPinBinaryIgnoresLaterSourceChanges(t *testing.T) {
	source := filepath.Join(t.TempDir(), "sailwright")
	writeFile(t, source, "original")
	state := t.TempDir()

	pinned, err := PinBinary(source, state, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PinBinary(source, state, strings.Repeat("0", 64)); err == nil {
		t.Fatal("expected a SHA mismatch to fail")
	}
	writeFile(t, source, "replaced by agent")
	if err := pinned.Verify(); err != nil {
		t.Fatalf("pinned copy must be unaffected by source changes: %v", err)
	}
	data, _ := os.ReadFile(pinned.Path)
	if string(data) != "original" {
		t.Fatalf("pinned copy content %q", data)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(pinned.Path, 0o700); err != nil {
			t.Fatal(err)
		}
	} else {
		_ = os.Chmod(pinned.Path, 0o600)
	}
	writeFile(t, pinned.Path, "tampered")
	if err := pinned.Verify(); err == nil {
		t.Fatal("expected tampering with the pinned copy to be detected")
	}
}

func TestTokenLifecycle(t *testing.T) {
	state := t.TempDir()
	token, err := LoadOrCreateToken(state)
	if err != nil || !tokenPattern.MatchString(token) {
		t.Fatalf("token %q, %v", token, err)
	}
	again, err := LoadOrCreateToken(state)
	if err != nil || again != token {
		t.Fatalf("token must be stable: %q vs %q (%v)", again, token, err)
	}
	rotated, err := RotateToken(state)
	if err != nil || rotated == token {
		t.Fatalf("rotation must change the token: %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(TokenPath(state), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadToken(state); err == nil {
			t.Fatal("a world-readable token file must be refused")
		}
	}
}

func TestAssessRisk(t *testing.T) {
	cases := []struct {
		args   []string
		bundle string
		want   []string
	}{
		{[]string{"install"}, "", []string{RiskHostModifying}},
		{[]string{"provision", "local"}, "", []string{RiskHostCode, RiskHostModifying}},
		{[]string{"provision", "local", "--check"}, "", []string{RiskHostCode}},
		{[]string{"provision", "ubuntu", "--", "-e", "x=1"}, "", []string{RiskHostCode, RiskPassthroughArgs}},
		{[]string{"provision", "ubuntu", "--playbook", "/w/a/site.yml"}, "/w/a", []string{RiskHostCode}},
		{[]string{"provision", "ubuntu", "--playbook", "/etc/site.yml"}, "/w/a", []string{RiskExternalPaths, RiskHostCode}},
		{[]string{"push", "ghcr.io/x/y:z", "--password", "p"}, "", []string{RiskCredentials, RiskNetworkWrite}},
		{[]string{"destroy", "all"}, "", []string{RiskAllTargets}},
		{[]string{"create", "ubuntu", "--arch", "all"}, "", nil},
	}
	for _, tc := range cases {
		got := AssessRisk(tc.args, tc.bundle)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("AssessRisk(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestRedactionAndTerminalSanitizing(t *testing.T) {
	redactor := NewRedactor("supersecret", "abc")
	var out strings.Builder
	writer := newLineRedactingWriter(redactor, &out)
	_, _ = writer.Write([]byte("password=super"))
	_, _ = writer.Write([]byte("secret \x1b[31mred\x1b[0m abc\n"))
	_, _ = writer.Write([]byte("tail supersecret"))
	_ = writer.Flush()
	if got := out.String(); got != "password=*** red abc\ntail ***" {
		t.Fatalf("unexpected redacted output %q", got)
	}

	hostile := "ok\x1b[2J\x1b]0;title\x07\r\nFAKE approve\u202eexe.txt"
	clean := sanitizeForTerminal(hostile)
	if strings.ContainsAny(clean, "\x1b\r\n\u202e") {
		t.Fatalf("control characters survived: %q", clean)
	}
	if got := quoteArgv([]string{"provision", "a b", "x\ny"}); got != `provision "a b" "x\ny"` {
		t.Fatalf("unexpected quoting %q", got)
	}
}

// syncBuffer is a goroutine-safe buffer for console output.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestConsoleApprover(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	out := &syncBuffer{}
	approver := NewConsoleApprover(bufio.NewReader(reader), out, time.Minute)

	decisions := make(chan Decision, 1)
	go func() {
		decisions <- approver.Request(context.Background(), ApprovalRequest{ID: "job1", Argv: []string{"install"}, AgentNote: "please\x1b[2J"}).Decision
	}()
	waitFor(t, func() bool { return strings.Contains(out.String(), `approve job1`) })
	if strings.Contains(out.String(), "\x1b") {
		t.Fatal("agent note escape sequences must be removed")
	}

	if got := approver.Request(context.Background(), ApprovalRequest{ID: "job2"}).Decision; got != DecisionBusy {
		t.Fatalf("a second pending request must be refused, got %s", got)
	}
	_, _ = io.WriteString(writer, "approve job2\n")
	_, _ = io.WriteString(writer, "approve job1\n")
	if got := <-decisions; got != DecisionApproved {
		t.Fatalf("decision %s", got)
	}

	go func() { decisions <- approver.Request(context.Background(), ApprovalRequest{ID: "job3"}).Decision }()
	waitFor(t, func() bool { return strings.Contains(out.String(), `approve job3`) })
	_, _ = io.WriteString(writer, "deny job3\n")
	if got := <-decisions; got != DecisionDenied {
		t.Fatalf("decision %s", got)
	}

	// A decision for an old id must not approve a new request.
	go func() { decisions <- approver.Request(context.Background(), ApprovalRequest{ID: "job4"}).Decision }()
	waitFor(t, func() bool { return strings.Contains(out.String(), `approve job4`) })
	_, _ = io.WriteString(writer, "approve job1\n")
	_, _ = io.WriteString(writer, "deny job4\n")
	if got := <-decisions; got != DecisionDenied {
		t.Fatalf("stale approval leaked: %s", got)
	}

	short := NewConsoleApprover(bufio.NewReader(strings.NewReader("")), out, 50*time.Millisecond)
	waitFor(t, func() bool { return !short.Enabled() })
	if got := short.Request(context.Background(), ApprovalRequest{ID: "x"}).Decision; got != DecisionDenied {
		t.Fatalf("a closed console must deny, got %s", got)
	}

	expiring := NewConsoleApprover(bufio.NewReader(&blockingReader{}), out, 50*time.Millisecond)
	if got := expiring.Request(context.Background(), ApprovalRequest{ID: "y"}).Decision; got != DecisionExpired {
		t.Fatalf("expected expiry, got %s", got)
	}
}

// blockingReader never returns data, like an idle terminal.
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) {
	select {}
}

func TestLineRedactingWriterCatchesSecretAcrossForcedFlush(t *testing.T) {
	const secret = "s3cr3t-value-123"
	for split := 1; split < len(secret); split++ {
		var out strings.Builder
		writer := newLineRedactingWriter(NewRedactor(secret), &out)
		// The first write ends inside the secret and exceeds the pending limit.
		first := strings.Repeat("a", maxPendingLineSize+1-split) + secret[:split]
		second := secret[split:] + strings.Repeat("b", 100)
		for _, chunk := range []string{first, second} {
			if _, err := writer.Write([]byte(chunk)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Flush(); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), secret[:6]) || strings.Contains(out.String(), secret[len(secret)-6:]) {
			t.Fatalf("split %d: secret leaked", split)
		}
		if want := len(first) + len(second) - len(secret) + len(redactedMarker); out.Len() != want {
			t.Fatalf("split %d: output length %d, want %d", split, out.Len(), want)
		}
	}
}

func TestLineRedactingWriterCatchesCompleteSecretStraddlingForcedFlush(t *testing.T) {
	const secret = "s3cr3t-value-123"
	for trailing := 0; trailing < len(secret); trailing++ {
		var out strings.Builder
		writer := newLineRedactingWriter(NewRedactor(secret), &out)
		chunk := strings.Repeat("a", maxPendingLineSize+1) + secret + strings.Repeat("b", trailing)
		for _, part := range []string{chunk, "zzz\n"} {
			if _, err := writer.Write([]byte(part)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Flush(); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), secret[:6]) || strings.Contains(out.String(), secret[len(secret)-6:]) {
			t.Fatalf("trailing %d: secret leaked", trailing)
		}
	}
}

func TestRedactorWithRequiredHidesShortSecrets(t *testing.T) {
	base := NewRedactor("longenough")
	if got := base.With("pw").String("pw longenough"); got != "pw ***" {
		t.Fatalf("With must ignore short values, got %q", got)
	}
	if got := base.WithRequired("pw").String("my pw is pw, longenough"); strings.Contains(got, "pw is pw") || strings.Contains(got, "longenough") {
		t.Fatalf("WithRequired must hide short values and keep earlier secrets, got %q", got)
	}
	if got := base.WithRequired("").String("unchanged"); got != "unchanged" {
		t.Fatalf("empty secrets must be ignored, got %q", got)
	}
}

func TestRedactorWithKeepsRequiredShortSecrets(t *testing.T) {
	r := NewRedactor("longenough").WithRequired("pw").With("xy")
	if got := r.String("pw xy longenough"); strings.Contains(got, "pw") || strings.Contains(got, "longenough") {
		t.Fatalf("With must keep the minimum length of the redactor it extends, got %q", got)
	}
}
