package livegame

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The poll answers 204 only when the client already has this version or a
// newer one; anything it cannot parse gets the full state.
func TestUnchanged(t *testing.T) {
	cases := []struct {
		query string
		want  bool
	}{
		{"", false},
		{"?since=4", false},
		{"?since=5", true},
		{"?since=9", true},
		{"?since=nonsense", false},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		got := Unchanged(rec, httptest.NewRequest(http.MethodGet, "/state"+c.query, nil), 5)
		if got != c.want {
			t.Errorf("%q: Unchanged = %v, want %v", c.query, got, c.want)
		}
		if got && rec.Code != http.StatusNoContent {
			t.Errorf("%q: status = %d, want 204", c.query, rec.Code)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%q: the poll is cacheable", c.query)
		}
	}
}
