package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const agentSocketPermission = 0o600

// localSocketKey marks requests that arrived over the agent Unix socket.
type localSocketKey struct{}

func fromLocalSocket(ctx context.Context) bool {
	local, _ := ctx.Value(localSocketKey{}).(bool)
	return local
}

// Listeners are the endpoints the gateway serves on.
//
// The Unix socket in the agent dir is the simple, portable way for Linux containers
// (rootful or rootless Docker, Podman): the directory is bind-mounted read-only into
// the devcontainer. The TCP listener serves Docker Desktop on macOS and Windows,
// where host.docker.internal reaches the host's loopback.
type Listeners struct {
	TCP        net.Listener
	Socket     net.Listener
	SocketPath string
	// View serves the build live view on loopback for the human on the host.
	View    net.Listener
	ViewURL string
}

// Close closes all listeners.
func (l *Listeners) Close() {
	for _, listener := range []net.Listener{l.TCP, l.Socket, l.View} {
		if listener != nil {
			_ = listener.Close()
		}
	}
}

// Listen opens the TCP listener and, except on Windows, the agent Unix socket, and
// writes a copy of the token next to the socket.
func (g *Gateway) Listen() (*Listeners, error) {
	tcp, err := net.Listen("tcp", g.cfg.ListenAddr.String())
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w (the address must belong to a network interface of this host)", g.cfg.ListenAddr, err)
	}
	listeners := &Listeners{TCP: tcp}

	if err := ensurePrivateDir(g.cfg.AgentDir); err != nil {
		listeners.Close()
		return nil, fmt.Errorf("create agent dir: %w", err)
	}
	if err := writePrivateFileAtomic(filepath.Join(g.cfg.AgentDir, AgentTokenName), []byte(g.token+"\n")); err != nil {
		listeners.Close()
		return nil, fmt.Errorf("write agent token: %w", err)
	}
	if runtime.GOOS != "windows" {
		socket, socketPath, err := ListenAgentSocket(g.cfg.AgentDir)
		if err != nil {
			listeners.Close()
			return nil, err
		}
		listeners.Socket, listeners.SocketPath = socket, socketPath
	}
	if g.cfg.ViewAddr.IsValid() {
		view, err := net.Listen("tcp", g.cfg.ViewAddr.String())
		if err != nil {
			listeners.Close()
			return nil, fmt.Errorf("listen on %s for the live view: %w (set view_listen to another port or %q)", g.cfg.ViewAddr, err, disabledSetting)
		}
		listeners.View = view
		listeners.ViewURL = "http://" + view.Addr().String() + g.viewPath()
		g.jobs.SetViewURL(listeners.ViewURL)
	}
	return listeners, nil
}

// ListenAgentSocket creates <dir>/gateway.sock with owner-only permissions. A stale
// socket from a previous run is replaced; a live one is an error.
func ListenAgentSocket(dir string) (net.Listener, string, error) {
	path := filepath.Join(dir, AgentSocketName)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, "", fmt.Errorf("%s exists and is not a socket; remove it", path)
		}
		if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
			_ = conn.Close()
			return nil, "", fmt.Errorf("another gateway is already listening on %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, "", fmt.Errorf("remove stale socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, "", fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, agentSocketPermission); err != nil {
		_ = listener.Close()
		return nil, "", err
	}
	return listener, path, nil
}

// ServeListeners serves on every open listener until ctx is cancelled.
func (g *Gateway) ServeListeners(ctx context.Context, listeners *Listeners) error {
	handler := g.Handler()
	var servers []*http.Server
	errs := make(chan error, 3)

	serve := func(listener net.Listener, handler http.Handler, local bool) {
		server := &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
		}
		if local {
			server.ConnContext = func(ctx context.Context, _ net.Conn) context.Context {
				return context.WithValue(ctx, localSocketKey{}, true)
			}
		}
		servers = append(servers, server)
		go func() {
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- err
				return
			}
			errs <- nil
		}()
	}
	if listeners.TCP != nil {
		serve(listeners.TCP, handler, false)
	}
	if listeners.Socket != nil {
		serve(listeners.Socket, handler, true)
	}
	if listeners.View != nil {
		serve(listeners.View, g.ViewHandler(), false)
	}
	if len(servers) == 0 {
		return errors.New("no listeners to serve")
	}

	var firstErr error
	select {
	case <-ctx.Done():
	case firstErr = <-errs:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, server := range servers {
		_ = server.Shutdown(shutdownCtx)
	}
	return firstErr
}
