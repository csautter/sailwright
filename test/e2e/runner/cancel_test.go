package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/csautter/dev-alchemy/test/e2e/runner"
)

// cancelExecutor blocks build steps until they are cancelled and reports the build
// state that a later list sees.
type cancelExecutor struct {
	stubExecutor
	finishBuild bool
	buildState  string
}

func (c *cancelExecutor) Run(ctx context.Context, req runner.StepRequest) (runner.Result, error) {
	if req.Step.Op == runner.OpBuild && !c.finishBuild {
		<-ctx.Done()
		return runner.Result{ExitCode: -1}, ctx.Err()
	}
	return c.stubExecutor.Run(ctx, req)
}

func (c *cancelExecutor) List(context.Context, string) ([]runner.ListedTarget, error) {
	return []runner.ListedTarget{{OS: "ubuntu", Type: "server", Arch: "amd64", Fields: map[string]string{"build": c.buildState}}}, nil
}

func TestCancelAfterStep(t *testing.T) {
	scenario, err := runner.LoadScenario(writeScenario(t, "cancel", `name: cancel
requires: [target]
steps:
  - name: cancelled build
    op: build
    cancel_after: 50ms
    expect:
      state: {command: build, field: build, regex: "^(missing|incomplete)$"}
`))
	if err != nil {
		t.Fatal(err)
	}
	target := runner.Target{OS: "ubuntu", Type: "server", Arch: "amd64"}
	opts := runner.RunOptions{Target: target, Getenv: func(string) string { return "" }}

	for _, tc := range []struct {
		name    string
		exec    *cancelExecutor
		passed  bool
		problem string
	}{
		{name: "artifact missing", exec: &cancelExecutor{buildState: "missing"}, passed: true},
		{name: "artifact looks complete", exec: &cancelExecutor{buildState: "exists"}, problem: "field build"},
		{name: "build finished early", exec: &cancelExecutor{finishBuild: true, buildState: "missing"}, problem: "finished before"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := runner.Run(context.Background(), scenario, tc.exec, opts)
			if report.Passed != tc.passed {
				t.Fatalf("passed=%v, want %v:\n%s", report.Passed, tc.passed, runner.Markdown(report))
			}
			if tc.problem != "" && !strings.Contains(report.Steps[0].Reason, tc.problem) {
				t.Fatalf("expected reason to mention %q, got %q", tc.problem, report.Steps[0].Reason)
			}
		})
	}

	if _, err := runner.LoadScenario(writeScenario(t, "bad", "name: bad\nsteps:\n  - {name: x, op: build, cancel_after: soon}\n")); err == nil {
		t.Fatal("expected an invalid cancel_after to be rejected")
	}
}
