//go:build !windows

package gateway

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/csautter/dev-alchemy/internal/gateway/client"
)

func TestTunnelOverAgentSocket(t *testing.T) {
	g, _, _, _ := tunnelFixture(t)
	socket := filepath.Join(t.TempDir(), "gateway.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(SecurityPolicy{Token: g.token, Audit: g.audit}, http.NotFoundHandler(), http.HandlerFunc(g.serveTunnel))
	server := &http.Server{Handler: handler, ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
		return context.WithValue(ctx, localSocketKey{}, true)
	}}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	endpoint := client.Endpoint{URL: "http://localhost/mcp", Socket: socket, Token: g.token}
	conn, err := endpoint.OpenTunnel(context.Background(), "ubuntu", "server", "amd64", 22)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := client.CopyTunnel(context.Background(), conn, bytes.NewBufferString("socket guest bytes"), &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "socket guest bytes" {
		t.Fatal(output.String())
	}
}
