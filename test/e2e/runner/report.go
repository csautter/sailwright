package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteReports writes <dir>/<scenario>.json and <scenario>.md.
func WriteReports(dir string, report Report) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	name := report.ID
	if name == "" {
		name = report.Scenario
	}
	base := filepath.Join(dir, name)
	if err := os.WriteFile(base+".json", append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.WriteFile(base+".md", []byte(Markdown(report)), 0o600)
}

// Markdown renders a human-readable report.
func Markdown(report Report) string {
	var b strings.Builder
	result := "PASSED"
	if !report.Passed {
		result = "FAILED"
	}
	fmt.Fprintf(&b, "# Scenario `%s`: %s\n\n", report.Scenario, result)
	if report.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", report.Description)
	}
	fmt.Fprintf(&b, "| | |\n| --- | --- |\n")
	fmt.Fprintf(&b, "| Executor | %s |\n", report.Environment.Executor)
	fmt.Fprintf(&b, "| Host | %s/%s |\n", report.Environment.HostOS, report.Environment.HostArch)
	if report.Target.OS != "" {
		fmt.Fprintf(&b, "| Target | `%s` |\n", report.Target)
	}
	if report.Environment.BinarySHA256 != "" {
		fmt.Fprintf(&b, "| sailwright SHA-256 | `%s` |\n", report.Environment.BinarySHA256)
	}
	fmt.Fprintf(&b, "| Started | %s |\n| Duration | %s |\n\n", report.Started.Format(time.RFC3339), report.Duration.Round(time.Second))

	fmt.Fprintf(&b, "| # | Step | Op | Status | Exit | Duration |\n| --- | --- | --- | --- | --- | --- |\n")
	for i, step := range report.Steps {
		exit := "-"
		if step.ExitCode != nil {
			exit = fmt.Sprint(*step.ExitCode)
		}
		name := step.Name
		if step.Always {
			name += " (always)"
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s |\n", i+1, name, step.Op, step.Status, exit, step.Duration.Round(time.Second))
	}

	for _, step := range report.Steps {
		if step.Status != StatusFailed {
			continue
		}
		fmt.Fprintf(&b, "\n## Failed: %s\n\n", step.Name)
		if len(step.Argv) > 0 {
			fmt.Fprintf(&b, "Command: `sailwright %s`\n\n", strings.Join(step.Argv, " "))
		}
		fmt.Fprintf(&b, "Reason: %s\n", step.Reason)
		if step.OutputTail != "" {
			fmt.Fprintf(&b, "\n```text\n%s\n```\n", step.OutputTail)
		}
	}
	return b.String()
}
