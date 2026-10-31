package cmd

import (
	"github.com/csautter/dev-alchemy/internal/operationlock"
	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	"github.com/spf13/cobra"
)

func init() {
	for _, command := range []*cobra.Command{buildCmd, createCmd, startCmd, stopCmd, destroyCmd, provisionCmd, pullCmd, pushCmd} {
		run := command.RunE
		command.RunE = func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || args[0] == "local" {
				return run(cmd, args)
			}
			target := ""
			// Transfers can resolve their target from registry metadata; lock all targets.
			if args[0] != "all" && cmd != pullCmd && cmd != pushCmd {
				typ, _ := cmd.Flags().GetString("type")
				arch, _ := cmd.Flags().GetString("arch")
				if args[0] != "ubuntu" {
					typ = ""
				}
				target = args[0] + "/" + typ + "/" + arch
			}
			release, err := operationlock.Acquire(alchemy_build.GetDirectoriesInstance().AppDataDir, target)
			if err != nil {
				return err
			}
			defer release()
			return run(cmd, args)
		}
	}
}
