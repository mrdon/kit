package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireCSRF(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := RequireCSRF(ok)

	cases := []struct {
		name    string
		method  string
		headers map[string]string
		want    int
	}{
		{"get passes bare", http.MethodGet, nil, http.StatusNoContent},
		{"post bare refused", http.MethodPost, nil, http.StatusForbidden},
		{"post with header", http.MethodPost, map[string]string{CSRFHeader: "1"}, http.StatusNoContent},
		{"post with legacy chat header", http.MethodPost, map[string]string{"X-Kit-Chat": "1"}, http.StatusNoContent},
		{"post with legacy vault header", http.MethodDelete, map[string]string{"X-Kit-Vault": "1"}, http.StatusNoContent},
		{"post with json body", http.MethodPost, map[string]string{"Content-Type": "application/json; charset=utf-8"}, http.StatusNoContent},
		{"post form body refused", http.MethodPost, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, http.StatusForbidden},
		{"header but foreign origin", http.MethodPatch, map[string]string{CSRFHeader: "1", "Origin": "https://evil.example"}, http.StatusForbidden},
		{"header and same origin", http.MethodPut, map[string]string{CSRFHeader: "1", "Origin": "https://kit.example"}, http.StatusNoContent},
		{"header and null origin", http.MethodPost, map[string]string{CSRFHeader: "1", "Origin": "null"}, http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "https://kit.example/acme/api/x", nil)
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d", rec.Code, c.want)
			}
		})
	}
}
