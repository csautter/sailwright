package cmd

import (
	"slices"
	"testing"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The gateway derives approval risk hints from raw argv without importing the CLI, so
// it keeps its own list of boolean flags. An unknown boolean flag swallows the next
// positional ("provision -K local" would no longer look like local provisioning).
// Cobra's own help and completion flags are skipped: cobra adds them lazily on
// Execute, so they would make this test depend on test order.
func TestGatewayRiskTreatsEveryBooleanFlagAsValueless(t *testing.T) {
	for _, command := range rootCmd.Commands() {
		if command.Name() == "completion" || command.Name() == "help" {
			continue
		}
		var walk func(*cobra.Command)
		walk = func(current *cobra.Command) {
			current.Flags().VisitAll(func(flag *pflag.Flag) {
				if flag.Value.Type() != "bool" || flag.Name == "help" {
					return
				}
				spellings := []string{"--" + flag.Name}
				if flag.Shorthand != "" {
					spellings = append(spellings, "-"+flag.Shorthand)
				}
				for _, spelling := range spellings {
					args := []string{command.Name(), spelling, "all"}
					if !slices.Contains(gateway.AssessRisk(args, ""), gateway.RiskAllTargets) {
						t.Errorf("gateway treats boolean flag %s of %q as taking a value; add it to isFlagValue in internal/gateway/risk.go", spelling, current.CommandPath())
					}
				}
			})
			for _, child := range current.Commands() {
				walk(child)
			}
		}
		walk(command)
	}
}
