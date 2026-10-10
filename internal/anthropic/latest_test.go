package anthropic

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func day(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }

func TestPickLatestPerFamily(t *testing.T) {
	got := pickLatest([]modelInfo{
		{ID: "claude-3-5-sonnet-20241022", CreatedAt: day(1)},
		{ID: "claude-sonnet-4-6", CreatedAt: day(2)},
		{ID: "claude-sonnet-5-5", CreatedAt: day(3)},
		{ID: "claude-opus-4-7", CreatedAt: day(2)},
		{ID: "claude-haiku-4-5-20251001", CreatedAt: day(2)},
		{ID: "claude-haiku-5-5", CreatedAt: day(2)},
		{ID: "claude-3-haiku-20240307", CreatedAt: day(1)},
		{ID: "something-else", CreatedAt: day(9)},
	})
	want := map[string]string{"opus": "claude-opus-4-7", "sonnet": "claude-sonnet-5-5", "haiku": "claude-haiku-5-5"}
	for family, id := range want {
		if got[family] != id {
			t.Errorf("%s: got %q, want %q", family, got[family], id)
		}
	}
}

func restoreModels(t *testing.T) {
	t.Helper()
	o, s, h := ModelOpus(), ModelSonnet(), ModelHaiku()
	t.Cleanup(func() { SetModels(o, s, h) })
}

func TestResolveLatestKeepsIDsOnError(t *testing.T) {
	restoreModels(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewClient("k")
	c.baseURL = srv.URL
	if err := c.ResolveLatest(t.Context()); err == nil {
		t.Fatal("expected an error from a 500")
	}
	if ModelSonnet() != DefaultSonnet {
		t.Fatalf("sonnet changed on error: %q", ModelSonnet())
	}
}

func TestResolveLatestMovesToNewest(t *testing.T) {
	restoreModels(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "k" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"data":[
			{"id":"claude-sonnet-5-5","created_at":"2026-01-01T00:00:00Z"},
			{"id":"claude-sonnet-6-0","created_at":"2026-06-01T00:00:00Z"}]}`))
	}))
	defer srv.Close()
	c := NewClient("k")
	c.baseURL = srv.URL
	if err := c.ResolveLatest(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ModelSonnet() != "claude-sonnet-6-0" {
		t.Fatalf("sonnet = %q", ModelSonnet())
	}
	if ModelHaiku() != DefaultHaiku {
		t.Fatalf("haiku changed with no haiku listed: %q", ModelHaiku())
	}
}
