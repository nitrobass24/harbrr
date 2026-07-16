package http

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

// TrustedProxies reports whether ip is a configured trusted reverse-proxy peer. It is
// the shared shape internal/web/api's X-Forwarded-For gate and internal/web/torznabhttp's
// X-Forwarded-Proto gate both consume, so a CIDR-match bug can't diverge between them.
// A nil TrustedProxies trusts nothing (fail closed).
type TrustedProxies func(ip net.IP) bool

// ParseTrustedProxies parses a list of IPs/CIDRs (a bare IP becomes a host network) into
// a TrustedProxies check.
func ParseTrustedProxies(entries []string) (TrustedProxies, error) {
	nets, err := ParseCIDRs(entries)
	if err != nil {
		return nil, err
	}
	return func(ip net.IP) bool {
		for _, n := range nets {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}, nil
}

// ParseCIDRs parses a list of IPs or CIDRs into networks. A bare IP becomes a host
// network (/32 or /128). It is the single parse behind ParseTrustedProxies and
// internal/web/api's IP allowlist, so the bare-IP/trimming rules can't drift apart.
func ParseCIDRs(entries []string) ([]*net.IPNet, error) {
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
			e += "/" + strconv.Itoa(bits)
		}
		_, n, err := net.ParseCIDR(e)
		if err != nil {
			return nil, err //nolint:wrapcheck // surfaced verbatim at construction; callers add their own config-key context.
		}
		out = append(out, n)
	}
	return out, nil
}

// peerIP extracts the direct TCP peer's IP from r.RemoteAddr, or nil if unparseable.
func peerIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

// RequestScheme derives "https"/"http" for a request: TLS is always authoritative;
// otherwise X-Forwarded-Proto is honored ONLY when the direct peer is a configured
// trusted proxy (a nil trusted or an untrusted peer ignores the header, so an
// internet client can't forge https and fool a cookie/redirect decision).
func RequestScheme(r *http.Request, trusted TrustedProxies) string {
	if r.TLS != nil {
		return "https"
	}
	if r.Header.Get("X-Forwarded-Proto") != "https" {
		return "http"
	}
	if trusted == nil {
		return "http"
	}
	if peer := peerIP(r); peer != nil && trusted(peer) {
		return "https"
	}
	return "http"
}
