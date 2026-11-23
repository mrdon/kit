//go:build e2e

package trivia

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	consoleweb "github.com/mrdon/kit/web/console"
)

// backend is one running Kit web process: its own Service (broker, sweeper)
// over the shared database, exactly as a deploy brings up a fresh one.
type backend struct {
	svc  *Service
	srv  *httptest.Server
	stop context.CancelFunc
}

// pubLog is when each version first left a server, across deploys.
type pubLog struct {
	mu sync.Mutex
	at map[int64]time.Time
}

func (l *pubLog) record(v int64) {
	l.mu.Lock()
	if _, ok := l.at[v]; !ok {
		l.at[v] = time.Now()
	}
	l.mu.Unlock()
}

func (l *pubLog) since(t time.Time) map[int64]time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[int64]time.Time{}
	for v, at := range l.at {
		if !at.Before(t) {
			out[v] = at
		}
	}
	return out
}

func startBackend(f *fixture, baseURL string, gameID uuid.UUID, pubs *pubLog) *backend {
	svc := NewService(f.pool)
	frames, unsub := svc.Broker().Subscribe(gameID)
	go func() {
		for snap := range frames {
			pubs.record(snap.StateVersion)
		}
	}()
	app := &App{pool: f.pool, svc: svc, baseURL: baseURL}
	mux := http.NewServeMux()
	registerPublicRoutes(mux, app)
	mux.Handle("GET /console/assets/", consoleweb.AssetHandler())
	ctx, cancel := context.WithCancel(context.Background())
	svc.StartSweeper(ctx)
	stop := func() { cancel(); unsub() }
	return &backend{svc: svc, srv: httptest.NewServer(mux), stop: stop}
}

// kill is a process dying: sweeper gone, every socket cut.
func (b *backend) kill() {
	b.stop()
	b.srv.CloseClientConnections()
	b.srv.Close()
}

func (b *backend) addr() string { return strings.TrimPrefix(b.srv.URL, "http://") }

// pageView is the newest state one browser page has received.
type pageView struct {
	Version  int64
	Phase    string
	Screen   string
	At       time.Time
	arrivals []arrival
}

type arrival struct {
	v  int64
	at time.Time
}

// observer is the Node/Playwright process holding the real pages.
type observer struct {
	t     *testing.T
	stdin io.WriteCloser
	mu    sync.Mutex
	pages map[string]pageView
	errs  []string
	ready chan struct{}
}

type obsLine struct {
	Ready   bool    `json:"ready"`
	Page    string  `json:"page"`
	Version *int64  `json:"version"`
	Phase   string  `json:"phase"`
	Screen  *string `json:"screen"`
	Via     string  `json:"via"`
	Error   string  `json:"error"`
}

type obsPage struct {
	Name   string     `json:"name"`
	Path   string     `json:"path"`
	Cookie *obsCookie `json:"cookie,omitempty"`
}

type obsCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Path  string `json:"path"`
}

func startObserver(t *testing.T, base string, pages []obsPage) *observer {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "observer.json")
	raw, _ := json.Marshal(map[string]any{"base": base, "pages": pages})
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := exec.Command("npm", "root", "-g").Output()
	if err != nil {
		t.Fatalf("npm root -g: %v", err)
	}
	cmd := exec.Command("node", "e2e/observer.mjs", cfgPath)
	cmd.Env = append(os.Environ(), "NODE_PATH="+strings.TrimSpace(string(root)))
	cmd.Stderr = os.Stderr
	stdout, _ := cmd.StdoutPipe()
	stdin, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting observer: %v", err)
	}
	o := &observer{t: t, stdin: stdin, pages: map[string]pageView{}, ready: make(chan struct{})}
	go o.read(stdout)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		o.send(map[string]any{"cmd": "quit"})
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	select {
	case <-o.ready:
	case err := <-exited:
		t.Fatalf("observer exited before it was ready: %v", err)
	case <-time.After(60 * time.Second):
		t.Fatal("observer never became ready")
	}
	return o
}

func (o *observer) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var l obsLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		o.mu.Lock()
		switch {
		case l.Ready:
			close(o.ready)
		case l.Error != "":
			o.errs = append(o.errs, l.Page+": "+l.Error)
		case l.Page != "":
			v := o.pages[l.Page]
			if l.Version != nil && *l.Version > v.Version {
				v.Version, v.Phase, v.At = *l.Version, l.Phase, time.Now()
				v.arrivals = append(v.arrivals, arrival{*l.Version, v.At})
			}
			if l.Screen != nil {
				v.Screen = *l.Screen
			}
			o.pages[l.Page] = v
		}
		o.mu.Unlock()
	}
}

func (o *observer) send(cmd map[string]any) {
	raw, _ := json.Marshal(cmd)
	_, _ = o.stdin.Write(append(raw, '\n'))
}

func (o *observer) view(name string) pageView {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pages[name]
}

// arrivedAt is when page first held version v or newer (zero if never).
func (o *observer) arrivedAt(page string, v int64) time.Time {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, a := range o.pages[page].arrivals {
		if a.v >= v {
			return a.at
		}
	}
	return time.Time{}
}

func (o *observer) pageErrors() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.errs...)
}

// harness is one game, its room of browsers, and the network between them.
type harness struct {
	t     *testing.T
	f     *fixture
	game  *Game
	proxy *chaosProxy
	be    *backend
	obs   *observer
	pubs  *pubLog
	names []string // every page
	teams []uuid.UUID
}

// answering is the two tables that answer every question; the third never
// does, so no question closes early.
func (h *harness) answering() []uuid.UUID { return h.teams[:2] }

// newChaosRoom builds a game on short clocks, three tables, a TV and three
// phones behind the chaos proxy, and starts the game onto the board.
func newChaosRoom(t *testing.T) *harness {
	t.Helper()
	f := newFixture(t)
	f.seedBank(topicSet(), 6)
	s := defaultSettings()
	s.AnswerSeconds, s.RevealSeconds, s.BetSeconds, s.GraceSeconds = 6, 3, 5, 1
	game := f.newGame(s, topicSet())

	h := &harness{t: t, f: f, game: game, pubs: &pubLog{at: map[int64]time.Time{}}}
	h.proxy = newChaosProxy(t, "")
	h.be = startBackend(f, h.proxy.URL(), game.ID, h.pubs)
	h.proxy.setBackend(h.be.addr())
	t.Cleanup(func() { h.be.kill() })

	path := CookiePath(f.tenant.Slug, game.Name)
	pages := []obsPage{{Name: "tv", Path: path + "/tv"}}
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("phone-%d", i)
		team, token, err := f.svc.Join(f.ctx, f.tenant.ID, game.ID, fmt.Sprintf("Table %d", i))
		if err != nil {
			t.Fatal(err)
		}
		h.teams = append(h.teams, team.ID)
		pages = append(pages, obsPage{Name: name, Path: path, Cookie: &obsCookie{
			Name: TeamCookieName, Value: CookieValue(team.ID, token), Path: path,
		}})
	}
	for _, p := range pages {
		h.names = append(h.names, p.Name)
	}
	h.obs = startObserver(t, h.proxy.URL(), pages)
	h.converge("pages loading the lobby", h.names, 20*time.Second)
	h.do(ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	h.converge("game start", h.names, 5*time.Second)
	return h
}

func (h *harness) svc() *Service { return h.be.svc }

func (h *harness) truth() *Snapshot {
	h.t.Helper()
	snap, err := h.f.svc.Snapshot(h.f.ctx, h.f.tenant.ID, h.game.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	return snap
}

func (h *harness) do(req ActionRequest) {
	h.t.Helper()
	if _, err := h.svc().Do(h.f.ctx, h.f.tenant.ID, h.game.ID, req); err != nil {
		h.t.Fatalf("host %s: %v", req.Action, err)
	}
}

// deploy replaces the server the way a Dokku deploy does: the old process
// dies, the address refuses connections for a gap, a fresh one comes up.
func (h *harness) deploy(gap time.Duration) {
	h.be.kill()
	time.Sleep(gap)
	h.be = startBackend(h.f, h.proxy.URL(), h.game.ID, h.pubs)
	h.proxy.setBackend(h.be.addr())
}

// laggards lists pages that have not yet received version v.
func (h *harness) laggards(v int64, among []string) []string {
	var out []string
	for _, n := range among {
		if pv := h.obs.view(n); pv.Version < v {
			out = append(out, fmt.Sprintf("%s@v%d(%s)", n, pv.Version, pv.Phase))
		}
	}
	return out
}

// converge waits until every page in among has received the server's current
// version, failing after budget. Returns how long it took.
func (h *harness) converge(label string, among []string, budget time.Duration) time.Duration {
	h.t.Helper()
	start := time.Now()
	for {
		v := h.truth().StateVersion
		if len(h.laggards(v, among)) == 0 {
			return time.Since(start)
		}
		if time.Since(start) > budget {
			tr := h.truth()
			h.t.Fatalf("%s: after %v these screens are still behind the server (v%d %s): %v",
				label, budget, tr.StateVersion, tr.Phase, h.laggards(tr.StateVersion, among))
		}
		time.Sleep(100 * time.Millisecond)
	}
}
