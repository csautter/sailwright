package cmd

import (
	"fmt"
	"os"

	"github.com/csautter/dev-alchemy/pkg/provision"

	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use: "sailwright",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		cmd.SilenceUsage = true
		path, origin := provision.ConfigLocation()
		if origin == "current directory" && (cmd.Name() == "provision" || cmd.Parent() == provisionCmd) {
			fmt.Fprintf(cmd.ErrOrStderr(), "Using source configuration from current directory: %s\n", path)
		}
	},
	Short: "Sailwright CLI - build self provisioning developer environments",
	Long:  "Sailwright is a developer tool that helps you to build self provisioning developer environments for multiple platforms with ease.",
	// Uncomment the following line if your bare application
	// has an action associated with it:
	// Run: func(cmd *cobra.Command, args []string) { },
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	installOperationLocks()
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	// rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.cmd.yaml)")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	//rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
