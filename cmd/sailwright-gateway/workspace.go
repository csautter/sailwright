package main

import (
	"fmt"
	"path"
	"strings"
	"text/tabwriter"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/spf13/cobra"
)

// newWorkspaceCommand manages the registered agent workspaces. They live in
// <state-dir>/agent_workspaces.json next to gateway.yaml, which is never rewritten; a
// running gateway picks changes up without a restart.
func newWorkspaceCommand(stateDir *string) *cobra.Command {
	var configPath string
	command := &cobra.Command{
		Use:   "workspace",
		Short: "List, add or remove agent workspaces without editing gateway.yaml",
		Long: `Agent workspaces map a directory an agent sees (inside its devcontainer) to the
host directory that backs it, so run_cli can snapshot files from it (bundle_root).

Workspaces come from agent_workspaces in gateway.yaml and from <state-dir>/` + "agent_workspaces.json" + `,
which this command and approved workspace_register calls write. A running gateway picks
changes up without a restart.`,
	}
	command.PersistentFlags().StringVar(&configPath, "config", "", "Config file (default: <state-dir>/gateway.yaml if present)")

	list := &cobra.Command{
		Use:   "list",
		Short: "Show all agent workspaces and where they are configured",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*stateDir, configPath, nil)
			if err != nil {
				return err
			}
			registered, err := gateway.LoadRegisteredWorkspaces(cfg)
			if err != nil {
				return err
			}
			out := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(out, "AGENT PATH\tHOST PATH\tSOURCE")
			for _, mapping := range cfg.AgentWorkspaces {
				fmt.Fprintf(out, "%s\t%s\tgateway.yaml\n", mapping.Agent, mapping.Host)
			}
			for _, workspace := range registered {
				fmt.Fprintf(out, "%s\t%s\tregistered %s\n", workspace.Agent, workspace.Host, workspace.RegisteredAt.Local().Format("2006-01-02 15:04"))
			}
			return out.Flush()
		},
	}
	add := &cobra.Command{
		Use:   "add <agent-path> <host-path>",
		Short: "Register an agent workspace",
		Example: `  sailwright-gateway workspace add /workspaces/my-roles /home/me/Projects/my-roles
  sailwright-gateway workspace add /workspaces/my-roles 'C:\Users\me\Projects\my-roles'`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*stateDir, configPath, nil)
			if err != nil {
				return err
			}
			workspace, err := gateway.AddRegisteredWorkspace(cfg, gateway.WorkspaceMapping{Agent: args[0], Host: args[1]}, "")
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "registered %s -> %s\n", workspace.Agent, workspace.Host)
			return nil
		},
	}
	remove := &cobra.Command{
		Use:   "remove <agent-path>",
		Short: "Remove a registered agent workspace (gateway.yaml entries stay)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*stateDir, configPath, nil)
			if err != nil {
				return err
			}
			removed, err := gateway.RemoveRegisteredWorkspace(cfg, args[0])
			if err != nil {
				return err
			}
			agentPath := path.Clean(strings.TrimSpace(args[0]))
			if !removed {
				for _, mapping := range cfg.AgentWorkspaces {
					if mapping.Agent == agentPath {
						return fmt.Errorf("%s is configured in gateway.yaml; remove it there", agentPath)
					}
				}
				return fmt.Errorf("no registered workspace %s", agentPath)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", agentPath)
			return nil
		},
	}
	command.AddCommand(list, add, remove)
	return command
}
