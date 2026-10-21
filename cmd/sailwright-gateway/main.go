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
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
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

	root.AddCommand(newServeCommand(&stateDir), newTokenCommand(&stateDir), newCleanCommand(&stateDir), newConnectCommand())
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
	command.Flags().StringVar(&flags.listen, "listen", "", "Listen address, an IP literal with port, or \"off\" (default 127.0.0.1:7391; off on Linux)")
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
	cfg, err := loadConfig(flags.stateDir, flags.configPath, func(cfg *gateway.Config) {
		if flags.binary != "" {
			cfg.Binary = flags.binary
		}
		if flags.binarySHA256 != "" {
			cfg.BinarySHA256 = flags.binarySHA256
		}
		if flags.listen != "" {
			cfg.Listen = flags.listen
		}
	})
	if err != nil {
		return err
	}
	if legacy := cfg.LegacyDataDir(); legacy != "" {
		fmt.Printf("note: %s is no longer used (data dir is now %s); remove it to free disk space\n", legacy, cfg.DataDir)
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
		approver = gateway.NewConsoleApprover(stdin, os.Stdout, cfg.ApprovalTTLValue).
			HideInputWith(gateway.TerminalInputHider(int(os.Stdin.Fd()))) // #nosec G115 -- file descriptors fit in int.
	}

	// The shutdown question is read by the approval console when it runs, so the two
	// never compete for stdin.
	var confirmer gateway.Confirmer
	if console, ok := approver.(*gateway.ConsoleApprover); ok {
		confirmer = console
	} else if interactive {
		confirmer = gateway.LineConfirmer{In: stdin, Out: os.Stdout}
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
	closed := false
	defer func() {
		if !closed {
			_ = g.Close()
		}
	}()

	// Signals stay captured until runServe returns, so a second Ctrl+C while jobs are
	// cancelled does not leave their process trees behind.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var interrupted atomic.Bool
	go func() {
		select {
		case sig := <-signals:
			interrupted.Store(sig == os.Interrupt)
			cancel()
		case <-ctx.Done():
		}
	}()

	refreshCtx, cancelRefresh := context.WithTimeout(ctx, 2*time.Minute)
	g.RefreshTargets(refreshCtx)
	cancelRefresh()

	// Bind before printing the banner so a failed bind is not reported as listening.
	listeners, err := g.Listen()
	if err != nil {
		return err
	}
	defer listeners.Close()
	printBanner(cfg, pinned, listeners.SocketPath, listeners.ViewURL, approver.Enabled())
	serveErr := g.ServeListeners(ctx, listeners)
	listeners.Close()
	closed = true
	_ = g.Close()
	if serveErr == nil && interrupted.Load() && confirmer != nil {
		offerClean(cfg, confirmer, signals)
	}
	return serveErr
}

// loadConfig loads and resolves the gateway config; override runs before Resolve.
func loadConfig(stateDirFlag string, configPath string, override func(*gateway.Config)) (gateway.Config, error) {
	stateDir, err := resolveStateDir(stateDirFlag)
	if err != nil {
		return gateway.Config{}, err
	}
	cfg, err := gateway.LoadConfig(stateDir, configPath)
	if err != nil {
		return gateway.Config{}, err
	}
	if override != nil {
		override(&cfg)
	}
	// An explicit --state-dir keeps everything below it, so instances do not share data.
	if stateDirFlag == "" && cfg.DataDir == "" {
		if cfg.DataDir, err = gateway.DefaultDataDir(); err != nil {
			return gateway.Config{}, err
		}
	}
	if err := cfg.Resolve(); err != nil {
		return gateway.Config{}, err
	}
	return cfg, nil
}

// offerClean asks after a Ctrl+C shutdown whether to delete a large gateway cache.
// Another Ctrl+C, before or during the question, exits and keeps the cache.
func offerClean(cfg gateway.Config, confirmer gateway.Confirmer, signals <-chan os.Signal) {
	threshold := cfg.CleanPromptThreshold()
	if threshold == 0 {
		return
	}
	select {
	case <-signals:
		return
	default:
	}
	usage, err := gateway.MeasureCache(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot measure the gateway cache: %v\n", err)
		return
	}
	if usage.Total < threshold {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
		}
	}()
	fmt.Println()
	if !confirmer.Confirm(ctx, usage.CleanQuestion()) {
		fmt.Println("cache kept (\"sailwright-gateway clean\" deletes it later)")
		return
	}
	if err := clearCache(cfg, usage); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func clearCache(cfg gateway.Config, usage gateway.CacheUsage) error {
	if err := gateway.ClearCache(cfg); err != nil {
		return fmt.Errorf("the cache was only partly deleted: %w", err)
	}
	fmt.Printf("deleted %s of cache\n", gateway.FormatGB(usage.Total))
	return nil
}

func newCleanCommand(stateDir *string) *cobra.Command {
	var configPath string
	var yes bool
	command := &cobra.Command{
		Use:   "clean",
		Short: "Delete the gateway's download and build cache",
		Long: `Deletes the contents of the gateway's cache and Packer cache below its data dir.
Created VMs, their Vagrant and libvirt state and a shared_cache_dir are not touched.
The gateway must be stopped.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*stateDir, configPath, nil)
			if err != nil {
				return err
			}
			if cfg.ListenAddr.IsValid() {
				if conn, err := net.DialTimeout("tcp", cfg.ListenAddr.String(), time.Second); err == nil {
					_ = conn.Close()
					return fmt.Errorf("a gateway is listening on %s; stop it before cleaning", cfg.ListenAddr)
				}
			}
			if socket := filepath.Join(cfg.AgentDir, gateway.AgentSocketName); cfg.AgentDir != "" {
				if conn, err := net.DialTimeout("unix", socket, time.Second); err == nil {
					_ = conn.Close()
					return fmt.Errorf("a gateway is listening on %s; stop it before cleaning", socket)
				}
			}
			usage, err := gateway.MeasureCache(cfg)
			if err != nil {
				return err
			}
			if usage.Total == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "the gateway cache is empty")
				return nil
			}
			if !yes {
				if !term.IsTerminal(int(os.Stdin.Fd())) { // #nosec G115 -- file descriptors fit in int.
					return errors.New("cannot ask without a terminal; pass --yes")
				}
				ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
				defer stop()
				confirmer := gateway.LineConfirmer{In: bufio.NewReader(os.Stdin), Out: cmd.OutOrStdout()}
				if !confirmer.Confirm(ctx, usage.CleanQuestion()) {
					fmt.Fprintln(cmd.OutOrStdout(), "cache kept")
					return nil
				}
			}
			return clearCache(cfg, usage)
		},
	}
	command.Flags().StringVar(&configPath, "config", "", "Config file (default: <state-dir>/gateway.yaml if present)")
	command.Flags().BoolVar(&yes, "yes", false, "Delete without asking")
	return command
}

func printBanner(cfg gateway.Config, pinned gateway.PinnedBinary, socketPath string, viewURL string, approvals bool) {
	approvalState := "disabled (safe set only)"
	if approvals {
		approvalState = "enabled on this terminal"
	}
	socket := "not available on this OS (use TCP)"
	if socketPath != "" {
		socket = socketPath
	}
	tcpURL := "TCP off (agent socket only)"
	if cfg.ListenAddr.IsValid() {
		tcpURL = fmt.Sprintf("http://%s/mcp", cfg.ListenAddr)
	}
	liveView := "off"
	if viewURL != "" {
		liveView = viewURL + "  (build console, open in a browser on this host)"
	}
	fmt.Printf(`
sailwright-gateway %s
  listening:  %s
  socket:     %s
  agent dir:  %s  (mount read-only into the devcontainer at /run/sailwright-gateway)
  binary:     %s (sha256 %s)
  data dir:   %s
  approvals:  %s
  live view:  %s
  token file: %s
  audit log:  %s

In the devcontainer, agents connect with: sailwright-gateway connect
Press Ctrl+C to stop.
`, version, tcpURL, socket, cfg.AgentDir, pinned.Source, pinned.SHA256, cfg.DataDir, approvalState, liveView,
		gateway.TokenPath(cfg.StateDir), cfg.StateDir+string(os.PathSeparator)+"audit.jsonl")
}
