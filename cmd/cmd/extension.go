package cmd

import (
	"fmt"
	"io"
	"text/tabwriter"

	alchemy_extension "github.com/csautter/dev-alchemy/pkg/extension"
	"github.com/spf13/cobra"
)

var (
	extensionDiscoverFunc = alchemy_extension.Discover
	extensionRunFunc      = alchemy_extension.Run
)

var extensionCmd = &cobra.Command{
	Use:   "extension",
	Short: "Discover and run external Dev Alchemy extensions",
	Long: `Discover and run external Dev Alchemy extensions.

Extensions are executable files on PATH named alchemy-<name>. They run as
separate processes so private extensions can integrate through a stable command
and JSON file contract without linking into the open Dev Alchemy binary.`,
}

var extensionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available external extensions",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		extensions, err := extensionDiscoverFunc(alchemy_extension.DiscoverOptions{})
		if err != nil {
			return err
		}
		return printAvailableExtensions(cmd.OutOrStdout(), extensions)
	},
}

var extensionRunCmd = &cobra.Command{
	Use:   "run <name> [-- <extension args...>]",
	Short: "Run an external extension executable",
	Long: `Run an external extension executable.

The extension name resolves to alchemy-<name> on PATH. Pass extension flags
after -- so Dev Alchemy does not parse them.`,
	Example: `  alchemy extension run analyzer -- scan --out snapshot.json
  alchemy extension run analyzer -- generate --from snapshot.json --out generated-ansible`,
	Args: validateExtensionRunArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, extensionArgs := splitExtensionRunArgs(cmd, args)
		return extensionRunFunc(cmd.Context(), alchemy_extension.RunOptions{
			Name:   name,
			Args:   extensionArgs,
			Stdin:  cmd.InOrStdin(),
			Stdout: cmd.OutOrStdout(),
			Stderr: cmd.ErrOrStderr(),
		})
	},
}

func printAvailableExtensions(writer io.Writer, extensions []alchemy_extension.Executable) error {
	if len(extensions) == 0 {
		_, err := fmt.Fprintln(writer, "No Dev Alchemy extensions found on PATH. Install an executable named alchemy-<name> to add one.")
		return err
	}

	tw := tabwriter.NewWriter(writer, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "NAME\tEXECUTABLE"); err != nil {
		return err
	}
	for _, extension := range extensions {
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", extension.Name, extension.Path); err != nil {
			return err
		}
	}

	return tw.Flush()
}

func validateExtensionRunArgs(cmd *cobra.Command, args []string) error {
	positionalArgCount := len(args)
	if dashIndex := cmd.ArgsLenAtDash(); dashIndex >= 0 {
		positionalArgCount = dashIndex
	}
	if positionalArgCount < 1 {
		return fmt.Errorf("accepts at least 1 arg(s), received %d", positionalArgCount)
	}

	return nil
}

func splitExtensionRunArgs(cmd *cobra.Command, args []string) (string, []string) {
	if dashIndex := cmd.ArgsLenAtDash(); dashIndex >= 0 {
		return args[0], args[dashIndex:]
	}

	if len(args) == 1 {
		return args[0], nil
	}
	return args[0], args[1:]
}

func init() {
	rootCmd.AddCommand(extensionCmd)
	extensionCmd.AddCommand(extensionListCmd)
	extensionCmd.AddCommand(extensionRunCmd)
}
