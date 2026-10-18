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
	"github.com/csautter/dev-alchemy/internal/gateway/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	follow       bool
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

	root.AddCommand(newServeCommand(&stateDir), newTokenCommand(&stateDir), newConnectCommand())
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
	command.Flags().BoolVar(&flags.follow, "follow", false, "Mirror job output to this terminal (approval prompts still appear)")
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

func newConnectCommand() *cobra.Command {
	var check bool
	command := &cobra.Command{
		Use:   "connect",
		Short: "Bridge MCP over stdio to a running gateway (run inside the devcontainer)",
		Long: `Serves the gateway's tools over stdio so any agent harness can start it as an
MCP server. The gateway is found automatically:

  1. SAILWRIGHT_GATEWAY_URL (http://... or unix:///path/to/gateway.sock)
  2. the socket in SAILWRIGHT_GATEWAY_DIR (default /run/sailwright-gateway)
  3. http://host.docker.internal:7391/mcp

The token is read from SAILWRIGHT_GATEWAY_TOKEN or the token file in that directory.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint, err := client.Discover(os.Getenv)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if check {
				session, err := client.Connect(ctx, endpoint, &mcp.Implementation{Name: "sailwright-gateway-connect", Version: version})
				if err != nil {
					return err
				}
				defer session.Close()
				fmt.Fprintf(cmd.OutOrStdout(), "connected to %s (%s)\n", endpoint, endpoint.Source)
				return nil
			}
			bridge := &client.Bridge{Endpoint: endpoint, Version: version}
			return bridge.Run(ctx, &mcp.StdioTransport{})
		},
	}
	command.Flags().BoolVar(&check, "check", false, "Only test the connection and print where the gateway was found")
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
		Config:       cfg,
		Pinned:       pinned,
		Token:        token,
		Approver:     approver,
		Console:      os.Stdout,
		Version:      version,
		FollowOutput: flags.follow,
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

	// Bind before printing the banner so a failed bind is not reported as listening.
	listeners, err := g.Listen()
	if err != nil {
		return err
	}
	defer listeners.Close()
	printBanner(cfg, pinned, listeners.SocketPath, approver.Enabled())
	return g.ServeListeners(ctx, listeners)
}

func printBanner(cfg gateway.Config, pinned gateway.PinnedBinary, socketPath string, approvals bool) {
	approvalState := "disabled (safe set only)"
	if approvals {
		approvalState = "enabled on this terminal"
	}
	socket := "not available on this OS (use TCP)"
	if socketPath != "" {
		socket = socketPath
	}
	fmt.Printf(`
sailwright-gateway %s
  listening:  http://%s/mcp
  socket:     %s
  agent dir:  %s  (mount read-only into the devcontainer at /run/sailwright-gateway)
  binary:     %s (sha256 %s)
  data dir:   %s
  approvals:  %s
  token file: %s
  audit log:  %s

In the devcontainer, agents connect with: sailwright-gateway connect
Press Ctrl+C to stop.
`, version, cfg.ListenAddr, socket, cfg.AgentDir, pinned.Source, pinned.SHA256, cfg.DataDir, approvalState,
		gateway.TokenPath(cfg.StateDir), cfg.StateDir+string(os.PathSeparator)+"audit.jsonl")
}
