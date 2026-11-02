package cmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/csautter/dev-alchemy/internal/selfupdate"
)

func latestServer(t *testing.T, tag string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases/latest" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// testUpdateEnv builds a standalone installation in a temp directory whose
// replace function fails the test if it is ever called.
func testUpdateEnv(t *testing.T, latest string) (updateEnv, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "sailwright")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	return updateEnv{
		in: strings.NewReader(""), out: &out, errOut: &errOut,
		executable: exe, version: "v1.0.0", goos: runtime.GOOS, goarch: runtime.GOARCH,
		settingsFile: filepath.Join(dir, "config", "update.json"),
		getenv:       func(string) string { return "" },
		releases:     selfupdate.Releases{Version: "v1.0.0", BaseURL: latestServer(t, latest)},
		replace: func([]selfupdate.Replacement) error {
			t.Fatal("replace must not be called")
			return nil
		},
	}, &out, &errOut
}

func TestRunUpdateCheckExitCodes(t *testing.T) {
	env, out, _ := testUpdateEnv(t, "v1.0.1")
	err := runUpdate(context.Background(), updateOptions{check: true}, env)
	if !errors.As(err, new(updateAvailableError)) || !strings.Contains(out.String(), "v1.0.0 → v1.0.1") {
		t.Fatalf("update available: err=%v out=%q", err, out)
	}
	for _, latest := range []string{"v1.0.0", "v0.9.0"} {
		env, out, _ := testUpdateEnv(t, latest)
		if err := runUpdate(context.Background(), updateOptions{check: true}, env); err != nil || !strings.Contains(out.String(), "up to date") {
			t.Fatalf("latest %s: err=%v out=%q", latest, err, out)
		}
	}
}

func TestRunUpdateRefusals(t *testing.T) {
	env, _, _ := testUpdateEnv(t, "v1.0.1")
	cases := []struct {
		name string
		o    updateOptions
		mod  func(*updateEnv)
		want string
	}{
		{"development", updateOptions{}, func(e *updateEnv) { e.version = "dev" }, "development builds"},
		{"homebrew", updateOptions{}, func(e *updateEnv) { e.executable = "/opt/homebrew/Cellar/sailwright/1.0.0/bin/sailwright" }, "brew upgrade"},
		{"package manager", updateOptions{}, func(e *updateEnv) { e.executable = `/home/u/scoop/apps/sailwright/current/sailwright` }, "package manager"},
		{"invalid target", updateOptions{target: "latest"}, nil, "stable release"},
		{"non-interactive without yes", updateOptions{}, nil, "pass --yes"},
		{"downgrade without terminal", updateOptions{yes: true, target: "v0.9.0"}, nil, "downgrades require an interactive terminal"},
		{"remote source", updateOptions{yes: true}, func(e *updateEnv) { e.releases.BaseURL = "https://example.com" }, "loopback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := env
			if tc.mod != nil {
				tc.mod(&e)
			}
			err := runUpdate(context.Background(), tc.o, e)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunUpdateInvalidSettingsIncludesRepairHint(t *testing.T) {
	env, _, _ := testUpdateEnv(t, "v1.0.1")
	if err := os.MkdirAll(filepath.Dir(env.settingsFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.settingsFile, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runUpdate(context.Background(), updateOptions{check: true}, env)
	if err == nil || !strings.Contains(err.Error(), settingsRepairHint) {
		t.Fatalf("err = %v", err)
	}
}

func TestConfirmUpdate(t *testing.T) {
	var errOut bytes.Buffer
	env := updateEnv{errOut: &errOut, interactive: true}
	cases := []struct {
		reply          string
		yes, downgrade bool
		ok             bool
	}{
		{"", true, false, true},
		{"y\n", false, false, true},
		{"YES\n", false, false, true},
		{"y", false, false, true},
		{"n\n", false, false, false},
		{"\n", false, false, false},
		{"y\n", true, true, true},
		{"n\n", true, true, false},
	}
	for _, tc := range cases {
		env.in = strings.NewReader(tc.reply)
		if err := confirmUpdate(env, tc.yes, tc.downgrade); (err == nil) != tc.ok {
			t.Errorf("reply %q yes=%t downgrade=%t: err=%v", tc.reply, tc.yes, tc.downgrade, err)
		}
	}
}

func TestRunUpdateSettings(t *testing.T) {
	env, out, errOut := testUpdateEnv(t, "v1.0.1")
	if err := runUpdateSettings(updateOptions{autoChanged: true, auto: "on", yes: true}, env); err == nil {
		t.Fatal("settings combined with release options")
	}
	if err := runUpdateSettings(updateOptions{autoChanged: true, auto: "maybe"}, env); err == nil {
		t.Fatal("invalid --auto-check accepted")
	}
	if err := os.MkdirAll(filepath.Dir(env.settingsFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.settingsFile, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runUpdateSettings(updateOptions{autoChanged: true, auto: "on"}, env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "replacing invalid update settings") || !strings.Contains(out.String(), "true (interval 1h0m0s)") {
		t.Fatalf("repair output: out=%q err=%q", out, errOut)
	}
}

func TestUpdateTargets(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "sailwright")
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	names, _, err := updateTargets(exe, "linux", false, true)
	if err != nil || len(names) != 1 {
		t.Fatalf("without gateway: %v %v", names, err)
	}
	gateway := filepath.Join(dir, "sailwright-gateway")
	if err := os.WriteFile(gateway, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	names, paths, err := updateTargets(exe, "linux", false, true)
	if err != nil || len(names) != 2 || paths[1] != gateway {
		t.Fatalf("with gateway: %v %v %v", names, paths, err)
	}
	if names, _, err := updateTargets(exe, "linux", true, true); err != nil || len(names) != 1 {
		t.Fatalf("--no-gateway: %v %v", names, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Remove(gateway); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(exe, gateway); err != nil {
			t.Fatal(err)
		}
		if _, _, err := updateTargets(exe, "linux", false, false); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("symlinked gateway: %v", err)
		}
	}
}

func TestUpdateCheckApplies(t *testing.T) {
	find := func(args ...string) bool {
		t.Helper()
		command, _, err := rootCmd.Find(args)
		if err != nil {
			t.Fatal(err)
		}
		return updateCheckApplies(command, args)
	}
	if find("--version") || find() {
		t.Error("root command must not check")
	}
	if find("update", "--help") || find("version", "-h") {
		t.Error("help must not check")
	}
	if !find("version") {
		t.Error("runnable subcommand rejected (eligibility is decided separately)")
	}
	if !find("build", "-v", "ubuntu") {
		t.Error("build -v (verbose) must still check")
	}
}
