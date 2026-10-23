// Package client finds and connects to a sailwright-gateway from inside a container.
//
// Discovery order:
//  1. SAILWRIGHT_GATEWAY_URL, either http(s)://host:port/mcp or unix:///path/to/gateway.sock
//  2. the agent socket in SAILWRIGHT_GATEWAY_DIR (default /run/sailwright-gateway), if
//     it accepts connections (Linux containers with the agent dir bind-mounted)
//  3. http://host.docker.internal:7391/mcp (Docker Desktop on macOS and Windows)
//
// The token comes from SAILWRIGHT_GATEWAY_TOKEN or <dir>/token.
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

// Discover resolves the gateway endpoint from the environment and the mounted agent dir.
func Discover(getenv func(string) string) (Endpoint, error) {
	dir := strings.TrimSpace(getenv(EnvDir))
	if dir == "" {
		dir = DefaultDir
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
		endpoint = Endpoint{URL: DefaultTCPURL, Source: "default TCP address"}
	}

	endpoint.Token = strings.TrimSpace(getenv(EnvToken))
	if endpoint.Token == "" {
		// #nosec G304 -- fixed file name inside the configured gateway dir.
		data, err := os.ReadFile(filepath.Join(dir, tokenName))
		if err != nil {
			return Endpoint{}, fmt.Errorf("no gateway token: set %s or mount the gateway agent dir at %s (%v)", EnvToken, dir, err)
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
