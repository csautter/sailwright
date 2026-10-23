package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

// DirectExecutor runs the sailwright binary itself. It is meant for disposable CI
// runners and for a human on the host; agents use the gateway executor.
type DirectExecutor struct {
	Binary string
	// AllowHostChanges enables provision_local and other host-modifying steps.
	AllowHostChanges bool
	// Stream receives live output (optional).
	Stream io.Writer
}

// Name implements Executor.
func (d *DirectExecutor) Name() string { return ExecutorDirect }

// Describe implements Executor.
func (d *DirectExecutor) Describe(context.Context) (Environment, error) {
	sum, err := hashFile(d.Binary)
	if err != nil {
		return Environment{}, err
	}
	return Environment{Executor: ExecutorDirect, HostOS: runtime.GOOS, HostArch: runtime.GOARCH, BinarySHA256: sum}, nil
}

// HostChangesAllowed implements Executor.
func (d *DirectExecutor) HostChangesAllowed(context.Context) (bool, string) {
	if d.AllowHostChanges {
		return true, ""
	}
	return false, "set SAILWRIGHT_E2E_ALLOW_HOST_CHANGES=1 to run host-modifying steps directly"
}

// Run implements Executor.
func (d *DirectExecutor) Run(ctx context.Context, req StepRequest) (Result, error) {
	argv, err := DirectArgv(req)
	if err != nil {
		return Result{}, err
	}
	if req.Step.Op == OpProvisionLocal && !d.AllowHostChanges {
		return Result{}, errors.New("host changes are not allowed for the direct executor")
	}
	output, code, err := d.exec(ctx, argv)
	return Result{Argv: argv, ExitCode: code, Output: output}, err
}

// List implements Executor.
func (d *DirectExecutor) List(ctx context.Context, command string) ([]ListedTarget, error) {
	output, code, err := d.exec(ctx, []string{command, "list", "--json"})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("`%s list --json` exited with %d", command, code)
	}
	return parseListing(output)
}

func (d *DirectExecutor) exec(ctx context.Context, argv []string) (string, int, error) {
	// #nosec G204 -- the operator chooses the binary; argv comes from repository scenarios.
	cmd := exec.CommandContext(ctx, d.Binary, argv...)
	var buf bytes.Buffer
	var out io.Writer = &buf
	if d.Stream != nil {
		out = io.MultiWriter(&buf, d.Stream)
	}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return buf.String(), 0, nil
	case errors.As(err, &exitErr):
		return buf.String(), exitErr.ExitCode(), nil
	default:
		return buf.String(), -1, err
	}
}

// DirectArgv maps a step to sailwright argv. The gateway applies its own grammar to
// the equivalent tool call, so both executors run the same command.
func DirectArgv(req StepRequest) ([]string, error) {
	step := req.Step
	getenv := req.Getenv
	targetArgs := func(command string) []string {
		argv := []string{command, req.Target.OS}
		if req.Target.Arch != "" {
			argv = append(argv, "--arch", req.Target.Arch)
		}
		if req.Target.Type != "" {
			argv = append(argv, "--type", req.Target.Type)
		}
		return argv
	}

	switch step.Op {
	case OpBuild:
		argv := targetArgs("build")
		if engine := stepEngine(req); engine != "" {
			argv = append(argv, "--engine", engine)
		}
		if step.Bool("headless") {
			argv = append(argv, "--headless")
		}
		return argv, nil
	case OpCreate, OpStart, OpStop, OpDestroy:
		return targetArgs(step.Op), nil
	case OpProvision:
		argv := targetArgs("provision")
		if step.Bool("check") {
			argv = append(argv, "--check")
		}
		if verbosity, ok := step.Int("verbosity"); ok {
			argv = append(argv, "--verbosity", strconv.Itoa(verbosity))
		}
		return argv, nil
	case OpPull:
		ref := step.String("ref", getenv)
		if ref == "" {
			return nil, errors.New("pull needs with.ref")
		}
		argv := []string{"pull", ref, "--os", req.Target.OS, "--yes"}
		if req.Target.Arch != "" {
			argv = append(argv, "--arch", req.Target.Arch)
		}
		if req.Target.Type != "" {
			argv = append(argv, "--type", req.Target.Type)
		}
		if engine := stepEngine(req); engine != "" {
			argv = append(argv, "--engine", engine)
		}
		return argv, nil
	case OpProvisionLocal:
		argv := []string{"provision", "local", "--yes"}
		if step.Bool("check") {
			argv = append(argv, "--check")
		}
		return argv, nil
	case OpList:
		return []string{step.String("command", getenv), "list", "--json"}, nil
	case OpCLI:
		return step.Args(getenv), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrStepNotSupported, step.Op)
}

func hashFile(path string) (string, error) {
	// #nosec G304 -- operator-selected binary.
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
