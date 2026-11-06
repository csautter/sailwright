package build

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveDefaultAppDataDirForOS_UsesOverride(t *testing.T) {
	got, err := resolveDefaultAppDataDirForOS(
		"linux",
		func(key string) string {
			if key == devAlchemyAppDataEnvVar {
				return "/tmp/dev-alchemy-custom"
			}
			return ""
		},
		func() (string, error) { return "/home/tester", nil },
		func() (string, error) { return "/config", nil },
	)
	if err != nil {
		t.Fatalf("resolveDefaultAppDataDirForOS returned error: %v", err)
	}
	if got != filepath.Clean("/tmp/dev-alchemy-custom") {
		t.Fatalf("expected override path, got %q", got)
	}
}

func TestResolveDefaultAppDataDirForOS_Darwin(t *testing.T) {
	got, err := resolveDefaultAppDataDirForOS(
		"darwin",
		func(string) string { return "" },
		func() (string, error) { return "/Users/tester", nil },
		func() (string, error) { return "", nil },
	)
	if err != nil {
		t.Fatalf("resolveDefaultAppDataDirForOS returned error: %v", err)
	}
	want := filepath.Join("/Users/tester", "Library", "Application Support", devAlchemyAppName)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveDefaultAppDataDirForOS_WindowsPrefersLocalAppData(t *testing.T) {
	got, err := resolveDefaultAppDataDirForOS(
		"windows",
		func(key string) string {
			switch key {
			case "LOCALAPPDATA":
				return `C:\Users\tester\AppData\Local`
			case "APPDATA":
				return `C:\Users\tester\AppData\Roaming`
			default:
				return ""
			}
		},
		func() (string, error) { return "", nil },
		func() (string, error) { return `C:\Users\tester\AppData\Roaming`, nil },
	)
	if err != nil {
		t.Fatalf("resolveDefaultAppDataDirForOS returned error: %v", err)
	}
	want := filepath.Join(`C:\Users\tester\AppData\Local`, devAlchemyAppName)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveDefaultAppDataDirForOS_LinuxUsesXDGDataHome(t *testing.T) {
	got, err := resolveDefaultAppDataDirForOS(
		"linux",
		func(key string) string {
			if key == "XDG_DATA_HOME" {
				return "/home/tester/.local/share"
			}
			return ""
		},
		func() (string, error) { return "/home/tester", nil },
		func() (string, error) { return "", nil },
	)
	if err != nil {
		t.Fatalf("resolveDefaultAppDataDirForOS returned error: %v", err)
	}
	want := filepath.Join("/home/tester/.local/share", devAlchemyAppName)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveDefaultAppDataDirForOS_LinuxFallsBackToHome(t *testing.T) {
	got, err := resolveDefaultAppDataDirForOS(
		"linux",
		func(string) string { return "" },
		func() (string, error) { return "/home/tester", nil },
		func() (string, error) { return "", nil },
	)
	if err != nil {
		t.Fatalf("resolveDefaultAppDataDirForOS returned error: %v", err)
	}
	want := filepath.Join("/home/tester", ".local", "share", devAlchemyAppName)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveDefaultAppDataDirForOS_ReturnsErrorWhenHomeUnavailable(t *testing.T) {
	_, err := resolveDefaultAppDataDirForOS(
		"darwin",
		func(string) string { return "" },
		func() (string, error) { return "", errors.New("boom") },
		func() (string, error) { return "", nil },
	)
	if err == nil {
		t.Fatal("expected error when home directory lookup fails")
	}
}

func TestResolveDefaultConfigDirForOS_UsesOverride(t *testing.T) {
	got, err := resolveDefaultConfigDirForOS(
		"linux",
		func(key string) string {
			if key == devAlchemyConfigEnvVar {
				return "/tmp/dev-alchemy-config"
			}
			return ""
		},
		func() (string, error) { return "/home/tester", nil },
		func() (string, error) { return "/config", nil },
	)
	if err != nil {
		t.Fatalf("resolveDefaultConfigDirForOS returned error: %v", err)
	}
	if got != filepath.Clean("/tmp/dev-alchemy-config") {
		t.Fatalf("expected override path, got %q", got)
	}
}

func TestResolveDefaultConfigDirForOS_LinuxUsesXDGConfigHome(t *testing.T) {
	got, err := resolveDefaultConfigDirForOS(
		"linux",
		func(key string) string {
			if key == "XDG_CONFIG_HOME" {
				return "/home/tester/.config"
			}
			return ""
		},
		func() (string, error) { return "/home/tester", nil },
		func() (string, error) { return "", nil },
	)
	if err != nil {
		t.Fatalf("resolveDefaultConfigDirForOS returned error: %v", err)
	}
	want := filepath.Join("/home/tester/.config", devAlchemyAppName)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveDefaultConfigDirForOS_WindowsPrefersRoamingAppData(t *testing.T) {
	got, err := resolveDefaultConfigDirForOS(
		"windows",
		func(key string) string {
			switch key {
			case "LOCALAPPDATA":
				return `C:\Users\tester\AppData\Local`
			case "APPDATA":
				return `C:\Users\tester\AppData\Roaming`
			default:
				return ""
			}
		},
		func() (string, error) { return "", nil },
		func() (string, error) { return `C:\Users\tester\AppData\Roaming`, nil },
	)
	if err != nil {
		t.Fatalf("resolveDefaultConfigDirForOS returned error: %v", err)
	}
	want := filepath.Join(`C:\Users\tester\AppData\Roaming`, devAlchemyAppName)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEnsureDirectoriesExist_CreatesPrivateDirectories(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "app", "cache")

	if err := ensureDirectoriesExist("", target); err != nil {
		t.Fatalf("ensureDirectoriesExist returned error: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("expected directory to exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %q to be a directory", target)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if got := info.Mode().Perm(); got != managedDirPermission {
		t.Fatalf("expected permissions %o, got %o", managedDirPermission, got)
	}
}

func TestManagedDirectoryOverrides(t *testing.T) {
	for _, name := range []string{devAlchemyCacheEnvVar, devAlchemyVagrantEnvVar, devAlchemyPackerCacheEnvVar} {
		t.Run(name, func(t *testing.T) {
			want := t.TempDir()
			t.Setenv(name, want)
			if got := directoryOverride(name, "fallback"); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestGetDirectoriesHonorsManagedOverrides(t *testing.T) {
	app, config, project := t.TempDir(), t.TempDir(), t.TempDir()
	cache, vagrant, packer := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv(projectDirEnvVar, project)
	t.Setenv(devAlchemyCacheEnvVar, cache)
	t.Setenv(devAlchemyVagrantEnvVar, vagrant)
	t.Setenv(devAlchemyPackerCacheEnvVar, packer)
	dirs := &Directories{WorkingDir: t.TempDir(), AppDataDir: app, ConfigDir: config}
	dirs.GetDirectories()
	if dirs.CacheDir != cache || dirs.VagrantDir != vagrant || dirs.PackerCacheDir != packer {
		t.Fatalf("managed overrides ignored: %+v", dirs)
	}
}

func TestTargetTypeFollowsVMConfigurations(t *testing.T) {
	typed := map[string]bool{}
	for _, config := range AvailableVirtualMachineConfigs() {
		if config.UbuntuType != "" {
			typed[config.OS] = true
		}
	}
	if !typed["ubuntu"] {
		t.Fatal("expected ubuntu to have typed targets")
	}
	for _, config := range AvailableVirtualMachineConfigs() {
		want := ""
		if typed[config.OS] {
			want = "desktop"
		}
		if got := TargetType(config.OS, "desktop"); got != want {
			t.Errorf("TargetType(%s) = %q, want %q", config.OS, got, want)
		}
	}
	for _, osName := range []string{"all", "local"} {
		if got := TargetType(osName, "server"); got != "" {
			t.Errorf("TargetType(%s) = %q", osName, got)
		}
	}
}
