package gateway

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/csautter/dev-alchemy/internal/gateway/client"
)

type fixedVMResolver struct {
	address     netip.Addr
	protocol    string
	unavailable bool
}

func (r *fixedVMResolver) Resolve(context.Context, string, string, string) (ManagedVM, error) {
	if r.unavailable {
		return ManagedVM{}, ErrVMAccessUnavailable
	}
	protocol := r.protocol
	if protocol == "" {
		protocol = "ssh"
	}
	return ManagedVM{OS: "ubuntu", Type: "server", Arch: "amd64", Address: r.address, NetworkMode: "bridged", Protocol: protocol}, nil
}

func (r *fixedVMResolver) ResolveFresh(ctx context.Context, osName, osType, arch string) (ManagedVM, error) {
	return r.Resolve(ctx, osName, osType, arch)
}

func (r *fixedVMResolver) Identify(context.Context, string, string, string) (VMIdentity, error) {
	return VMIdentity{Exists: !r.unavailable}, nil
}

func tunnelFixture(t *testing.T) (*Gateway, client.Endpoint, *fixedVMResolver, string) {
	t.Helper()
	service, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	go func() {
		for {
			conn, err := service.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	audit, err := OpenAuditLog(auditPath, NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	resolver := &fixedVMResolver{address: netip.MustParseAddr("192.0.2.10")}
	catalog := NewTargetCatalog()
	catalog.Set(vmAccessListing, []Target{{OS: "ubuntu", Type: "server", Arch: "amd64", Engine: "qemu"}}, nil)
	g := &Gateway{cfg: Config{MaxTunnels: 1, TunnelIdleTimeoutValue: 300 * time.Millisecond, TunnelPrefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}}, catalog: catalog, vmResolver: resolver, audit: audit, backgroundCtx: ctx, backgroundCancel: cancel, tunnelSlots: make(chan struct{}, 1), token: "test-token"}
	// Substitute only the network dial: the resolver and production address checks
	// still run, while the fake guest service is reachable on loopback in tests.
	g.tunnelDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, service.Addr().String())
	}
	policy := SecurityPolicy{Token: g.token, AllowedHosts: []string{"127.0.0.1"}, SourcePrefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Audit: audit}
	server := httptest.NewServer(NewHTTPHandler(policy, http.NotFoundHandler(), http.HandlerFunc(g.serveTunnel)))
	t.Cleanup(func() { cancel(); server.Close(); g.background.Wait(); _ = audit.Close() })
	return g, client.Endpoint{URL: server.URL + "/mcp", Token: g.token}, resolver, auditPath
}

func TestTunnelCopiesLargePayloadAndAudits(t *testing.T) {
	g, endpoint, _, auditPath := tunnelFixture(t)
	conn, err := endpoint.OpenTunnel(context.Background(), "ubuntu", "server", "amd64", 22)
	if err != nil {
		t.Fatal(err)
	}
	input := bytes.Repeat([]byte("guest-stream\n"), 200000)
	var output bytes.Buffer
	if err := client.CopyTunnel(context.Background(), conn, bytes.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(input, output.Bytes()) {
		t.Fatalf("stream truncated: got %d, want %d", output.Len(), len(input))
	}
	g.background.Wait()
	audit, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tunnel_opened", "tunnel_closed", "bytes_to_vm", "bytes_from_vm", "LAN_OR_UNKNOWN_NETWORK", `"network_mode":"bridged"`} {
		if !strings.Contains(string(audit), want) {
			t.Fatalf("audit lacks %s: %s", want, audit)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(string(audit)), "\n") {
		if (strings.Contains(line, `"tunnel_opened"`) || strings.Contains(line, `"tunnel_closed"`)) && !strings.Contains(line, `"peer":"127.0.0.1:`) {
			t.Fatalf("tunnel event without peer: %s", line)
		}
	}
}

func TestTunnelRejectsUnauthorizedPortsAddressesAndStoppedVMs(t *testing.T) {
	_, endpoint, resolver, auditPath := tunnelFixture(t)
	for _, tc := range []struct {
		name, token, address string
		port                 int
		stopped              bool
		status               int
		protocol             string
	}{
		{"token", "bad", "192.0.2.10", 22, false, 401, ""},
		{"port", endpoint.Token, "192.0.2.10", 80, false, 403, ""},
		{"loopback", endpoint.Token, "127.0.0.1", 22, false, 403, ""},
		{"mapped-loopback", endpoint.Token, "::ffff:127.0.0.1", 22, false, 403, ""},
		{"link-local", endpoint.Token, "169.254.1.1", 22, false, 403, ""},
		{"multicast", endpoint.Token, "224.0.0.1", 22, false, 403, ""},
		{"unspecified", endpoint.Token, "0.0.0.0", 22, false, 403, ""},
		{"stopped", endpoint.Token, "192.0.2.10", 22, true, 404, ""},
		{"off-link", endpoint.Token, "198.51.100.7", 22, false, 403, ""},
		{"public", endpoint.Token, "8.8.8.8", 22, false, 403, ""},
		{"winrm-port-to-ssh-guest", endpoint.Token, "192.0.2.10", 5985, false, 403, "ssh"},
		{"ssh-port-to-winrm-guest", endpoint.Token, "192.0.2.10", 22, false, 403, "winrm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver.address, resolver.unavailable, resolver.protocol = netip.MustParseAddr(tc.address), tc.stopped, tc.protocol
			// Use raw HTTP here so the client-side port validation cannot mask a server bug.
			req, _ := http.NewRequest("GET", strings.TrimSuffix(endpoint.URL, "/mcp")+"/tunnel?os=ubuntu&port="+strconv.Itoa(tc.port), nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "sailwright-tunnel")
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("got %d, want %d", response.StatusCode, tc.status)
			}
		})
	}
	audit, _ := os.ReadFile(auditPath)
	if !strings.Contains(string(audit), "http_denied") || !strings.Contains(string(audit), "tunnel_denied") {
		t.Fatalf("missing denial audit: %s", audit)
	}
}

func TestTunnelLimitsIdleAndShutdown(t *testing.T) {
	g, endpoint, _, _ := tunnelFixture(t)
	conn, err := endpoint.OpenTunnel(context.Background(), "ubuntu", "server", "amd64", 22)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if other, err := endpoint.OpenTunnel(context.Background(), "ubuntu", "server", "amd64", 22); err == nil {
		other.Close()
		t.Fatal("connection limit ignored")
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("idle tunnel stayed open")
	}
	g.background.Wait()
	conn2, err := endpoint.OpenTunnel(context.Background(), "ubuntu", "server", "amd64", 22)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	g.backgroundCancel()
	_ = conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn2.Read(make([]byte, 1)); err == nil {
		t.Fatal("shutdown left tunnel open")
	}
	g.background.Wait()
}

func TestTunnelRejectsHostAddresses(t *testing.T) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range addresses {
		prefix, err := netip.ParsePrefix(value.String())
		if err == nil && allowedTunnelAddress(prefix.Addr(), nil) == nil {
			t.Fatalf("host address allowed: %s", prefix.Addr())
		}
	}
}

func TestTunnelAllowsWinRMPortsForWinRMGuests(t *testing.T) {
	g, endpoint, resolver, _ := tunnelFixture(t)
	resolver.protocol = "winrm"
	for _, port := range []int{5985, 5986} {
		conn, err := endpoint.OpenTunnel(context.Background(), "ubuntu", "server", "amd64", port)
		if err != nil {
			t.Fatalf("port %d: %v", port, err)
		}
		conn.Close()
		// The fixture allows one tunnel; wait until the gateway frees its slot.
		g.background.Wait()
	}
}

func TestTunnelAllowsAddressesOnHostSubnets(t *testing.T) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range addresses {
		prefix, err := netip.ParsePrefix(value.String())
		if err != nil || !prefix.Addr().Is4() || prefix.Addr().IsLoopback() || prefix.Bits() > 30 {
			continue
		}
		// Pick a neighbor of the host address inside the same subnet.
		neighbor := prefix.Addr().Next()
		if !prefix.Contains(neighbor) || !neighbor.IsGlobalUnicast() {
			neighbor = prefix.Addr().Prev()
		}
		if !prefix.Masked().Contains(neighbor) || neighbor == prefix.Masked().Addr() {
			continue
		}
		if err := allowedTunnelAddress(neighbor, nil); err != nil {
			// The neighbor may itself be another host address.
			if !strings.Contains(err.Error(), "belongs to the host") {
				t.Fatalf("on-link address %s refused: %v", neighbor, err)
			}
		}
		return
	}
	t.Skip("no IPv4 host subnet to test")
}

func TestTunnelAllowedCIDRsExtendOnLinkSubnets(t *testing.T) {
	address := netip.MustParseAddr("198.51.100.7")
	if err := allowedTunnelAddress(address, nil); err == nil {
		t.Skip("documentation network is on-link on this host")
	}
	if err := allowedTunnelAddress(address, []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}); err != nil {
		t.Fatal(err)
	}
}
