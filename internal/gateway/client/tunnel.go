package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// OpenTunnel upgrades an authenticated HTTP/1 connection to a raw guest stream.
func (e Endpoint) OpenTunnel(ctx context.Context, osName, typ, arch string, port int) (net.Conn, error) {
	if port != 22 && port != 5985 && port != 5986 {
		return nil, fmt.Errorf("port must be 22, 5985 or 5986")
	}
	endpoint, err := url.Parse(e.URL)
	if err != nil {
		return nil, err
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("unsupported gateway URL scheme")
	}
	endpoint.Path, endpoint.RawPath, endpoint.RawQuery, endpoint.Fragment = "/tunnel", "", "", ""
	values := url.Values{"os": {osName}, "type": {typ}, "arch": {arch}, "port": {strconv.Itoa(port)}}
	endpoint.RawQuery = values.Encode()
	var conn net.Conn
	dialer := net.Dialer{Timeout: 10 * time.Second}
	if e.Socket != "" {
		conn, err = dialer.DialContext(ctx, "unix", e.Socket)
	} else {
		host := endpoint.Host
		if endpoint.Port() == "" {
			port := "80"
			if endpoint.Scheme == "https" {
				port = "443"
			}
			host = net.JoinHostPort(endpoint.Hostname(), port)
		}
		conn, err = dialer.DialContext(ctx, "tcp", host)
		if err == nil && endpoint.Scheme == "https" {
			secure := tls.Client(conn, &tls.Config{ServerName: endpoint.Hostname(), MinVersion: tls.VersionTLS12})
			err = secure.HandshakeContext(ctx)
			if err == nil {
				conn = secure
			}
		}
	}
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, err
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	success := false
	defer func() {
		if !success {
			_ = conn.Close()
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(40 * time.Second))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.Token)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "sailwright-tunnel")
	if err := req.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusSwitchingProtocols || response.Header.Get("Upgrade") != "sailwright-tunnel" {
		if response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, fmt.Errorf("tunnel refused: HTTP %d", response.StatusCode)
	}
	_ = conn.SetDeadline(time.Time{})
	success = true
	return &bufferedTunnel{Conn: conn, reader: reader}, nil
}

type bufferedTunnel struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedTunnel) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c *bufferedTunnel) CloseWrite() error {
	if conn, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return nil
}

// CopyTunnel carries stdio or a local connection over a guest tunnel. It preserves
// half-close semantics so a client can finish writing while reading its response.
func CopyTunnel(ctx context.Context, tunnel net.Conn, input io.Reader, output io.Writer) error {
	defer tunnel.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = tunnel.Close()
		case <-done:
		}
	}()
	go func() {
		_, _ = io.Copy(tunnel, input)
		if conn, ok := tunnel.(interface{ CloseWrite() error }); ok {
			_ = conn.CloseWrite()
		}
	}()
	_, err := io.Copy(output, tunnel)
	return err
}
