package runner

import (
	"context"
	"fmt"
)

// defaultAvailabilityCommand decides which matrix targets this host offers.
const defaultAvailabilityCommand = "create"

// TargetPlan is one matrix entry and why it is skipped, if it is.
type TargetPlan struct {
	Target Target
	Skip   string
}

// MatrixOptions narrow a matrix run.
type MatrixOptions struct {
	// Filter runs only matrix targets that match it (SAILWRIGHT_E2E_TARGET).
	Filter *Target
	// IncludeUnstable also runs targets the CLI marks unstable, such as emulated
	// architectures (SAILWRIGHT_E2E_INCLUDE_UNSTABLE=1).
	IncludeUnstable bool
}

// PlanMatrix decides which matrix targets run on this host. It also returns targets
// of the scenario's covers command that the matrix does not contain.
func PlanMatrix(ctx context.Context, scenario Scenario, executor Executor, opts MatrixOptions) ([]TargetPlan, []ListedTarget, error) {
	command := scenario.Covers
	if command == "" {
		command = defaultAvailabilityCommand
	}
	listed, err := executor.List(ctx, command)
	if err != nil {
		return nil, nil, fmt.Errorf("list %s targets: %w", command, err)
	}

	var plans []TargetPlan
	for _, target := range scenario.Matrix() {
		if opts.Filter != nil && !sameTarget(*opts.Filter, target) {
			continue
		}
		plan := TargetPlan{Target: target}
		entry, found := FindTarget(listed, target)
		switch {
		case !found:
			plan.Skip = fmt.Sprintf("not offered by `%s list` on this host", command)
		case entry.Fields["status"] == "unstable" && !opts.IncludeUnstable:
			plan.Skip = "marked unstable on this host (e.g. emulated architecture); set " + EnvIncludeUnstable + "=1 to run it"
		}
		plans = append(plans, plan)
	}

	var uncovered []ListedTarget
	if scenario.Covers != "" {
		for _, entry := range listed {
			covered := false
			for _, target := range scenario.Matrix() {
				if _, ok := FindTarget([]ListedTarget{entry}, target); ok {
					covered = true
					break
				}
			}
			if !covered {
				uncovered = append(uncovered, entry)
			}
		}
	}
	return plans, uncovered, nil
}

func sameTarget(filter Target, target Target) bool {
	if filter.OS != target.OS || filter.Type != target.Type {
		return false
	}
	if filter.Arch != "" && filter.Arch != target.Arch {
		return false
	}
	return filter.Engine == "" || filter.Engine == target.Engine
}
