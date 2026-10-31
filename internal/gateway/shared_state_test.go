package gateway

import (
	"os"
	"path/filepath"
	"testing"
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
