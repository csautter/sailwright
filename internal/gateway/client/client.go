// Package client connects agents to a sailwright-gateway on the same machine or
// through a mounted agent directory in a devcontainer. Explicit endpoint and token
// settings take precedence over discovery; mounted container credentials take
// precedence over the current user's local gateway.
package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Environment variables and defaults used for discovery.
const (
	EnvURL   = "SAILWRIGHT_GATEWAY_URL"
	EnvToken = "SAILWRIGHT_GATEWAY_TOKEN"
	EnvDir   = "SAILWRIGHT_GATEWAY_DIR"

	DefaultDir    = "/run/sailwright-gateway"
	DefaultTCPURL = "http://host.docker.internal:7391/mcp"
	LocalTCPURL   = "http://127.0.0.1:7391/mcp"
	UserDirName   = ".sailwright-gateway"

	socketName  = "gateway.sock"
	tokenName   = "token"
	socketURL   = "http://localhost/mcp"
	dialTimeout = 2 * time.Second
)

// Endpoint is a resolved gateway address plus token.
type Endpoint struct {
	// URL is the HTTP endpoint; for socket endpoints it is a placeholder host.
	URL string
	// Socket is the Unix socket path, if the gateway is reached over a socket.
	Socket string
	Token  string
	// Source explains how the endpoint was found.
	Source string
}

// String describes the endpoint without the token.
func (e Endpoint) String() string {
	if e.Socket != "" {
		return "unix://" + e.Socket
	}
	return e.URL
}

// Discover resolves the gateway from explicit settings, the container mount, or
// the current user's agent directory. Credentials always belong to the selected
// directory, so discovering a local gateway cannot replace a mounted host token.
func Discover(getenv func(string) string) (Endpoint, error) {
	home, _ := os.UserHomeDir()
	localDir := ""
	if home != "" {
		localDir = filepath.Join(home, UserDirName)
	}
	return discover(getenv, DefaultDir, localDir)
}

func discover(getenv func(string) string, mountedDir, localDir string) (Endpoint, error) {
	dir := strings.TrimSpace(getenv(EnvDir))
	fallback := DefaultTCPURL
	if dir == "" {
		dir = mountedDir
		// A mounted token remains authoritative even on Docker Desktop, where the
		// mounted Unix socket cannot accept connections across the Docker VM.
		if _, err := os.Stat(filepath.Join(mountedDir, tokenName)); os.IsNotExist(err) && localDir != "" {
			if _, err := os.Stat(filepath.Join(localDir, tokenName)); err == nil || socketReachable(filepath.Join(localDir, socketName)) {
				dir, fallback = localDir, LocalTCPURL
			}
		}
	}
	var endpoint Endpoint
	switch raw := strings.TrimSpace(getenv(EnvURL)); {
	case strings.HasPrefix(raw, "unix://"):
		endpoint = Endpoint{URL: socketURL, Socket: strings.TrimPrefix(raw, "unix://"), Source: EnvURL}
	case raw != "":
		endpoint = Endpoint{URL: raw, Source: EnvURL}
	case socketReachable(filepath.Join(dir, socketName)):
		endpoint = Endpoint{URL: socketURL, Socket: filepath.Join(dir, socketName), Source: "socket in " + dir}
	default:
		endpoint = Endpoint{URL: fallback, Source: "TCP fallback for " + dir}
	}
	endpoint.Token = strings.TrimSpace(getenv(EnvToken))
	if endpoint.Token == "" {
		// #nosec G304 -- fixed file name inside the selected operator-owned agent directory.
		data, err := os.ReadFile(filepath.Join(dir, tokenName))
		if err != nil {
			return Endpoint{}, fmt.Errorf("no gateway token: start a local gateway, mount its agent directory at %s, or set %s/%s (%v)", mountedDir, EnvDir, EnvToken, err)
		}
		endpoint.Token = strings.TrimSpace(string(data))
	}
	if endpoint.Token == "" {
		return Endpoint{}, errors.New("the gateway token is empty")
	}
	return endpoint, nil
}

func socketReachable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return false
	}
	conn, err := net.DialTimeout("unix", path, dialTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// HTTPClient returns an HTTP client that reaches the endpoint and sends the token.
func (e Endpoint) HTTPClient() *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	if e.Socket != "" {
		socket := e.Socket
		base.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socket)
		}
		base.Proxy = nil
	}
	return &http.Client{Transport: bearerTransport{token: e.Token, base: base}}
}

// Connect opens an MCP client session to the gateway.
func Connect(ctx context.Context, endpoint Endpoint, impl *mcp.Implementation) (*mcp.ClientSession, error) {
	client := mcp.NewClient(impl, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             endpoint.URL,
		HTTPClient:           endpoint.HTTPClient(),
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to gateway at %s (%s): %w", endpoint, endpoint.Source, err)
	}
	return session, nil
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(clone)
}
