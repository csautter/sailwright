package cmd

import (
	"encoding/json"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// State format and locking protocol are versioned independently of releases.
const (
	stateSchemaVersion = 1
	// lockProtocolVersion versions internal/operationlock's file layout and semantics.
	lockProtocolVersion = 1
)

func init() {
	rootCmd.Version = cliVersion()
	rootCmd.Flags().Bool("gateway-metadata", false, "Print state compatibility metadata")
	_ = rootCmd.Flags().MarkHidden("gateway-metadata")
	rootCmd.RunE = func(cmd *cobra.Command, _ []string) error {
		enabled, _ := cmd.Flags().GetBool("gateway-metadata")
		if !enabled {
			return cmd.Help()
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			SchemaVersion int    `json:"schema_version"`
			LockVersion   int    `json:"lock_version"`
			Version       string `json:"version"`
		}{stateSchemaVersion, lockProtocolVersion, cliVersion()})
	}
}

func cliVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	version := info.Main.Version
	if version == "" {
		// `go build file.go` records no module version.
		version = "(devel)"
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			version += "+" + setting.Value
		}
		if setting.Key == "vcs.modified" && setting.Value == "true" {
			version += ".dirty"
		}
	}
	return version
}
