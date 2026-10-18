package runner_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/internal/gateway/client"
	"github.com/csautter/dev-alchemy/internal/gateway/gatewaytest"
	"github.com/csautter/dev-alchemy/test/e2e/runner"
)

func TestRepositoryScenariosAreValid(t *testing.T) {
	scenarios, err := runner.LoadScenarios("../scenarios")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, scenario := range scenarios {
		names[scenario.Name] = true
	}
	for _, want := range []string{"smoke", "vm-lifecycle", "destroy-idempotent", "local-check"} {
		if !names[want] {
			t.Fatalf("scenario %s missing", want)
		}
	}
}

func writeScenario(t *testing.T, name string, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadScenarioRejectsInvalidFiles(t *testing.T) {
	cases := map[string]string{
		"unknown-field": "name: unknown-field\nsteps: [{name: a, op: cli, with: {args: [x]}}]\nbogus: 1\n",
		"unknown-op":    "name: unknown-op\nsteps: [{name: a, op: rm}]\n",
		"no-steps":      "name: no-steps\nsteps: []\n",
		"wrong-name":    "name: other\nsteps: [{name: a, op: cli, with: {args: [x]}}]\n",
		"bad-regex":     "name: bad-regex\nsteps: [{name: a, op: cli, with: {args: [x]}, expect: {output_regex: '('}}]\n",
		"needs-target":  "name: needs-target\nsteps: [{name: a, op: create}]\n",
		"bad-recap":     "name: bad-recap\nrequires: [target]\nsteps: [{name: a, op: provision, expect: {recap: {broken: 1}}}]\n",
		"cli-no-args":   "name: cli-no-args\nsteps: [{name: a, op: cli}]\n",
		"bad-only":      "name: bad-only\nsteps: [{name: a, op: cli, only: ssh, with: {args: [x]}}]\n",
	}
	for name, body := range cases {
		if _, err := runner.LoadScenario(writeScenario(t, name, body)); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

func TestParseRecapSumsHosts(t *testing.T) {
	output := `PLAY [all] ***
TASK [x] ***
PLAY RECAP *********************************************************************
vm1                        : ok=5    changed=2    unreachable=0    failed=0    skipped=1    rescued=0    ignored=0
vm2                        : ok=3    changed=1    unreachable=0    failed=1    skipped=0    rescued=0    ignored=0
`
	recap, ok := runner.ParseRecap(output)
	if !ok || recap["ok"] != 8 || recap["changed"] != 3 || recap["failed"] != 1 {
		t.Fatalf("unexpected recap %v (%v)", recap, ok)
	}
	if _, ok := runner.ParseRecap("no recap here"); ok {
		t.Fatal("expected no recap")
	}
}

func TestParseTarget(t *testing.T) {
	target, err := runner.ParseTarget("ubuntu/server/amd64")
	if err != nil || target.OS != "ubuntu" || target.Type != "server" || target.Arch != "amd64" {
		t.Fatalf("got %+v, %v", target, err)
	}
	target, err = runner.ParseTarget("windows11//arm64")
	if err != nil || target.Type != "" || target.Arch != "arm64" {
		t.Fatalf("got %+v, %v", target, err)
	}
	for _, bad := range []string{"", "/server", "a/b/c/d"} {
		if _, err := runner.ParseTarget(bad); err == nil {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}

// stubExecutor fails selected ops and records what ran.
type stubExecutor struct {
	failOps map[string]bool
	ran     []string
}

func (s *stubExecutor) Name() string { return runner.ExecutorDirect }
func (s *stubExecutor) Describe(context.Context) (runner.Environment, error) {
	return runner.Environment{Executor: "stub"}, nil
}
func (s *stubExecutor) HostChangesAllowed(context.Context) (bool, string) { return true, "" }
func (s *stubExecutor) List(context.Context, string) ([]runner.ListedTarget, error) {
	return nil, errors.New("not supported")
}
func (s *stubExecutor) Run(_ context.Context, req runner.StepRequest) (runner.Result, error) {
	s.ran = append(s.ran, req.Step.Name)
	if s.failOps[req.Step.Op] {
		return runner.Result{ExitCode: 1, Output: "boom"}, nil
	}
	return runner.Result{ExitCode: 0, Output: "fine"}, nil
}

func TestRunSkipsAfterFailureButAlwaysRunsCleanup(t *testing.T) {
	scenario, err := runner.LoadScenario(writeScenario(t, "flow", `name: flow
requires: [target]
steps:
  - {name: create, op: create}
  - {name: start, op: start}
  - {name: provision, op: provision}
  - {name: gated, op: cli, when_env: SAILWRIGHT_E2E_NOT_SET, with: {args: [x]}}
always:
  - {name: destroy, op: destroy}
`))
	if err != nil {
		t.Fatal(err)
	}
	executor := &stubExecutor{failOps: map[string]bool{"start": true}}
	report := runner.Run(context.Background(), scenario, executor, runner.RunOptions{Target: runner.Target{OS: "ubuntu"}, Getenv: func(string) string { return "" }})
	if report.Passed {
		t.Fatal("expected the scenario to fail")
	}
	if strings.Join(executor.ran, ",") != "create,start,destroy" {
		t.Fatalf("unexpected steps run: %v", executor.ran)
	}
	statuses := []string{}
	for _, step := range report.Steps {
		statuses = append(statuses, step.Status)
	}
	if strings.Join(statuses, ",") != "passed,failed,skipped,skipped,passed" {
		t.Fatalf("unexpected statuses %v", statuses)
	}
	if !strings.Contains(runner.Markdown(report), "## Failed: start") {
		t.Fatal("markdown must describe the failure")
	}
}

func TestEnvExpansionOnlyAllowsE2EVariables(t *testing.T) {
	scenario, err := runner.LoadScenario(writeScenario(t, "expand", `name: expand
steps:
  - {name: echo, op: cli, with: {args: ["${SAILWRIGHT_E2E_VALUE}", "${HOME}"]}}
`))
	if err != nil {
		t.Fatal(err)
	}
	args := scenario.Steps[0].Args(func(name string) string { return "value-of-" + name })
	if args[0] != "value-of-SAILWRIGHT_E2E_VALUE" || args[1] != "${HOME}" {
		t.Fatalf("unexpected expansion %q", args)
	}
}

func loadRepoScenario(t *testing.T, name string) runner.Scenario {
	t.Helper()
	scenario, err := runner.LoadScenario(filepath.Join("..", "scenarios", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return scenario
}

var approvalID = regexp.MustCompile(`approve ([0-9a-f]{8})"`)

// autoApprove plays the human at the gateway terminal for the self-test.
func autoApprove(t *testing.T, h *gatewaytest.Harness, stop <-chan struct{}) {
	approved := map[string]bool{}
	for {
		select {
		case <-stop:
			return
		case <-time.After(20 * time.Millisecond):
		}
		for _, match := range approvalID.FindAllStringSubmatch(h.Console.String(), -1) {
			if !approved[match[1]] {
				approved[match[1]] = true
				h.Approve(t, match[1])
			}
		}
	}
}

// TestScenariosAgainstGatewayWithFakeCLI runs the repository scenarios through the real
// gateway (MCP, policy, approval) with a fake sailwright binary behind it.
func TestScenariosAgainstGatewayWithFakeCLI(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{Approvals: true})
	endpoint := h.TCPEndpoint()
	if h.SocketPath != "" {
		endpoint = h.SocketEndpoint()
	}
	executor := &runner.GatewayExecutor{Endpoint: endpoint, PollInterval: 20 * time.Millisecond}
	if err := executor.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = executor.Close() })

	stop := make(chan struct{})
	defer close(stop)
	go autoApprove(t, h, stop)

	getenv := func(string) string { return "" }
	target := runner.Target{OS: "ubuntu", Type: "server", Arch: "amd64"}
	for _, name := range []string{"smoke", "vm-lifecycle", "destroy-idempotent", "local-check"} {
		report := runner.Run(context.Background(), loadRepoScenario(t, name), executor, runner.RunOptions{Target: target, Getenv: getenv, Logf: t.Logf})
		if !report.Passed {
			t.Fatalf("scenario %s failed:\n%s", name, runner.Markdown(report))
		}
		if report.Environment.Executor != runner.ExecutorGateway || report.Environment.BinarySHA256 == "" {
			t.Fatalf("unexpected environment %+v", report.Environment)
		}
	}

	// The host-changing step must have gone through an approval.
	if !strings.Contains(h.Console.String(), "approval") || !strings.Contains(h.Console.String(), "provision local --yes --check") {
		t.Fatalf("expected an approval for local provisioning:\n%s", h.Console.String())
	}
}

func TestScenariosWithDirectExecutorAndFakeCLI(t *testing.T) {
	binary := gatewaytest.BuildFakeCLI(t)
	t.Setenv("FAKE_CLI_STATE", t.TempDir())
	executor := &runner.DirectExecutor{Binary: binary}
	target := runner.Target{OS: "windows11", Arch: "amd64"}
	getenv := func(string) string { return "" }

	for _, name := range []string{"smoke", "vm-lifecycle", "destroy-idempotent"} {
		report := runner.Run(context.Background(), loadRepoScenario(t, name), executor, runner.RunOptions{Target: target, Getenv: getenv})
		if !report.Passed {
			t.Fatalf("scenario %s failed:\n%s", name, runner.Markdown(report))
		}
	}

	if ok, _ := executor.HostChangesAllowed(context.Background()); ok {
		t.Fatal("host changes must be off by default")
	}

	out := t.TempDir()
	report := runner.Run(context.Background(), loadRepoScenario(t, "smoke"), executor, runner.RunOptions{Getenv: getenv})
	if err := runner.WriteReports(out, report); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"smoke.json", "smoke.md"} {
		if _, err := os.Stat(filepath.Join(out, file)); err != nil {
			t.Fatalf("report %s missing: %v", file, err)
		}
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := map[string]string{}
	getenv := func(name string) string { return env[name] }
	if _, err := runner.ConfigFromEnv(getenv); err == nil {
		t.Fatal("direct executor without binary must fail")
	}
	env[runner.EnvBinary] = "/bin/sailwright"
	env[runner.EnvTarget] = "ubuntu/server/amd64"
	cfg, err := runner.ConfigFromEnv(getenv)
	if err != nil || cfg.Executor != runner.ExecutorDirect || cfg.Target.OS != "ubuntu" || cfg.AllowHostChanges {
		t.Fatalf("unexpected config %+v, %v", cfg, err)
	}
	env[runner.EnvExecutor] = runner.ExecutorGateway
	env[client.EnvDir] = t.TempDir()
	if _, err := runner.ConfigFromEnv(getenv); err == nil {
		t.Fatal("gateway executor without a token must fail")
	}
	env[client.EnvToken] = "token"
	cfg, err = runner.ConfigFromEnv(getenv)
	if err != nil || cfg.Gateway.URL != client.DefaultTCPURL || cfg.Gateway.Token != "token" {
		t.Fatalf("unexpected config %+v, %v", cfg, err)
	}
}

func TestUnknownEnvCatchesTypos(t *testing.T) {
	scenarios, err := runner.LoadScenarios("../scenarios")
	if err != nil {
		t.Fatal(err)
	}
	environ := []string{
		"SAILWRIGHT_E2E_TARGETS=ubuntu/server/amd64",
		"SAILWRIGHT_E2E_TARGET=ubuntu/server/amd64",
		"SAILWRIGHT_E2E_PULL_REF=ghcr.io/x/y:z",
		"SAILWRIGHT_GATEWAY_DIR=/run/sailwright-gateway",
		"HOME=/root",
	}
	if got := runner.UnknownEnv(environ, scenarios); len(got) != 1 || got[0] != "SAILWRIGHT_E2E_TARGETS" {
		t.Fatalf("expected only the typo to be reported, got %v", got)
	}
}
