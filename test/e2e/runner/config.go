package runner

import (
	"context"
	"errors"
	"fmt"
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
	EnvGatewayURL       = "SAILWRIGHT_GATEWAY_URL"
	EnvGatewayToken     = "SAILWRIGHT_GATEWAY_TOKEN"

	defaultGatewayURL = "http://host.docker.internal:7391/mcp"
	defaultOutDir     = "out"
)

// Config selects and configures the executor.
type Config struct {
	Executor         string
	Binary           string
	Target           *Target
	AllowHostChanges bool
	OutDir           string
	GatewayURL       string
	GatewayToken     string
}

// ConfigFromEnv reads the E2E configuration from the environment.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := Config{
		Executor:         strings.TrimSpace(getenv(EnvExecutor)),
		Binary:           strings.TrimSpace(getenv(EnvBinary)),
		AllowHostChanges: getenv(EnvAllowHostChanges) == "1",
		OutDir:           strings.TrimSpace(getenv(EnvOutDir)),
		GatewayURL:       strings.TrimSpace(getenv(EnvGatewayURL)),
		GatewayToken:     strings.TrimSpace(getenv(EnvGatewayToken)),
	}
	if cfg.Executor == "" {
		cfg.Executor = ExecutorDirect
	}
	if cfg.OutDir == "" {
		cfg.OutDir = defaultOutDir
	}
	if cfg.GatewayURL == "" {
		cfg.GatewayURL = defaultGatewayURL
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
		if cfg.GatewayToken == "" {
			return Config{}, fmt.Errorf("%s is required for the gateway executor", EnvGatewayToken)
		}
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
		executor := &GatewayExecutor{URL: cfg.GatewayURL, Token: cfg.GatewayToken}
		if err := executor.Connect(ctx); err != nil {
			return nil, nil, err
		}
		return executor, executor.Close, nil
	}
	return nil, nil, errors.New("unknown executor")
}
