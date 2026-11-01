package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Names of the managed directory variables understood by the sailwright CLI
// (see pkg/build/directories.go ManagedEnv).
const (
	cliAppName        = "dev-alchemy"
	envAppDataDir     = "DEV_ALCHEMY_APP_DATA_DIR"
	envConfigDir      = "DEV_ALCHEMY_CONFIG_DIR"
	envCacheDir       = "DEV_ALCHEMY_CACHE_DIR"
	envVagrantDir     = "DEV_ALCHEMY_VAGRANT_DIR"
	envPackerCacheDir = "DEV_ALCHEMY_PACKER_CACHE_DIR"
	envPackerCache    = "PACKER_CACHE_DIR"
	envLockTimeout    = "SAILWRIGHT_LOCK_TIMEOUT"
	childLockTimeout  = "30s"
)

// baseEnvNames are the host variables a sailwright child process needs to find its
// tools. Anything else, notably SSH_AUTH_SOCK and cloud credentials, is dropped.
// On Windows, the Hyper-V cmdlets that Packer calls fail with "Value cannot be null.
// Parameter name: name" when COMPUTERNAME is missing.
var baseEnvNames = map[string][]string{
	"unix": {"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "LC_CTYPE", "TMPDIR", "TZ", "XDG_RUNTIME_DIR", "LIBVIRT_DEFAULT_URI"},
	"windows": {
		"Path", "PATHEXT", "SystemRoot", "SystemDrive", "windir", "ComSpec", "TEMP", "TMP",
		"USERPROFILE", "USERNAME", "USERDOMAIN", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA",
		"ProgramData", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "CommonProgramFiles",
		"CommonProgramFiles(x86)", "CommonProgramW6432", "PSModulePath", "NUMBER_OF_PROCESSORS",
		"PROCESSOR_ARCHITECTURE", "PROCESSOR_IDENTIFIER", "PROCESSOR_LEVEL", "PROCESSOR_REVISION", "OS",
		"COMPUTERNAME", "ALLUSERSPROFILE", "PUBLIC",
	},
}

// ManagedDirs are the gateway-owned sailwright directories, isolated from the user's
// regular sailwright data.
type ManagedDirs struct {
	AppData     string
	Config      string
	Cache       string
	Vagrant     string
	PackerCache string
	Work        string
	Staging     string
	Logs        string
	// Screens keeps sampled build console frames per job; jobs do not survive a restart.
	Screens      string
	EmbeddedData string
	Shared       bool
}

// NewManagedDirs derives the managed directories from the config.
func NewManagedDirs(cfg Config) ManagedDirs {
	dirs := ManagedDirs{
		AppData:      filepath.Join(cfg.DataDir, "app"),
		Config:       filepath.Join(cfg.DataDir, "config"),
		Work:         filepath.Join(cfg.StateDir, "work"),
		Staging:      filepath.Join(cfg.StateDir, "staging"),
		Logs:         filepath.Join(cfg.StateDir, "logs"),
		Screens:      filepath.Join(cfg.StateDir, "screens"),
		EmbeddedData: filepath.Join(cfg.DataDir, "embedded"),
		Shared:       cfg.StateIsolation == "shared",
	}
	dirs.Cache = filepath.Join(dirs.AppData, "cache")
	if cfg.SharedCacheDir != "" {
		dirs.Cache = cfg.SharedCacheDir
	}
	dirs.Vagrant = filepath.Join(dirs.AppData, ".vagrant")
	dirs.PackerCache = filepath.Join(dirs.AppData, "packer_cache")
	if cfg.StateIsolation == "shared" {
		dirs.AppData, dirs.Cache = cfg.SharedAppData, cfg.SharedCache
		dirs.Vagrant, dirs.PackerCache = cfg.SharedVagrant, cfg.SharedPackerCache
		if cfg.SharedCacheDir != "" {
			dirs.Cache = cfg.SharedCacheDir
		}
	}
	return dirs
}

// Ensure creates all managed directories with owner-only permissions.
func (d ManagedDirs) Ensure() error {
	for _, dir := range []string{d.AppData, d.Config, d.Cache, d.Vagrant, d.PackerCache, d.Work, d.Staging, d.Logs, d.Screens, d.EmbeddedData} {
		if d.Shared && (dir == d.AppData || dir == d.Cache || dir == d.Vagrant || dir == d.PackerCache) {
			if err := os.MkdirAll(dir, stateDirPermission); err != nil {
				return err
			}
			continue
		}
		if err := ensurePrivateDir(dir); err != nil {
			return err
		}
	}
	return nil
}

// PlaybookDir is where the pinned binary extracts its embedded playbooks.
func (d ManagedDirs) PlaybookDir() string {
	return filepath.Join(d.EmbeddedData, "project", "playbooks")
}

// BuildChildEnv returns the complete environment for a sailwright child process. It
// is built from scratch; getenv is usually os.Getenv.
func BuildChildEnv(dirs ManagedDirs, passthrough []string, getenv func(string) string) []string {
	values := map[string]string{}
	family := "unix"
	if runtime.GOOS == "windows" {
		family = "windows"
	}
	for _, name := range baseEnvNames[family] {
		if value := getenv(name); value != "" {
			values[name] = value
		}
	}
	for _, name := range passthrough {
		if value := getenv(name); value != "" {
			values[name] = value
		}
	}
	values["SAILWRIGHT_PROJECT_DIR"] = ""
	values["SAILWRIGHT_EMBEDDED_DATA_DIR"] = dirs.EmbeddedData
	// Jobs wait briefly for a target the operator's CLI is using instead of failing.
	values[envLockTimeout] = childLockTimeout
	values[envAppDataDir] = dirs.AppData
	values[envConfigDir] = dirs.Config
	values[envCacheDir] = dirs.Cache
	values[envVagrantDir] = dirs.Vagrant
	values[envPackerCacheDir] = dirs.PackerCache
	values[envPackerCache] = dirs.PackerCache

	env := make([]string, 0, len(values))
	for name, value := range values {
		env = append(env, name+"="+value)
	}
	sort.Strings(env)
	return env
}

// PassthroughSecrets returns the values of passthrough variables; they are redacted
// from all output.
func PassthroughSecrets(passthrough []string, getenv func(string) string) []string {
	var secrets []string
	for _, name := range passthrough {
		if value := strings.TrimSpace(getenv(name)); value != "" {
			secrets = append(secrets, value)
		}
	}
	return secrets
}

// findGitRoot returns the first ancestor of dir that contains .git. The CLI treats
// such a directory as its project and would read playbooks from it, so the gateway
// work dir must not be inside a git checkout.
func findGitRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// resolveSharedDirs mirrors the CLI's directory contract without importing it;
// TestSharedAppDataMirrorsCLIDefaults keeps both in sync.
func (c *Config) resolveSharedDirs(getenv func(string) string, goos string) error {
	app, err := sharedAppDataDir(goos, getenv, os.UserHomeDir, os.UserConfigDir)
	if err != nil {
		return err
	}
	c.SharedAppData, err = filepath.Abs(app)
	if err != nil {
		return fmt.Errorf("resolve shared app data: %w", err)
	}
	for _, entry := range []struct {
		name, fallback string
		destination    *string
	}{
		{envCacheDir, "cache", &c.SharedCache},
		{envVagrantDir, ".vagrant", &c.SharedVagrant},
		{envPackerCacheDir, "packer_cache", &c.SharedPackerCache},
	} {
		value := getenv(entry.name)
		if value == "" {
			value = filepath.Join(c.SharedAppData, entry.fallback)
		}
		*entry.destination, err = filepath.Abs(value)
		if err != nil {
			return err
		}
	}
	return nil
}

// sharedAppDataDir returns the CLI's default app-data directory for goos.
func sharedAppDataDir(goos string, getenv func(string) string, home, configDir func() (string, error)) (string, error) {
	if app := getenv(envAppDataDir); app != "" {
		return filepath.Clean(app), nil
	}
	switch goos {
	case "darwin":
		dir, err := home()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "Library", "Application Support", cliAppName), nil
	case "windows":
		for _, name := range []string{"LOCALAPPDATA", "APPDATA"} {
			if base := getenv(name); base != "" {
				return filepath.Join(base, cliAppName), nil
			}
		}
		base, err := configDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(base, cliAppName), nil
	default:
		if base := getenv("XDG_DATA_HOME"); base != "" {
			return filepath.Join(base, cliAppName), nil
		}
		dir, err := home()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, ".local", "share", cliAppName), nil
	}
}
