package auth

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientAddr returns the request's remote IP, preferring the first entry
// in X-Forwarded-For when running behind a proxy (Dokku's nginx sets it).
// Returns nil when nothing parseable is available. Used for audit rows
// and per-IP rate limits; never for authorization.
func ClientAddr(r *http.Request) *netip.Addr {
	if r == nil {
		return nil
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
		if addr, err := netip.ParseAddr(first); err == nil {
			return &addr
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return &addr
	}
	return nil
}

// ClientIP is ClientAddr as a string, "" when unknown.
func ClientIP(r *http.Request) string {
	if a := ClientAddr(r); a != nil {
		return a.String()
	}
	return ""
}
