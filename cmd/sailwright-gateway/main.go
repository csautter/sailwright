// Command sailwright-gateway is the host-side gateway that lets an agent in a
// devcontainer drive a pinned sailwright binary over MCP. See docs/agent-gateway.md.
//
// The name follows the sailwright extension contract (sailwright-<name> on PATH), but
// the gateway runs standalone and does not depend on the extension interface.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

type serveFlags struct {
	stateDir     string
	configPath   string
	binary       string
	binarySHA256 string
	listen       string
	noApprovals  bool
}

func newRootCommand() *cobra.Command {
	var stateDir string
	root := &cobra.Command{
		Use:           "sailwright-gateway",
		Short:         "Host-side MCP gateway that lets agents drive a pinned sailwright binary",
		SilenceUsage:  true,
		SilenceErrors: false,
		Version:       version,
	}
	root.PersistentFlags().StringVar(&stateDir, "state-dir", "", "Gateway state directory (default: <user config dir>/sailwright-gateway)")

	root.AddCommand(newServeCommand(&stateDir), newTokenCommand(&stateDir))
	return root
}

func resolveStateDir(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	return gateway.DefaultStateDir()
}

func newServeCommand(stateDir *string) *cobra.Command {
	flags := serveFlags{}
	command := &cobra.Command{
		Use:   "serve",
		Short: "Pin a sailwright binary and serve the MCP gateway in the foreground",
		Long: `Pins the given sailwright binary, then serves MCP over HTTP.

Run it in a foreground terminal: requests outside the safe set are shown there and
need "approve <id>" from you. Agents cannot approve their own requests.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.stateDir = *stateDir
			return runServe(cmd.Context(), flags)
		},
	}
	command.Flags().StringVar(&flags.configPath, "config", "", "Config file (default: <state-dir>/gateway.yaml if present)")
	command.Flags().StringVar(&flags.binary, "binary", "", "sailwright binary to pin (overrides the config)")
	command.Flags().StringVar(&flags.binarySHA256, "binary-sha256", "", "Expected SHA-256 of the binary; skips the interactive confirmation")
	command.Flags().StringVar(&flags.listen, "listen", "", "Listen address, an IP literal with port (default 127.0.0.1:7391)")
	command.Flags().BoolVar(&flags.noApprovals, "no-approvals", false, "Disable the approval console; only the safe set is available")
	return command
}

func newTokenCommand(stateDir *string) *cobra.Command {
	var rotate bool
	command := &cobra.Command{
		Use:   "token",
		Short: "Print the bearer token agents must send (creates one if missing)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveStateDir(*stateDir)
			if err != nil {
				return err
			}
			var token string
			if rotate {
				token, err = gateway.RotateToken(dir)
			} else {
				token, err = gateway.LoadOrCreateToken(dir)
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), token)
			return nil
		},
	}
	command.Flags().BoolVar(&rotate, "rotate", false, "Replace the token with a new random one")
	return command
}

func runServe(parent context.Context, flags serveFlags) error {
	if parent == nil {
		parent = context.Background()
	}
	stateDir, err := resolveStateDir(flags.stateDir)
	if err != nil {
		return err
	}
	cfg, err := gateway.LoadConfig(stateDir, flags.configPath)
	if err != nil {
		return err
	}
	if flags.binary != "" {
		cfg.Binary = flags.binary
	}
	if flags.binarySHA256 != "" {
		cfg.BinarySHA256 = flags.binarySHA256
	}
	if flags.listen != "" {
		cfg.Listen = flags.listen
	}
	if err := cfg.Resolve(); err != nil {
		return err
	}

	token, err := gateway.LoadOrCreateToken(cfg.StateDir)
	if err != nil {
		return err
	}

	stdin := bufio.NewReader(os.Stdin)
	interactive := term.IsTerminal(int(os.Stdin.Fd())) // #nosec G115 -- file descriptors fit in int.

	pinned, err := gateway.PinBinary(cfg.Binary, cfg.StateDir, cfg.BinarySHA256)
	if err != nil {
		return err
	}
	if cfg.BinarySHA256 == "" {
		if !interactive {
			return errors.New("cannot confirm the binary without a terminal; pass --binary-sha256")
		}
		confirmed, err := gateway.ConfirmPin(pinned, stdin, os.Stdout)
		if err != nil {
			return err
		}
		if !confirmed {
			return errors.New("binary not pinned")
		}
	}

	var approver gateway.Approver = gateway.DisabledApprover{}
	switch {
	case flags.noApprovals:
	case !interactive:
		fmt.Println("stdin is not a terminal: the approval console is disabled, only the safe set is available")
	default:
		approver = gateway.NewConsoleApprover(stdin, os.Stdout, cfg.ApprovalTTLValue)
	}

	g, err := gateway.New(gateway.Options{
		Config:   cfg,
		Pinned:   pinned,
		Token:    token,
		Approver: approver,
		Console:  os.Stdout,
		Version:  version,
	})
	if err != nil {
		return err
	}
	defer g.Close()

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	refreshCtx, cancelRefresh := context.WithTimeout(ctx, 2*time.Minute)
	g.RefreshTargets(refreshCtx)
	cancelRefresh()

	printBanner(cfg, pinned, approver.Enabled())
	return g.Serve(ctx)
}

func printBanner(cfg gateway.Config, pinned gateway.PinnedBinary, approvals bool) {
	approvalState := "disabled (safe set only)"
	if approvals {
		approvalState = "enabled on this terminal"
	}
	fmt.Printf(`
sailwright-gateway %s
  listening:  http://%s/mcp
  binary:     %s (sha256 %s)
  data dir:   %s
  approvals:  %s
  token file: %s
  audit log:  %s

Agents need SAILWRIGHT_GATEWAY_TOKEN set to the content of the token file.
Press Ctrl+C to stop.
`, version, cfg.ListenAddr, pinned.Source, pinned.SHA256, cfg.DataDir, approvalState,
		gateway.TokenPath(cfg.StateDir), cfg.StateDir+string(os.PathSeparator)+"audit.jsonl")
}
