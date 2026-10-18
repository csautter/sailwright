package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDataDirForOS(t *testing.T) {
	home := func() (string, error) { return "/home/u", nil }
	tests := []struct {
		name string
		goos string
		env  map[string]string
		want string
	}{
		{name: "linux default", goos: "linux", want: filepath.Join("/home/u", ".local", "share", "sailwright-gateway")},
		{name: "linux xdg", goos: "linux", env: map[string]string{"XDG_DATA_HOME": "/data"}, want: filepath.Join("/data", "sailwright-gateway")},
		{name: "windows local app data", goos: "windows", env: map[string]string{"LOCALAPPDATA": `C:\Users\u\AppData\Local`}, want: filepath.Join(`C:\Users\u\AppData\Local`, "sailwright-gateway")},
		{name: "windows without local app data", goos: "windows"},
		{name: "darwin uses state dir", goos: "darwin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := defaultDataDirForOS(tt.goos, func(name string) string { return tt.env[name] }, home)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLegacyDataDir(t *testing.T) {
	state := t.TempDir()
	cfg := Config{StateDir: state, DataDir: filepath.Join(t.TempDir(), "data")}
	if got := cfg.LegacyDataDir(); got != "" {
		t.Fatalf("no legacy dir yet, got %q", got)
	}
	legacy := filepath.Join(state, "data")
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := cfg.LegacyDataDir(); got != legacy {
		t.Fatalf("got %q, want %q", got, legacy)
	}
	cfg.DataDir = legacy
	if got := cfg.LegacyDataDir(); got != "" {
		t.Fatalf("legacy dir in use, got %q", got)
	}
}
