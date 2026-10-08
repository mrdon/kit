package auth

import (
	"net/http"
	"net/url"
	"strings"
)

// CSRFHeader is the custom header every state-changing browser request
// carries. The session cookie is SameSite=Lax, so a top-level cross-site
// navigation still sends it; the defence is that a cross-origin page can't
// add a custom header (or a JSON content type) without a CORS preflight,
// and no other origin is allowed through preflight.
//
// This used to be three headers (X-Kit-Web for the console, X-Kit-Chat for
// the PWA, X-Kit-Vault for the vault) with three wrappers that agreed on
// the idea and disagreed on the details. It is one header and one wrapper
// now.
const CSRFHeader = "X-Kit-Web"

// legacyCSRFHeaders are accepted alongside CSRFHeader while clients built
// before the consolidation are still cached by the PWA's service worker.
// Remove once a release has shipped with every frontend sending X-Kit-Web.
var legacyCSRFHeaders = []string{"X-Kit-Chat", "X-Kit-Vault"}

// RequireCSRF refuses a state-changing request (POST, PUT, PATCH, DELETE)
// unless it proves it was sent by first-party script: it carries the custom
// header, or its body is application/json (which browsers also refuse to
// send cross-origin without preflight). When the browser supplies an Origin
// header it must also match the request host; a mismatch is refused even
// with the header, since a misconfigured CORS policy elsewhere should not be
// able to turn a custom header into a bypass. GET, HEAD and OPTIONS pass.
func RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if !hasCSRFProof(r) {
				http.Error(w, "missing "+CSRFHeader+" header", http.StatusForbidden)
				return
			}
			if !originMatchesHost(r) {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func hasCSRFProof(r *http.Request) bool {
	if r.Header.Get(CSRFHeader) == "1" {
		return true
	}
	for _, h := range legacyCSRFHeaders {
		if r.Header.Get(h) == "1" {
			return true
		}
	}
	return strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
}

// originMatchesHost compares the Origin header's host to the request's
// Host. A missing Origin passes (older clients and some same-origin
// requests omit it); an unparseable or "null" Origin does not.
func originMatchesHost(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
