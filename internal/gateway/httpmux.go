package gateway

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

const (
	// peerHeader carries the verified remote address to tool handlers. Any value sent
	// by the client is removed first.
	peerHeader          = "X-Sailwright-Gateway-Peer"
	maxRequestBodyBytes = 1 << 20
	mcpPath             = "/mcp"
)

// SecurityPolicy holds the request checks shared by every endpoint (/mcp now, /tunnel
// later).
type SecurityPolicy struct {
	Token          string
	SourcePrefixes []netip.Prefix
	AllowedHosts   []string
	AllowedOrigins []string
	Audit          *AuditLog
}

// NewHTTPHandler wraps the MCP handler with the security middleware and routes it.
func NewHTTPHandler(policy SecurityPolicy, mcpHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(mcpPath, mcpHandler)
	return policy.Middleware(mux)
}

// Middleware enforces source address, Host, Origin and bearer token checks in that
// order and fails closed. Requests over the agent Unix socket skip the network checks.
func (p SecurityPolicy) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(peerHeader)
		peer := r.RemoteAddr

		// Access to the agent socket is controlled by file permissions and by which
		// container gets the mount; network source and DNS rebinding checks do not
		// apply to it. Origin and token are checked for every transport.
		if fromLocalSocket(r.Context()) {
			peer = "unix-socket"
		} else {
			if !p.sourceAllowed(r.RemoteAddr) {
				p.deny(w, r, http.StatusForbidden, "source address not allowed")
				return
			}
			if !p.hostAllowed(r.Host) {
				p.deny(w, r, http.StatusForbidden, "host header not allowed")
				return
			}
		}
		if origin := r.Header.Get("Origin"); origin != "" && !containsFold(p.AllowedOrigins, origin) {
			p.deny(w, r, http.StatusForbidden, "origin not allowed")
			return
		}
		if !p.tokenValid(r.Header.Get("Authorization")) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="sailwright-gateway"`)
			p.deny(w, r, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		r.Header.Set(peerHeader, peer)
		next.ServeHTTP(w, r)
	})
}

func (p SecurityPolicy) deny(w http.ResponseWriter, r *http.Request, status int, reason string) {
	_ = p.Audit.Record(AuditEvent{Event: "http_denied", Peer: r.RemoteAddr, HTTPStatus: status, Error: reason})
	http.Error(w, http.StatusText(status), status)
}

func (p SecurityPolicy) sourceAllowed(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range p.SourcePrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (p SecurityPolicy) hostAllowed(hostHeader string) bool {
	host := hostOnly(hostHeader)
	return host != "" && containsFold(p.AllowedHosts, host)
}

func (p SecurityPolicy) tokenValid(header string) bool {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) || p.Token == "" {
		return false
	}
	presented := strings.TrimSpace(header[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(presented), []byte(p.Token)) == 1
}

func containsFold(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return true
		}
	}
	return false
}
