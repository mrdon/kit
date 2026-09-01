package trivia

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mrdon/kit/internal/auth"
)

// A refused request that leaves no server-side trace is undebuggable from the
// operator's side: somebody reports "I got a 400 saving the settings", the
// logs are silent, and the only way to find out which rule fired is to
// reproduce it by hand. That happened, so it gets a test.
//
// captureLogs swaps the default slog handler for the duration of a test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// serveConsole drives a request through the console routes with a caller in
// context, so the handler under test actually runs.
func (f *fixture) serveConsole(t *testing.T, method, path string, body any) (int, string) {
	t.Helper()
	mux := http.NewServeMux()
	app := &App{pool: f.pool, svc: f.svc, baseURL: "http://localhost:8489"}
	// Wrapped PER ROUTE, as production does: r.PathValue is only populated
	// once the mux has matched, so tenant middleware wrapping the whole mux
	// would see an empty slug.
	tenantMW := auth.TenantFromPath(f.pool)
	mux.Handle("PATCH /{slug}/api/trivia/games/{id}", tenantMW(http.HandlerFunc(app.handleUpdateGame)))
	mux.Handle("POST /{slug}/api/trivia/games/{id}/action", tenantMW(http.HandlerFunc(app.handleAction)))
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// Settings the server refuses must say so in the logs, with the route and the
// reason, not just on the wire.
func TestRefusedConsoleRequestIsLogged(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	logs := captureLogs(t)

	bad := Settings{
		BoardRows: 2, BoardColumns: 5,
		CellValues:    []int{500}, // one value for two rows: refused
		TokenValues:   []int{100, 200},
		AnswerSeconds: 60, RevealSeconds: 15, BetSeconds: 45,
	}
	status, body := f.serveConsole(t, http.MethodPatch,
		"/"+f.tenant.Slug+"/api/trivia/games/"+game.ID.String(),
		map[string]any{"settings": bad})

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %q)", status, body)
	}
	out := logs.String()
	if !strings.Contains(out, "trivia request refused") {
		t.Fatalf("the 400 left no trace in the logs:\n%s", out)
	}
	for _, want := range []string{`"status":400`, `"method":"PATCH"`, "cell values"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log line is missing %q:\n%s", want, out)
		}
	}
}

// A stale host tab clicking into a phase the game already left is the other
// refusal an operator gets asked about.
func TestPhaseConflictIsLogged(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	logs := captureLogs(t)

	status, _ := f.serveConsole(t, http.MethodPost,
		"/"+f.tenant.Slug+"/api/trivia/games/"+game.ID.String()+"/action",
		ActionRequest{Action: ActionReveal, FromPhase: PhaseQuestion})

	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
	out := logs.String()
	if !strings.Contains(out, "trivia request refused") || !strings.Contains(out, `"status":409`) {
		t.Fatalf("the 409 left no usable trace:\n%s", out)
	}
}

// A successful request must NOT log a refusal — otherwise the signal is
// worthless.
func TestSuccessfulRequestLogsNoRefusal(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	logs := captureLogs(t)

	status, body := f.serveConsole(t, http.MethodPatch,
		"/"+f.tenant.Slug+"/api/trivia/games/"+game.ID.String(),
		map[string]any{"settings": DefaultSettings()})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", status, body)
	}
	if strings.Contains(logs.String(), "trivia request refused") {
		t.Fatalf("a successful save logged a refusal:\n%s", logs.String())
	}
}

// A public refusal is logged too — a player reporting "it won't let me bet"
// should be findable.
func TestRefusedPlayerRequestIsLogged(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	logs := captureLogs(t)

	mux := http.NewServeMux()
	registerPublicRoutes(mux, &App{pool: f.pool, svc: f.svc, baseURL: "http://localhost:8489"})
	rec := f.request(http.MethodPost, f.gamePath(game)+"/answer", answerRequest{Answer: "42"}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	_ = mux
	out := logs.String()
	if !strings.Contains(out, "trivia request refused") || !strings.Contains(out, `"status":401`) {
		t.Fatalf("the 401 left no trace:\n%s", out)
	}
	if !strings.Contains(out, "join the game first") {
		t.Fatalf("the log does not say why:\n%s", out)
	}
}
