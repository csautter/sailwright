package runner

import (
	"context"

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

	defaultOutDir = "out"
)

// Config selects and configures the executor.
type Config struct {
	Executor         string
	Binary           string
	Target           *Target
	AllowHostChanges bool
	OutDir           string
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
			return Config{}, fmt.Errorf("%s is required for the direct executor", EnvBinary)
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
		executor := &GatewayExecutor{Endpoint: cfg.Gateway}
		if err := executor.Connect(ctx); err != nil {
			return nil, nil, err
		}
		return executor, executor.Close, nil
	}
	return nil, nil, errors.New("unknown executor")
}
