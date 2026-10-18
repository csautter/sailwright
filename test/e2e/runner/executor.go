package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Executor names.
const (
	ExecutorDirect  = "direct"
	ExecutorGateway = "gateway"
)

// Target identifies the VM a scenario works on.
type Target struct {
	OS   string `json:"os"`
	Type string `json:"type,omitempty"`
	Arch string `json:"arch,omitempty"`
}

// ParseTarget parses "os[/type][/arch]", e.g. "ubuntu/server/amd64" or "windows11//arm64".
func ParseTarget(value string) (Target, error) {
	parts := strings.Split(value, "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 3 {
		return Target{}, fmt.Errorf("target %q must look like os/type/arch", value)
	}
	target := Target{OS: parts[0]}
	if len(parts) > 1 {
		target.Type = parts[1]
	}
	if len(parts) > 2 {
		target.Arch = parts[2]
	}
	return target, nil
}

// String renders the target as os/type/arch.
func (t Target) String() string {
	return strings.Join([]string{t.OS, t.Type, t.Arch}, "/")
}

// StepRequest is one operation for an executor.
type StepRequest struct {
	Step   Step
	Target Target
	Getenv func(string) string
	Logf   func(format string, args ...any)
}

// Result is what an executor reports for one operation.
type Result struct {
	Argv     []string
	ExitCode int
	Output   string
	Note     string
}

// Environment describes where the scenario ran.
type Environment struct {
	Executor     string `json:"executor"`
	HostOS       string `json:"host_os"`
	HostArch     string `json:"host_arch"`
	BinarySHA256 string `json:"binary_sha256,omitempty"`
	Gateway      string `json:"gateway,omitempty"`
}

// ListedTarget is one entry of `<command> list --json`.
type ListedTarget struct {
	OS     string            `json:"os"`
	Type   string            `json:"type"`
	Arch   string            `json:"arch"`
	Engine string            `json:"engine"`
	Fields map[string]string `json:"fields"`
}

// Executor runs scenario steps.
type Executor interface {
	Name() string
	Describe(ctx context.Context) (Environment, error)
	Run(ctx context.Context, req StepRequest) (Result, error)
	List(ctx context.Context, command string) ([]ListedTarget, error)
	// HostChangesAllowed reports whether host-modifying steps can run at all.
	HostChangesAllowed(ctx context.Context) (bool, string)
}

// ErrStepNotSupported is returned when an executor cannot run an operation.
var ErrStepNotSupported = errors.New("step not supported by this executor")

// FindTarget returns the listed entry for a scenario target.
func FindTarget(targets []ListedTarget, target Target) (ListedTarget, bool) {
	arch := target.Arch
	if arch == "" {
		arch = "amd64"
	}
	for _, listed := range targets {
		if listed.OS != target.OS || listed.Arch != arch {
			continue
		}
		if listed.Type != "" && target.Type != "" && listed.Type != target.Type {
			continue
		}
		if listed.Type != "" && target.Type == "" && listed.Type != "server" {
			continue
		}
		return listed, true
	}
	return ListedTarget{}, false
}

// parseListing extracts the JSON listing line from CLI output.
func parseListing(output string) ([]ListedTarget, error) {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, `{"schema_version"`) {
			continue
		}
		var listing struct {
			SchemaVersion int            `json:"schema_version"`
			Targets       []ListedTarget `json:"targets"`
		}
		if err := json.Unmarshal([]byte(line), &listing); err != nil {
			return nil, err
		}
		return listing.Targets, nil
	}
	return nil, errors.New("no JSON listing in output")
}
