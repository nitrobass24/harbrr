package api

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// authMethod records how a request was authenticated, stored in the request
// context by resolveAuth so handlers (e.g. /me, logout) can adapt.
type authMethod int

const (
	authNone authMethod = iota
	authSession
	authAPIKey
	authDisabled // auth disabled + IP allowlisted (synthetic admin)
)

type ctxKey int

const authMethodCtxKey ctxKey = iota

// session keys.
const (
	sessionAuthenticated = "authenticated"
	sessionUsername      = "username"
)

// authMethodFrom returns the request's resolved auth method.
func authMethodFrom(ctx context.Context) authMethod {
	m, _ := ctx.Value(authMethodCtxKey).(authMethod)
	return m
}

// resolveAuth determines the request's auth method and stores it in the context.
// Order: X-API-Key (programmatic) → SCS session (browser) → auth-disabled mode
// gated by the IP allowlist.
func (rt *router) resolveAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), authMethodCtxKey, rt.detectAuth(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// detectAuth resolves the auth method for a request.
func (rt *router) detectAuth(r *http.Request) authMethod {
	if key := r.Header.Get("X-API-Key"); key != "" {
		if _, err := rt.auth.ValidateAPIKey(r.Context(), key); err == nil {
			return authAPIKey
		}
	}
	if rt.sessions.GetBool(r.Context(), sessionAuthenticated) {
		return authSession
	}
	if rt.cfg.AuthDisabled && rt.ipAllowed(r) {
		return authDisabled
	}
	return authNone
}

// requireAuth rejects unauthenticated requests with 401.
func (rt *router) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authMethodFrom(r.Context()) == authNone {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ipAllowed reports whether the request's client IP is in the allowlist.
func (rt *router) ipAllowed(r *http.Request) bool {
	ip := rt.clientIP(r)
	if ip == nil {
		return false
	}
	for _, n := range rt.allowlist {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP returns the request's client IP. X-Forwarded-For is consulted only
// when the direct TCP peer is a configured trusted proxy; the real client is then
// the RIGHTMOST entry that is not itself a trusted proxy. Each proxy in the chain
// APPENDS the address it received from, so the genuine hops sit at the right while
// a client-spoofed value sits at the left, never reached. Taking the leftmost
// entry (the old behavior) let any client forge an allowlisted IP and bypass the
// auth.mode=disabled allowlist.
func (rt *router) clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer == nil || !rt.isTrustedProxy(peer) {
		return peer
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return peer
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		hop := net.ParseIP(strings.TrimSpace(parts[i]))
		if hop == nil || rt.isTrustedProxy(hop) {
			continue
		}
		return hop
	}
	// Every forwarded hop was a trusted proxy (or unparseable); fall back to the
	// direct peer rather than trusting a client-controlled value.
	return peer
}

// isTrustedProxy reports whether ip is a configured trusted proxy.
func (rt *router) isTrustedProxy(ip net.IP) bool {
	for _, n := range rt.trustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// parseCIDRs parses a list of IPs or CIDRs into networks. A bare IP becomes a
// host network (/32 or /128).
func parseCIDRs(entries []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(entries))
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.Contains(e, "/") {
			ip := net.ParseIP(e)
			if ip == nil {
				return nil, &net.ParseError{Type: "IP address", Text: e}
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			e += "/" + itoa(bits)
		}
		_, n, err := net.ParseCIDR(e)
		if err != nil {
			return nil, err //nolint:wrapcheck // surfaced verbatim at construction.
		}
		out = append(out, n)
	}
	return out, nil
}

// itoa avoids strconv for a tiny two-value case (32/128).
func itoa(n int) string {
	if n == 32 {
		return "32"
	}
	return "128"
}
