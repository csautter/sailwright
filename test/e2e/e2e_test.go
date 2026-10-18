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
	executor, closeExecutor, err := runner.NewExecutor(ctx, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeExecutor() })

	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			var target runner.Target
			if scenario.Needs(runner.RequireTarget) {
				if cfg.Target == nil {
					t.Skipf("set %s (e.g. ubuntu/server/amd64) to run this scenario", runner.EnvTarget)
				}
				target = *cfg.Target
			}
			if scenario.Needs(runner.RequireHostChanges) {
				if ok, reason := executor.HostChangesAllowed(ctx); !ok {
					t.Skip(reason)
				}
			}

			report := runner.Run(ctx, scenario, executor, runner.RunOptions{Target: target, Getenv: os.Getenv, Logf: t.Logf})
			if err := runner.WriteReports(cfg.OutDir, report); err != nil {
				t.Errorf("write report: %v", err)
			}
			for _, step := range report.Failures() {
				t.Errorf("step %q failed: %s", step.Name, step.Reason)
			}
		})
	}
}
