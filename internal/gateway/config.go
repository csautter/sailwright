// Package gateway implements sailwright-gateway, a host-side policy enforcement point
// that lets an agent in a devcontainer drive a pinned sailwright binary over MCP.
//
// The gateway only knows how commands may run (authentication, classification into
// the safe set or host approval, snapshots, execution, audit). It knows nothing about
// tests; deterministic scenarios live in test/e2e and use the gateway as a client.
package gateway

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultListen         = "127.0.0.1:7391"
	defaultJobTimeout     = 6 * time.Hour
	defaultInfoTimeout    = 2 * time.Minute
	defaultApprovalTTL    = 10 * time.Minute
	defaultMinFreeDiskGB  = 20
	defaultOutputLimit    = 8 << 20
	defaultSnapshotBytes  = 50 << 20
	defaultSnapshotFiles  = 5000
	configFileName        = "gateway.yaml"
	tokenFileName         = "token"
	stateDirPermission    = 0o700
	privateFilePermission = 0o600
	defaultStateDirName   = "sailwright-gateway"
	defaultAgentDirName   = ".sailwright-gateway"
	// AgentSocketName and AgentTokenName are the files in the agent dir. The agent dir
	// is meant to be bind-mounted read-only into devcontainers.
	AgentSocketName        = "gateway.sock"
	AgentTokenName         = "token"
	maxConfigFileSizeBytes = 1 << 20
)

// WorkspaceMapping maps a directory the agent sees (inside its container) to the
// host directory that backs it, usually a devcontainer bind mount. Every mapped host
// directory is treated as agent-writable.
type WorkspaceMapping struct {
	Agent string `yaml:"agent"`
	Host  string `yaml:"host"`
}

// Config is the operator-owned gateway configuration. It is never writable through MCP.
type Config struct {
	Listen             string             `yaml:"listen"`
	AllowedHosts       []string           `yaml:"allowed_hosts"`
	AllowedSourceCIDRs []string           `yaml:"allowed_source_cidrs"`
	AllowedOrigins     []string           `yaml:"allowed_origins"`
	Binary             string             `yaml:"binary"`
	BinarySHA256       string             `yaml:"binary_sha256"`
	DataDir            string             `yaml:"data_dir"`
	SharedCacheDir     string             `yaml:"shared_cache_dir"`
	EnvPassthrough     []string           `yaml:"env_passthrough"`
	PullAllowlist      []string           `yaml:"pull_allowlist"`
	AgentWorkspaces    []WorkspaceMapping `yaml:"agent_workspaces"`
	JobTimeout         string             `yaml:"job_timeout"`
	ApprovalTTL        string             `yaml:"approval_ttl"`
	MinFreeDiskGB      int                `yaml:"min_free_disk_gb"`
	OutputLimitBytes   int                `yaml:"output_limit_bytes"`
	SnapshotMaxBytes   int64              `yaml:"snapshot_max_bytes"`
	SnapshotMaxFiles   int                `yaml:"snapshot_max_files"`
	AgentDir           string             `yaml:"agent_dir"`

	// Resolved values, filled by Resolve.
	StateDir         string         `yaml:"-"`
	ListenAddr       netip.AddrPort `yaml:"-"`
	SourcePrefixes   []netip.Prefix `yaml:"-"`
	JobTimeoutValue  time.Duration  `yaml:"-"`
	ApprovalTTLValue time.Duration  `yaml:"-"`
}

// DefaultStateDir returns the per-user gateway state directory.
func DefaultStateDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(base, defaultStateDirName), nil
}

// DefaultDataDir returns the data dir used with the default state dir. The data dir
// holds ISOs, images and build caches, so like the sailwright CLI it follows the
// platform's data location instead of the config location. An empty result means
// <state dir>/data.
func DefaultDataDir() (string, error) {
	return defaultDataDirForOS(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func defaultDataDirForOS(goos string, getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	switch goos {
	case "darwin":
		// The state dir already is in ~/Library/Application Support.
		return "", nil
	case "windows":
		// The state dir is in the roaming profile, which must not hold large files.
		if localAppData := getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, defaultStateDirName), nil
		}
		return "", nil
	default:
		if xdgDataHome := getenv("XDG_DATA_HOME"); xdgDataHome != "" {
			return filepath.Join(xdgDataHome, defaultStateDirName), nil
		}
		home, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir for data dir: %w", err)
		}
		return filepath.Join(home, ".local", "share", defaultStateDirName), nil
	}
}

// LegacyDataDir returns <state dir>/data when it exists but is no longer the data dir,
// so the operator can remove data the gateway does not use anymore.
func (c Config) LegacyDataDir() string {
	legacy := filepath.Join(c.StateDir, "data")
	if filepath.Clean(c.DataDir) == legacy {
		return ""
	}
	if info, err := os.Stat(legacy); err == nil && info.IsDir() {
		return legacy
	}
	return ""
}

// LoadConfig reads <stateDir>/gateway.yaml (or configPath when set). A missing default
// config file yields the defaults; unknown fields are an error.
func LoadConfig(stateDir string, configPath string) (Config, error) {
	cfg := Config{}
	explicit := configPath != ""
	if !explicit {
		configPath = filepath.Join(stateDir, configFileName)
	}

	// #nosec G304 -- the config path is chosen by the operator on the host.
	data, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if len(data) > maxConfigFileSizeBytes {
			return Config{}, fmt.Errorf("config %s is larger than %d bytes", configPath, maxConfigFileSizeBytes)
		}
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
			return Config{}, fmt.Errorf("parse config %s: %w", configPath, err)
		}
	case errors.Is(err, os.ErrNotExist) && !explicit:
	default:
		return Config{}, fmt.Errorf("read config %s: %w", configPath, err)
	}

	cfg.StateDir = stateDir
	return cfg, nil
}

// Resolve applies defaults and validates the configuration.
func (c *Config) Resolve() error {
	if c.StateDir == "" {
		return errors.New("state dir is required")
	}
	absState, err := filepath.Abs(c.StateDir)
	if err != nil {
		return fmt.Errorf("resolve state dir: %w", err)
	}
	c.StateDir = absState

	if c.Listen == "" {
		c.Listen = defaultListen
	}
	addrPort, err := netip.ParseAddrPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen must be an IP literal with port (e.g. %s): %w", defaultListen, err)
	}
	if addrPort.Addr().IsUnspecified() {
		return fmt.Errorf("refusing wildcard listen address %s; bind to loopback or a specific bridge IP", c.Listen)
	}
	c.ListenAddr = addrPort

	if len(c.AllowedHosts) == 0 {
		c.AllowedHosts = []string{"localhost", "127.0.0.1", "::1", "host.docker.internal"}
	}
	if len(c.AllowedSourceCIDRs) == 0 {
		c.AllowedSourceCIDRs = []string{"127.0.0.0/8", "::1/128"}
	}
	c.SourcePrefixes = nil
	for _, cidr := range c.AllowedSourceCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil {
			return fmt.Errorf("invalid allowed_source_cidrs entry %q: %w", cidr, err)
		}
		if prefix.Bits() == 0 {
			return fmt.Errorf("allowed_source_cidrs entry %q would allow every address", cidr)
		}
		c.SourcePrefixes = append(c.SourcePrefixes, prefix.Masked())
	}

	if c.DataDir == "" {
		c.DataDir = filepath.Join(c.StateDir, "data")
	}
	if c.AgentDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home dir for agent_dir: %w", err)
		}
		c.AgentDir = filepath.Join(home, defaultAgentDirName)
	}
	if c.AgentDir, err = filepath.Abs(c.AgentDir); err != nil {
		return fmt.Errorf("resolve agent dir: %w", err)
	}
	if c.DataDir, err = filepath.Abs(c.DataDir); err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	if c.SharedCacheDir != "" {
		if c.SharedCacheDir, err = filepath.Abs(c.SharedCacheDir); err != nil {
			return fmt.Errorf("resolve shared cache dir: %w", err)
		}
	}

	c.JobTimeoutValue, err = parseDurationDefault(c.JobTimeout, defaultJobTimeout)
	if err != nil {
		return fmt.Errorf("job_timeout: %w", err)
	}
	c.ApprovalTTLValue, err = parseDurationDefault(c.ApprovalTTL, defaultApprovalTTL)
	if err != nil {
		return fmt.Errorf("approval_ttl: %w", err)
	}
	if c.MinFreeDiskGB == 0 {
		c.MinFreeDiskGB = defaultMinFreeDiskGB
	}
	if c.OutputLimitBytes <= 0 {
		c.OutputLimitBytes = defaultOutputLimit
	}
	if c.SnapshotMaxBytes <= 0 {
		c.SnapshotMaxBytes = defaultSnapshotBytes
	}
	if c.SnapshotMaxFiles <= 0 {
		c.SnapshotMaxFiles = defaultSnapshotFiles
	}

	for i, name := range c.EnvPassthrough {
		name = strings.TrimSpace(name)
		if !envNamePattern.MatchString(name) {
			return fmt.Errorf("env_passthrough entry %q is not a valid environment variable name", name)
		}
		c.EnvPassthrough[i] = name
	}

	for i, prefix := range c.PullAllowlist {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" || !strings.Contains(prefix, "/") {
			return fmt.Errorf("pull_allowlist entry %q must name at least <registry>/<namespace>", prefix)
		}
		c.PullAllowlist[i] = strings.TrimSuffix(prefix, "/")
	}

	for i, mapping := range c.AgentWorkspaces {
		agent := strings.TrimSpace(mapping.Agent)
		host := strings.TrimSpace(mapping.Host)
		if !strings.HasPrefix(agent, "/") || path.Clean(agent) != agent || agent == "/" {
			return fmt.Errorf("agent_workspaces[%d].agent %q must be a clean absolute path other than /", i, mapping.Agent)
		}
		if !filepath.IsAbs(host) {
			return fmt.Errorf("agent_workspaces[%d].host %q must be an absolute host path", i, mapping.Host)
		}
		c.AgentWorkspaces[i] = WorkspaceMapping{Agent: agent, Host: filepath.Clean(host)}
	}

	if c.BinarySHA256 != "" && !sha256HexPattern.MatchString(c.BinarySHA256) {
		return errors.New("binary_sha256 must be 64 lowercase hex characters")
	}

	for _, dir := range []string{c.StateDir, c.DataDir, c.AgentDir} {
		if mapping, ok := c.hostPathInAgentWorkspace(dir); ok {
			return fmt.Errorf("%s is inside the agent-writable workspace %s; move the gateway directories outside of it", dir, mapping.Host)
		}
	}
	// The agent dir is mounted into containers, so it must not expose gateway state.
	for _, dir := range []string{c.StateDir, c.DataDir} {
		if isWithin(c.AgentDir, dir) || isWithin(dir, c.AgentDir) {
			return fmt.Errorf("agent_dir %s must not overlap the gateway directory %s", c.AgentDir, dir)
		}
	}
	return nil
}

// hostPathInAgentWorkspace reports whether a host path is inside (or contains) an
// agent-writable workspace.
func (c *Config) hostPathInAgentWorkspace(hostPath string) (WorkspaceMapping, bool) {
	for _, mapping := range c.AgentWorkspaces {
		if isWithin(mapping.Host, hostPath) || isWithin(hostPath, mapping.Host) {
			return mapping, true
		}
	}
	return WorkspaceMapping{}, false
}

func parseDurationDefault(value string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if parsed <= 0 {
		return 0, errors.New("must be positive")
	}
	return parsed, nil
}

// isWithin reports whether target equals base or lies below it (host paths).
func isWithin(base string, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// ensurePrivateDir creates dir with owner-only permissions.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, stateDirPermission); err != nil {
		return err
	}
	return os.Chmod(dir, stateDirPermission)
}

// hostOnly strips an optional port from a Host header value.
func hostOnly(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(hostport, "[]")
}
