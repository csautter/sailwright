//go:build e2e

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/csautter/dev-alchemy/test/e2e/runner"
)

func TestScenarios(t *testing.T) {
	scenarios, err := runner.LoadScenarios("scenarios")
	if err != nil {
		t.Fatal(err)
	}
	if unknown := runner.UnknownEnv(os.Environ(), scenarios); len(unknown) > 0 {
		t.Fatalf("unknown variables %s; check for typos (e.g. %s, not %sS)", strings.Join(unknown, ", "), runner.EnvTarget, runner.EnvTarget)
	}
	cfg, err := runner.ConfigFromEnv(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// Live command output; go test prints it immediately with -v.
	executor, closeExecutor, err := runner.NewExecutor(ctx, cfg, runner.NewPrefixWriter(os.Stdout, "    | "))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeExecutor() })
	host, err := executor.Describe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			if scenario.HostOS != "" && scenario.HostOS != host.HostOS {
				t.Skipf("only for %s hosts; this host is %s", scenario.HostOS, host.HostOS)
			}
			if scenario.Needs(runner.RequireHostChanges) {
				if ok, reason := executor.HostChangesAllowed(ctx); !ok {
					t.Skip(reason)
				}
			}
			if len(scenario.Matrix()) > 0 {
				runMatrix(ctx, t, cfg, scenario, executor)
				return
			}

			var target runner.Target
			if scenario.Needs(runner.RequireTarget) {
				if cfg.Target == nil {
					t.Skipf("set %s (e.g. ubuntu/server/amd64) to run this scenario", runner.EnvTarget)
				}
				target = *cfg.Target
			}
			runAndReport(ctx, t, cfg, scenario, executor, runner.RunOptions{Target: target})
		})
	}
}

// runMatrix runs a scenario once per matrix target, each as its own subtest.
func runMatrix(ctx context.Context, t *testing.T, cfg runner.Config, scenario runner.Scenario, executor runner.Executor) {
	plans, uncovered, err := runner.PlanMatrix(ctx, scenario, executor, runner.MatrixOptions{Filter: cfg.Target, IncludeUnstable: cfg.IncludeUnstable})
	if err != nil {
		t.Fatal(err)
	}
	if len(uncovered) > 0 && cfg.Target == nil {
		var names []string
		for _, entry := range uncovered {
			names = append(names, runner.Target{OS: entry.OS, Type: entry.Type, Arch: entry.Arch, Engine: entry.Engine}.String())
		}
		t.Errorf("this host offers targets that %s does not test; add them to its targets: %s", scenario.File, strings.Join(names, ", "))
	}
	if len(plans) == 0 {
		t.Skipf("no target of this scenario matches %s=%s", runner.EnvTarget, cfg.Target)
	}
	for _, plan := range plans {
		t.Run(plan.Target.Slug(), func(t *testing.T) {
			if plan.Skip != "" {
				t.Skip(plan.Skip)
			}
			runAndReport(ctx, t, cfg, scenario, executor, runner.RunOptions{Target: plan.Target, Matrix: true})
		})
	}
}

func runAndReport(ctx context.Context, t *testing.T, cfg runner.Config, scenario runner.Scenario, executor runner.Executor, opts runner.RunOptions) {
	opts.Getenv = os.Getenv
	opts.Logf = t.Logf
	report := runner.Run(ctx, scenario, executor, opts)
	if err := runner.WriteReports(cfg.OutDir, report); err != nil {
		t.Errorf("write report: %v", err)
	}
	for _, step := range report.Failures() {
		t.Errorf("step %q failed: %s", step.Name, step.Reason)
	}
}
