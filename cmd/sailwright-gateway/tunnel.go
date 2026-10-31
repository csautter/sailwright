package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/csautter/dev-alchemy/internal/gateway/client"
	"github.com/spf13/cobra"
)

func newVMCommand() *cobra.Command {
	var listen string
	vm := &cobra.Command{Use: "vm", Short: "Access a managed guest"}
	tunnel := &cobra.Command{Use: "tunnel <os>/<type>/<arch> <port>", Short: "Tunnel guest SSH or WinRM over stdio or a loopback listener", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			parts := strings.Split(args[0], "/")
			if len(parts) != 3 || parts[0] == "" || parts[2] == "" {
				return fmt.Errorf("target must be os/type/arch (use an empty type for Windows or macOS)")
			}
			port, err := strconv.Atoi(args[1])
			if err != nil || (port != 22 && port != 5985 && port != 5986) {
				return fmt.Errorf("port must be 22, 5985 or 5986")
			}
			endpoint, err := client.Discover(os.Getenv)
			if err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			open := func() (net.Conn, error) { return endpoint.OpenTunnel(ctx, parts[0], parts[1], parts[2], port) }
			if listen == "" {
				conn, err := open()
				if err != nil {
					return err
				}
				return client.CopyTunnel(ctx, conn, cmd.InOrStdin(), cmd.OutOrStdout())
			}
			address, err := net.ResolveTCPAddr("tcp", listen)
			if err != nil || address.IP == nil || !address.IP.IsLoopback() {
				return fmt.Errorf("--listen must be an explicit loopback IP and port")
			}
			listener, err := net.ListenTCP("tcp", address)
			if err != nil {
				return err
			}
			defer listener.Close()
			fmt.Fprintln(cmd.ErrOrStderr(), "Guest tunnel listening on", listener.Addr())
			go func() { <-ctx.Done(); _ = listener.Close() }()
			slots := make(chan struct{}, 16)
			for {
				local, err := listener.Accept()
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
				select {
				case slots <- struct{}{}:
				default:
					_ = local.Close()
					continue
				}
				go func() {
					defer func() { <-slots }()
					defer local.Close()
					remote, err := open()
					if err != nil {
						fmt.Fprintln(cmd.ErrOrStderr(), "Guest tunnel:", err)
						return
					}
					_ = client.CopyTunnel(ctx, remote, local, local)
				}()
			}
		},
	}
	tunnel.Flags().StringVar(&listen, "listen", "", "Listen on a loopback IP:port instead of using stdio")
	vm.AddCommand(tunnel)
	return vm
}
