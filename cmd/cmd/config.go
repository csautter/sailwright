package cmd

import (
	"fmt"

	"github.com/csautter/dev-alchemy/pkg/provision"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.PersistentFlags().StringVar(&provision.ConfigOverride, "config", "", "Path to ansible-role-sources.yml (overrides environment and current directory)")
	config := &cobra.Command{Use: "config", Short: "Manage role and playbook sources", PersistentPreRun: func(cmd *cobra.Command, args []string) { cmd.SilenceUsage = true }}
	path := &cobra.Command{Use: "path", Short: "Show the effective source configuration path and origin", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		path, origin := provision.ConfigLocation()
		fmt.Fprintf(cmd.OutOrStdout(), "%s (%s)\n", path, origin)
		return nil
	}}
	sources := &cobra.Command{Use: "sources", Short: "Manage local and Git sources"}
	list := &cobra.Command{Use: "list", Short: "List configured sources in precedence order", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		roles, playbooks, err := provision.ConfigSources()
		if err != nil {
			return err
		}
		for _, s := range roles {
			kind := "roles"
			if s.PlaybooksPath != "" {
				kind = "roles, playbooks"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s%s\t%s\n", s.Name, kind, s.Path, s.URL, s.Ref)
		}
		for _, s := range playbooks {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\tplaybooks\t%s%s\t%s\n", s.Name, s.Path, s.URL, s.Ref)
		}
		return nil
	}}
	var name, ref string
	add := &cobra.Command{Use: "add <folder|git-url>", Short: "Detect and add role and playbook roots", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return provision.AddConfigSource(args[0], name, ref) }}
	add.Flags().StringVar(&name, "name", "", "Unique source name (defaults to folder or repository name)")
	add.Flags().StringVar(&ref, "ref", "", "Git branch, tag, or commit")
	remove := &cobra.Command{Use: "remove <name>", Short: "Remove a named source without deleting its files", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return provision.RemoveConfigSource(args[0]) }}
	sources.AddCommand(add, list, remove)
	config.AddCommand(path, sources)
	rootCmd.AddCommand(config)
}
