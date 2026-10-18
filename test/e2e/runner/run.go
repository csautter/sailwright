package runner

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Step statuses.
const (
	StatusPassed  = "passed"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

const outputTailLines = 30

var (
	recapKeys    = map[string]bool{"ok": true, "changed": true, "unreachable": true, "failed": true, "skipped": true, "rescued": true, "ignored": true}
	recapLine    = regexp.MustCompile(`(?m)^\S.*?\s:\s+((?:\w+=\d+\s*)+)$`)
	recapCounter = regexp.MustCompile(`(\w+)=(\d+)`)
)

// RunOptions configure a scenario run.
type RunOptions struct {
	Target Target
	// Matrix marks a run of one target of a matrix scenario; reports get the
	// target in their name.
	Matrix bool
	Getenv func(string) string
	Logf   func(format string, args ...any)
	Now    func() time.Time
}

// StepReport is the outcome of one step.
type StepReport struct {
	Name       string        `json:"name"`
	Op         string        `json:"op"`
	Preflight  bool          `json:"preflight,omitempty"`
	Always     bool          `json:"always,omitempty"`
	Status     string        `json:"status"`
	Argv       []string      `json:"argv,omitempty"`
	ExitCode   *int          `json:"exit_code,omitempty"`
	Duration   time.Duration `json:"duration_ns"`
	Reason     string        `json:"reason,omitempty"`
	OutputTail string        `json:"output_tail,omitempty"`
}

// Report is the outcome of one scenario.
type Report struct {
	// ID names the report files: the scenario, plus the target for matrix runs.
	ID          string        `json:"id"`
	Scenario    string        `json:"scenario"`
	Description string        `json:"description,omitempty"`
	Target      Target        `json:"target"`
	Environment Environment   `json:"environment"`
	Started     time.Time     `json:"started"`
	Duration    time.Duration `json:"duration_ns"`
	Passed      bool          `json:"passed"`
	Steps       []StepReport  `json:"steps"`
}

// Failures returns the failed steps.
func (r Report) Failures() []StepReport {
	var failed []StepReport
	for _, step := range r.Steps {
		if step.Status == StatusFailed {
			failed = append(failed, step)
		}
	}
	return failed
}

// Run executes a scenario. After the first failed step the remaining steps are
// skipped, but `always` steps still run, unless a preflight step failed: then the
// scenario has not changed anything and nothing runs after the preflight.
func Run(ctx context.Context, scenario Scenario, executor Executor, opts RunOptions) Report {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	started := opts.Now()
	report := Report{ID: scenario.Name, Scenario: scenario.Name, Description: scenario.Description, Target: opts.Target, Started: started.UTC(), Passed: true}
	if opts.Matrix {
		report.ID = scenario.Name + "-" + opts.Target.Slug()
	}
	if env, err := executor.Describe(ctx); err == nil {
		report.Environment = env
	} else {
		report.Environment = Environment{Executor: executor.Name()}
		opts.Logf("describe executor: %v", err)
	}

	preflightFailed := false
	for _, step := range scenario.Preflight {
		if preflightFailed {
			report.Steps = append(report.Steps, StepReport{Name: step.Name, Op: step.Op, Preflight: true, Status: StatusSkipped, Reason: "an earlier preflight step failed"})
			continue
		}
		stepReport := runStep(ctx, step, executor, opts)
		stepReport.Preflight = true
		if stepReport.Status == StatusFailed {
			preflightFailed = true
			stepReport.Reason += "; the scenario did not start and left the host unchanged"
		}
		report.Steps = append(report.Steps, stepReport)
	}

	failed := preflightFailed
	skipReason := "an earlier step failed"
	if preflightFailed {
		skipReason = "a preflight step failed"
	}
	for _, step := range scenario.Steps {
		if failed {
			report.Steps = append(report.Steps, StepReport{Name: step.Name, Op: step.Op, Status: StatusSkipped, Reason: skipReason})
			continue
		}
		stepReport := runStep(ctx, step, executor, opts)
		report.Steps = append(report.Steps, stepReport)
		failed = stepReport.Status == StatusFailed
	}
	for _, step := range scenario.Always {
		if preflightFailed {
			// Cleanup would act on state the scenario did not create.
			report.Steps = append(report.Steps, StepReport{Name: step.Name, Op: step.Op, Always: true, Status: StatusSkipped, Reason: skipReason})
			continue
		}
		stepReport := runStep(context.WithoutCancel(ctx), step, executor, opts)
		stepReport.Always = true
		report.Steps = append(report.Steps, stepReport)
	}

	report.Duration = opts.Now().Sub(started)
	report.Passed = len(report.Failures()) == 0
	return report
}

func runStep(ctx context.Context, step Step, executor Executor, opts RunOptions) StepReport {
	report := StepReport{Name: step.Name, Op: step.Op}
	if enabled, reason := step.Enabled(executor.Name(), opts.Getenv); !enabled {
		report.Status = StatusSkipped
		report.Reason = reason
		return report
	}
	if step.WhenListed != "" {
		targets, err := executor.List(ctx, step.WhenListed)
		if err != nil {
			report.Status = StatusFailed
			report.Reason = fmt.Sprintf("list %s: %v", step.WhenListed, err)
			return report
		}
		if _, ok := FindTarget(targets, opts.Target); !ok {
			report.Status = StatusSkipped
			report.Reason = fmt.Sprintf("target is not offered by `%s list` on this host", step.WhenListed)
			return report
		}
	}

	opts.Logf("step %q (%s)", step.Name, step.Op)
	started := opts.Now()
	result, err := executor.Run(ctx, StepRequest{Step: step, Target: opts.Target, Getenv: opts.Getenv, Logf: opts.Logf})
	report.Duration = opts.Now().Sub(started)
	report.Argv = result.Argv
	report.OutputTail = tail(result.Output, outputTailLines)
	if err != nil {
		report.Status = StatusFailed
		report.Reason = err.Error()
		return report
	}
	code := result.ExitCode
	report.ExitCode = &code

	if problems := checkExpectations(ctx, step, result, report.Duration, executor, opts.Target); len(problems) > 0 {
		report.Status = StatusFailed
		report.Reason = strings.Join(problems, "; ")
		return report
	}
	report.Status = StatusPassed
	return report
}

func checkExpectations(ctx context.Context, step Step, result Result, duration time.Duration, executor Executor, target Target) []string {
	var problems []string
	expect := step.Expect
	if want := expect.expectedExitCode(); result.ExitCode != want {
		problems = append(problems, fmt.Sprintf("exit code %d, want %d", result.ExitCode, want))
	}
	if expect.outputRegex != nil && !expect.outputRegex.MatchString(result.Output) {
		problems = append(problems, fmt.Sprintf("output does not match %q", expect.OutputRegex))
	}
	if expect.outputNotRegex != nil && expect.outputNotRegex.MatchString(result.Output) {
		problems = append(problems, fmt.Sprintf("output matches %q", expect.OutputNotRegex))
	}
	if expect.maxDuration > 0 && duration > expect.maxDuration {
		problems = append(problems, fmt.Sprintf("took %s, limit %s", duration.Round(time.Second), expect.maxDuration))
	}
	if len(expect.Recap) > 0 {
		recap, ok := ParseRecap(result.Output)
		if !ok {
			problems = append(problems, "no Ansible PLAY RECAP in output")
		}
		for key, want := range expect.Recap {
			if ok && recap[key] != want {
				problems = append(problems, fmt.Sprintf("recap %s=%d, want %d", key, recap[key], want))
			}
		}
	}
	if state := expect.State; state != nil {
		targets, err := executor.List(ctx, state.Command)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("list %s: %v", state.Command, err))
		default:
			listed, found := FindTarget(targets, target)
			value := "missing"
			if found {
				value = listed.Fields[state.Field]
			}
			if !state.regex.MatchString(value) {
				problems = append(problems, fmt.Sprintf("%s list field %s is %q, want /%s/", state.Command, state.Field, value, state.Regex))
			}
		}
	}
	return problems
}

// ParseRecap sums the counters of every host line in the last Ansible PLAY RECAP.
func ParseRecap(output string) (map[string]int, bool) {
	index := strings.LastIndex(output, "PLAY RECAP")
	if index < 0 {
		return nil, false
	}
	totals := map[string]int{}
	found := false
	for _, match := range recapLine.FindAllStringSubmatch(output[index:], -1) {
		for _, counter := range recapCounter.FindAllStringSubmatch(match[1], -1) {
			value, err := strconv.Atoi(counter[2])
			if err != nil {
				continue
			}
			totals[counter[1]] += value
			found = true
		}
	}
	return totals, found
}

func tail(output string, lines int) string {
	parts := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}
