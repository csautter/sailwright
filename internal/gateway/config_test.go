package gateway

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestResolveLiveViewAndStuckHint(t *testing.T) {
	resolve := func(view string, stuck string) (Config, error) {
		cfg := Config{StateDir: t.TempDir(), AgentDir: t.TempDir(), ViewListen: view, ScreenStuckAfter: stuck}
		return cfg, cfg.Resolve()
	}

	cfg, err := resolve("", "")
	if err != nil || cfg.ViewAddr.String() != defaultViewListen || cfg.ScreenStuckAfterValue != defaultScreenStuckAfter {
		t.Fatalf("defaults: %v %s %s", err, cfg.ViewAddr, cfg.ScreenStuckAfterValue)
	}
	cfg, err = resolve("off", "off")
	if err != nil || cfg.ViewAddr.IsValid() || cfg.ScreenStuckAfterValue != 0 {
		t.Fatalf("off: %v %s %s", err, cfg.ViewAddr, cfg.ScreenStuckAfterValue)
	}
	cfg, err = resolve("[::1]:8080", "30m")
	if err != nil || cfg.ViewAddr.String() != "[::1]:8080" || cfg.ScreenStuckAfterValue.Minutes() != 30 {
		t.Fatalf("custom: %v %s %s", err, cfg.ViewAddr, cfg.ScreenStuckAfterValue)
	}
	for _, view := range []string{"0.0.0.0:8080", "192.168.1.10:8080", "localhost:8080", "127.0.0.1"} {
		if _, err := resolve(view, ""); err == nil {
			t.Errorf("view_listen %q should be rejected", view)
		}
	}
	for _, stuck := range []string{"0", "-5m", "soon"} {
		if _, err := resolve("", stuck); err == nil {
			t.Errorf("screen_stuck_after %q should be rejected", stuck)
		}
	}
}

func TestResolveListenOff(t *testing.T) {
	cfg := Config{StateDir: t.TempDir(), AgentDir: t.TempDir(), Listen: disabledSetting}
	err := cfg.Resolve()
	if runtime.GOOS == "windows" {
		if err == nil {
			t.Fatal("listen: off must be rejected without an agent socket")
		}
		return
	}
	if err != nil || cfg.ListenAddr.IsValid() {
		t.Fatalf("listen off: %v %s", err, cfg.ListenAddr)
	}
	if defaultListenFor("linux") != disabledSetting || defaultListenFor("darwin") != defaultListen || defaultListenFor("windows") != defaultListen {
		t.Fatal("unexpected per-OS listen defaults")
	}
}
