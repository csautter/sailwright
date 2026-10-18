package runner

import (
	"context"
	"sort"

	"errors"
	"fmt"
	"github.com/csautter/dev-alchemy/internal/gateway/client"
	"io"
	"strings"
)

// Environment variables understood by the E2E module.
const (
	EnvExecutor         = "SAILWRIGHT_E2E_EXECUTOR"
	EnvBinary           = "SAILWRIGHT_E2E_BIN"
	EnvTarget           = "SAILWRIGHT_E2E_TARGET"
	EnvAllowHostChanges = "SAILWRIGHT_E2E_ALLOW_HOST_CHANGES"
	EnvOutDir           = "SAILWRIGHT_E2E_OUT"
	EnvIncludeUnstable  = "SAILWRIGHT_E2E_INCLUDE_UNSTABLE"

	defaultOutDir = "out"
)

// Config selects and configures the executor.
type Config struct {
	Executor         string
	Binary           string
	Target           *Target
	AllowHostChanges bool
	OutDir           string
	IncludeUnstable  bool
	// Gateway is the discovered gateway endpoint (gateway executor only), see
	// internal/gateway/client for the discovery rules.
	Gateway client.Endpoint
}

// ConfigFromEnv reads the E2E configuration from the environment.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := Config{
		Executor:         strings.TrimSpace(getenv(EnvExecutor)),
		Binary:           strings.TrimSpace(getenv(EnvBinary)),
		AllowHostChanges: getenv(EnvAllowHostChanges) == "1",
		IncludeUnstable:  getenv(EnvIncludeUnstable) == "1",
		OutDir:           strings.TrimSpace(getenv(EnvOutDir)),
	}
	if cfg.Executor == "" {
		cfg.Executor = ExecutorDirect
	}
	if cfg.OutDir == "" {
		cfg.OutDir = defaultOutDir
	}
	if value := strings.TrimSpace(getenv(EnvTarget)); value != "" {
		target, err := ParseTarget(value)
		if err != nil {
			return Config{}, err
		}
		cfg.Target = &target
	}

	switch cfg.Executor {
	case ExecutorDirect:
		if cfg.Binary == "" {
			return Config{}, fmt.Errorf("%s is required for the direct executor; set %s=%s to run through sailwright-gateway instead", EnvBinary, EnvExecutor, ExecutorGateway)
		}
	case ExecutorGateway:
		endpoint, err := client.Discover(getenv)
		if err != nil {
			return Config{}, err
		}
		cfg.Gateway = endpoint
	default:
		return Config{}, fmt.Errorf("%s must be %q or %q", EnvExecutor, ExecutorDirect, ExecutorGateway)
	}
	return cfg, nil
}

// NewExecutor creates and connects the configured executor.
func NewExecutor(ctx context.Context, cfg Config, stream io.Writer) (Executor, func() error, error) {
	switch cfg.Executor {
	case ExecutorDirect:
		return &DirectExecutor{Binary: cfg.Binary, AllowHostChanges: cfg.AllowHostChanges, Stream: stream}, func() error { return nil }, nil
	case ExecutorGateway:
		executor := &GatewayExecutor{Endpoint: cfg.Gateway, Stream: stream}
		if err := executor.Connect(ctx); err != nil {
			return nil, nil, err
		}
		return executor, executor.Close, nil
	}
	return nil, nil, errors.New("unknown executor")
}

// UnknownEnv returns SAILWRIGHT_E2E_* variables from environ that neither the runner
// nor any scenario uses, which usually means a typo such as SAILWRIGHT_E2E_TARGETS.
func UnknownEnv(environ []string, scenarios []Scenario) []string {
	known := map[string]bool{EnvExecutor: true, EnvBinary: true, EnvTarget: true, EnvAllowHostChanges: true, EnvOutDir: true, EnvIncludeUnstable: true}
	for _, scenario := range scenarios {
		for _, name := range scenario.EnvReferences() {
			known[name] = true
		}
	}
	var unknown []string
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "SAILWRIGHT_E2E_") && !known[name] {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown
}
