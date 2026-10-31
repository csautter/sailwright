package gateway

import (
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

func (g *Gateway) serveTunnel(w http.ResponseWriter, r *http.Request) {
	g.backgroundMu.Lock()
	if g.closed {
		g.backgroundMu.Unlock()
		http.Error(w, "gateway is closing", http.StatusServiceUnavailable)
		return
	}
	g.background.Add(1)
	g.backgroundMu.Unlock()
	defer g.background.Done()
	requestCtx, cancelRequest := context.WithCancel(r.Context())
	defer cancelRequest()
	stopCancel := context.AfterFunc(g.backgroundCtx, cancelRequest)
	defer stopCancel()
	deny := func(status int, reason string) {
		_ = g.audit.Record(AuditEvent{Event: "tunnel_denied", Peer: r.Header.Get(peerHeader), HTTPStatus: status, Error: reason})
		http.Error(w, reason, status)
	}
	if r.Method != http.MethodGet || !strings.EqualFold(r.Header.Get("Upgrade"), "sailwright-tunnel") || !headerToken(r.Header.Get("Connection"), "upgrade") || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		deny(http.StatusBadRequest, "expected an HTTP tunnel upgrade without a body")
		return
	}
	query := r.URL.Query()
	for key, values := range query {
		if (key != "os" && key != "type" && key != "arch" && key != "port") || len(values) != 1 {
			deny(http.StatusBadRequest, "invalid tunnel parameters")
			return
		}
	}
	port, err := strconv.Atoi(query.Get("port"))
	if err != nil || (port != 22 && port != 5985 && port != 5986) {
		deny(http.StatusForbidden, "port is not allowed")
		return
	}
	select {
	case g.tunnelSlots <- struct{}{}:
		defer func() { <-g.tunnelSlots }()
	default:
		deny(http.StatusTooManyRequests, "tunnel limit reached")
		return
	}
	osName, typ, arch := query.Get("os"), query.Get("type"), query.Get("arch")
	if arch == "" {
		arch = "amd64"
	}
	if osName == "ubuntu" && typ == "" {
		typ = "server"
	}
	var vm ManagedVM
	if resolver, ok := g.vmResolver.(*CLIManagedVMResolver); ok {
		vm, err = resolver.resolve(requestCtx, osName, typ, arch, false)
	} else {
		vm, err = g.vmResolver.Resolve(requestCtx, osName, typ, arch)
	}
	if err != nil {
		deny(http.StatusNotFound, "managed VM is unavailable or not running")
		return
	}
	if err := allowedTunnelAddress(vm.Address); err != nil {
		deny(http.StatusForbidden, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(requestCtx, 10*time.Second)
	defer cancel()
	upstream, err := g.tunnelDial(ctx, "tcp", net.JoinHostPort(vm.Address.String(), strconv.Itoa(port)))
	if err != nil {
		deny(http.StatusBadGateway, "cannot connect to guest service")
		return
	}
	defer upstream.Close()
	params := map[string]any{"os": vm.OS, "type": vm.Type, "arch": vm.Arch, "address": vm.Address.String(), "port": port}
	risk := []string{}
	if vm.NetworkMode != "nat" {
		risk = append(risk, "LAN_OR_UNKNOWN_NETWORK")
	}
	if err := g.audit.Record(AuditEvent{Event: "tunnel_opened", Params: params, Risk: risk}); err != nil {
		http.Error(w, "audit unavailable", http.StatusServiceUnavailable)
		return
	}
	downstream, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		deny(http.StatusInternalServerError, "HTTP upgrade unavailable")
		return
	}
	defer downstream.Close()
	started := time.Now()
	var sent, received int64
	defer func() {
		params["bytes_to_vm"], params["bytes_from_vm"] = sent, received
		_ = g.audit.Record(AuditEvent{Event: "tunnel_closed", Params: params, DurationMS: time.Since(started).Milliseconds()})
	}()
	if _, err := buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: sailwright-tunnel\r\n\r\n"); err != nil {
		return
	}
	if err := buffered.Flush(); err != nil {
		return
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
	sent = <-counts
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
