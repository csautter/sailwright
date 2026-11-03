package cmd

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/csautter/dev-alchemy/pkg/provision"
	"github.com/spf13/cobra"
)

func init() {
	var jsonOutput, refresh bool
	var target string
	command := &cobra.Command{Use: "catalog", Short: "List tools (roles) and setups (playbooks) from your sources", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		entries, err := provision.Catalog(target, refresh)
		if err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
				SchemaVersion int                      `json:"schema_version"`
				Entries       []provision.CatalogEntry `json:"entries"`
			}{1, entries})
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "KIND\tNAME\tDESCRIPTION\tSOURCE\tPLATFORMS\tNOTES")
		for _, e := range entries {
			notes := ""
			if !e.Compatible {
				notes = "incompatible"
			}
			if len(e.Shadows) > 0 {
				notes += " shadows " + strings.Join(e.Shadows, ", ")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Kind, e.Name, e.Description, e.Source, strings.Join(e.Platforms, ", "), notes)
		}
		return w.Flush()
	}}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Print a JSON catalog")
	command.Flags().BoolVar(&refresh, "refresh", false, "Refresh Git sources before listing")
	command.Flags().StringVar(&target, "os", runtime.GOOS, "Target OS for platform compatibility")
	provisionCmd.AddCommand(command)
}
