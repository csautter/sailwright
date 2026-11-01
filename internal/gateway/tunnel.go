package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

func allowedTunnelAddress(address netip.Addr) error {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.Zone() != "" {
		return fmt.Errorf("VM address is not an allowed unicast guest address")
	}
	local, err := net.InterfaceAddrs()
	if err != nil {
		return fmt.Errorf("cannot check host addresses")
	}
	for _, value := range local {
		prefix, err := netip.ParsePrefix(value.String())
		if err != nil {
			return fmt.Errorf("cannot parse host interface address")
		}
		if prefix.Addr().Unmap() == address {
			return fmt.Errorf("VM address belongs to the host")
		}
	}
	return nil
}

// tunnelPorts are the guest services a tunnel may reach.
var tunnelPorts = map[int]bool{22: true, 5985: true, 5986: true}

// tunnelDialTimeout bounds the connection to the guest service.
const tunnelDialTimeout = 10 * time.Second

// tunnelRefusal is an HTTP status with the reason shown to the agent and audited.
type tunnelRefusal struct {
	status int
	reason string
}

// serveTunnel checks the request, connects to the guest and relays bytes until
// either side closes, the stream is idle or the gateway shuts down.
func (g *Gateway) serveTunnel(w http.ResponseWriter, r *http.Request) {
	if !g.enterBackground() {
		http.Error(w, "gateway is closing", http.StatusServiceUnavailable)
		return
	}
	defer g.background.Done()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer context.AfterFunc(g.backgroundCtx, cancel)()
	deny := func(refusal tunnelRefusal) {
		_ = g.audit.Record(AuditEvent{Event: "tunnel_denied", Peer: r.Header.Get(peerHeader), HTTPStatus: refusal.status, Error: refusal.reason})
		http.Error(w, refusal.reason, refusal.status)
	}

	target, port, refusal := g.parseTunnelRequest(r)
	if refusal != nil {
		deny(*refusal)
		return
	}
	select {
	case g.tunnelSlots <- struct{}{}:
		defer func() { <-g.tunnelSlots }()
	default:
		deny(tunnelRefusal{http.StatusTooManyRequests, "tunnel limit reached"})
		return
	}
	vm, refusal := g.authorizeTunnel(ctx, target)
	if refusal != nil {
		deny(*refusal)
		return
	}
	dialCtx, cancelDial := context.WithTimeout(ctx, tunnelDialTimeout)
	upstream, err := g.tunnelDial(dialCtx, "tcp", net.JoinHostPort(vm.Address.String(), strconv.Itoa(port)))
	cancelDial()
	if err != nil {
		deny(tunnelRefusal{http.StatusBadGateway, "cannot connect to guest service"})
		return
	}
	defer upstream.Close()

	peer := r.Header.Get(peerHeader)
	params := map[string]any{"os": vm.OS, "type": vm.Type, "arch": vm.Arch, "engine": vm.Engine, "network_mode": vm.NetworkMode, "address": vm.Address.String(), "port": port}
	risk := []string{}
	if lanReachable(vm.NetworkMode) {
		risk = append(risk, "LAN_OR_UNKNOWN_NETWORK")
	}
	if err := g.audit.Record(AuditEvent{Event: "tunnel_opened", Peer: peer, Params: params, Risk: risk}); err != nil {
		http.Error(w, "audit unavailable", http.StatusServiceUnavailable)
		return
	}
	// Every recorded tunnel_opened gets a tunnel_closed, also when the upgrade fails.
	started := time.Now()
	closeErr := ""
	defer func() {
		_ = g.audit.Record(AuditEvent{Event: "tunnel_closed", Peer: peer, Params: params, Error: closeErr, DurationMS: time.Since(started).Milliseconds()})
	}()
	downstream, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		closeErr = "HTTP upgrade unavailable"
		http.Error(w, closeErr, http.StatusInternalServerError)
		return
	}
	defer downstream.Close()
	sent, received := g.relayTunnel(downstream, buffered, upstream)
	params["bytes_to_vm"], params["bytes_from_vm"] = sent, received
}

// enterBackground registers work that Close waits for; false once closing.
func (g *Gateway) enterBackground() bool {
	g.backgroundMu.Lock()
	defer g.backgroundMu.Unlock()
	if g.closed {
		return false
	}
	g.background.Add(1)
	return true
}

// parseTunnelRequest checks the upgrade shape and parameters. The target is not
// trusted beyond naming a listed VM; the address always comes from the pinned CLI.
func (g *Gateway) parseTunnelRequest(r *http.Request) (VMTarget, int, *tunnelRefusal) {
	if r.Method != http.MethodGet || !strings.EqualFold(r.Header.Get("Upgrade"), "sailwright-tunnel") || !headerToken(r.Header.Get("Connection"), "upgrade") || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		return VMTarget{}, 0, &tunnelRefusal{http.StatusBadRequest, "expected an HTTP tunnel upgrade without a body"}
	}
	query := r.URL.Query()
	for key, values := range query {
		if (key != "os" && key != "type" && key != "arch" && key != "port") || len(values) != 1 {
			return VMTarget{}, 0, &tunnelRefusal{http.StatusBadRequest, "invalid tunnel parameters"}
		}
	}
	port, err := strconv.Atoi(query.Get("port"))
	if err != nil || !tunnelPorts[port] {
		return VMTarget{}, 0, &tunnelRefusal{http.StatusForbidden, "port is not allowed"}
	}
	target, err := g.grammar().ResolveVMTarget(vmAccessListing, query.Get("os"), query.Get("type"), query.Get("arch"), "")
	if err != nil {
		return VMTarget{}, 0, &tunnelRefusal{http.StatusBadRequest, err.Error()}
	}
	return target, port, nil
}

// authorizeTunnel decides whether the agent may reach the VM now and returns its
// current address.
func (g *Gateway) authorizeTunnel(ctx context.Context, target VMTarget) (ManagedVM, *tunnelRefusal) {
	if !g.ownership.allowed(target.Key()) {
		return ManagedVM{}, &tunnelRefusal{http.StatusForbidden, "VM was not created by this gateway; call vm_info to request operator approval"}
	}
	vm, err := g.vmResolver.ResolveFresh(ctx, target.OS, target.Type, target.Arch)
	if err != nil {
		// The detailed reason goes to the operator console (see CLIManagedVMResolver).
		return ManagedVM{}, &tunnelRefusal{http.StatusNotFound, "managed VM is unavailable or not running"}
	}
	allowed, err := g.ownership.confirm(target.Key(), vm.InstanceID)
	g.recordOwnership(err)
	if !allowed {
		return ManagedVM{}, &tunnelRefusal{http.StatusForbidden, "VM was recreated outside this gateway; call vm_info to request operator approval"}
	}
	if err := allowedTunnelAddress(vm.Address); err != nil {
		return ManagedVM{}, &tunnelRefusal{http.StatusForbidden, err.Error()}
	}
	return vm, nil
}

// lanReachable reports network modes in which the guest may sit on the LAN.
func lanReachable(mode string) bool {
	return mode != "nat" && mode != "user"
}

// relayTunnel completes the upgrade and copies bytes in both directions. Activity
// in either direction extends the idle deadline; gateway shutdown closes both ends.
func (g *Gateway) relayTunnel(downstream net.Conn, buffered *bufio.ReadWriter, upstream net.Conn) (sent, received int64) {
	if _, err := buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: sailwright-tunnel\r\n\r\n"); err != nil {
		return 0, 0
	}
	if err := buffered.Flush(); err != nil {
		return 0, 0
	}
	touch := func() {
		deadline := time.Now().Add(g.cfg.TunnelIdleTimeoutValue)
		_ = upstream.SetDeadline(deadline)
		_ = downstream.SetDeadline(deadline)
	}
	touch()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-g.backgroundCtx.Done():
			_ = downstream.Close()
			_ = upstream.Close()
		case <-done:
		}
	}()
	counts := make(chan int64, 1)
	go func() {
		n, _ := io.Copy(&activityWriter{Writer: upstream, touch: touch}, buffered.Reader)
		if c, ok := upstream.(interface{ CloseWrite() error }); ok {
			_ = c.CloseWrite()
		}
		counts <- n
	}()
	received, _ = io.Copy(&activityWriter{Writer: downstream, touch: touch}, upstream)
	// Once the guest closes output there is no useful remaining request stream.
	_ = downstream.Close()
	_ = upstream.Close()
	return <-counts, received
}

type activityWriter struct {
	io.Writer
	touch func()
}

func (w *activityWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if n > 0 {
		w.touch()
	}
	return n, err
}

func headerToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}
