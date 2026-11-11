package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

func TestSharedDirectoriesKeepConfigAndAssetsPrivate(t *testing.T) {
	app, cache := t.TempDir(), t.TempDir()
	t.Setenv(envAppDataDir, app)
	t.Setenv(envCacheDir, cache)
	t.Setenv(envConfigDir, t.TempDir())
	cfg := Config{StateDir: t.TempDir(), AgentDir: t.TempDir()}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	dirs := NewManagedDirs(cfg)
	if dirs.AppData != app || dirs.Cache != cache {
		t.Fatalf("CLI directories not shared: %+v", dirs)
	}
	if dirs.Config != filepath.Join(cfg.DataDir, "config") || isWithin(app, dirs.EmbeddedData) {
		t.Fatal("gateway private directories were shared")
	}
	sentinel := filepath.Join(cache, "keep")
	if err := os.WriteFile(sentinel, []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ClearCache(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal(err)
	}
}

func TestStateIsolationValidation(t *testing.T) {
	cfg := Config{StateDir: t.TempDir(), StateIsolation: "typo"}
	if err := cfg.Resolve(); err == nil {
		t.Fatal("invalid isolation accepted")
	}
	cfg.StateIsolation = "isolated"
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	if got := NewManagedDirs(cfg).AppData; got != filepath.Join(cfg.DataDir, "app") {
		t.Fatal(got)
	}
}

func TestSharedAppDataMirrorsCLIDefaults(t *testing.T) {
	home := func() (string, error) { return filepath.Join("/home", "op"), nil }
	config := func() (string, error) { return filepath.Join("/cfg"), nil }
	noConfig := func() (string, error) { return "", errors.New("no config dir") }
	for _, tc := range []struct {
		goos   string
		env    map[string]string
		config func() (string, error)
	}{
		{"linux", nil, config},
		{"linux", map[string]string{"XDG_DATA_HOME": "/xdg"}, config},
		{"linux", map[string]string{envAppDataDir: "/explicit/app/"}, config},
		{"darwin", nil, config},
		{"windows", map[string]string{"LOCALAPPDATA": `C:\Users\op\AppData\Local`}, config},
		{"windows", map[string]string{"APPDATA": `C:\Users\op\AppData\Roaming`}, config},
		{"windows", nil, config},
		{"windows", nil, noConfig},
	} {
		getenv := func(name string) string { return tc.env[name] }
		got, gotErr := sharedAppDataDir(tc.goos, getenv, home, tc.config)
		want, wantErr := alchemy_build.DefaultAppDataDirForOS(tc.goos, getenv, home, tc.config)
		if got != want || (gotErr == nil) != (wantErr == nil) {
			t.Errorf("%s %v: gateway %q (%v), CLI %q (%v)", tc.goos, tc.env, got, gotErr, want, wantErr)
		}
	}
}

func TestUnsetIsolationKeepsLegacyGatewayState(t *testing.T) {
	t.Setenv(envAppDataDir, t.TempDir())
	cfg := Config{StateDir: t.TempDir(), AgentDir: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(cfg.StateDir, "data", "app", ".vagrant"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Resolve(); err != nil {
		t.Fatal(err)
	}
	if cfg.StateIsolation != "isolated" || !cfg.LegacyIsolation {
		t.Fatalf("legacy state not preserved: %q legacy=%v", cfg.StateIsolation, cfg.LegacyIsolation)
	}
	if got := NewManagedDirs(cfg).AppData; got != filepath.Join(cfg.DataDir, "app") {
		t.Fatal(got)
	}

	explicit := Config{StateDir: cfg.StateDir, AgentDir: cfg.AgentDir, StateIsolation: "shared"}
	if err := explicit.Resolve(); err != nil {
		t.Fatal(err)
	}
	if explicit.StateIsolation != "shared" || explicit.LegacyIsolation {
		t.Fatal("explicit shared setting overridden")
	}
}

// Hypervisor VM names are global, so an isolated gateway still sees (and its vm_info
// would hand out credentials for) the operator's VMs on Hyper-V, UTM and Tart. VMs
// it did not create must stay gated in both modes, including legacy isolation.
func TestIsolatedStateGatesForeignVMs(t *testing.T) {
	t.Setenv(envAppDataDir, t.TempDir())
	explicit := Config{StateDir: t.TempDir(), AgentDir: t.TempDir(), StateIsolation: "isolated"}
	if err := explicit.Resolve(); err != nil {
		t.Fatal(err)
	}
	if !explicit.GateForeignVMs {
		t.Fatal("state_isolation: isolated does not gate VMs the gateway did not create")
	}
	legacy := Config{StateDir: t.TempDir(), AgentDir: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(legacy.StateDir, "data", "app"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Resolve(); err != nil {
		t.Fatal(err)
	}
	if !legacy.LegacyIsolation || !legacy.GateForeignVMs {
		t.Fatalf("legacy isolation does not gate foreign VMs: legacy=%v gate=%v", legacy.LegacyIsolation, legacy.GateForeignVMs)
	}
}
