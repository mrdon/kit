//go:build e2e

package trivia

// THE CHAOS TEST. Real Kit routes and sweeper, real TV and phone pages in
// headless Chrome, and a proxy between them that misbehaves the way bar wifi
// and deploys do. Every scenario plays one full cell -- question, reveal,
// betting, scoring, three of them ended by the clock alone -- and asserts that
// every screen in the room ends up showing what the server says, within a
// budget a room would tolerate.
//
// Run with `make trivia-e2e` (needs Postgres, Node, Chrome and the global
// playwright-core package). Not part of `make test`: it takes minutes.

import (
	"fmt"
	"testing"
	"time"
)

const tight = time.Second // a healthy screen follows a transition within this

type cellPlan struct {
	name   string
	strict func(h *harness) []string // must follow every transition within limit
	limit  time.Duration
	// fault starts on entry to the named phase and closes done when the
	// network is healthy again.
	at    Phase
	fault func(h *harness, done chan<- struct{})
	// settle is the budget, once the fault is over, for every screen to
	// catch up.
	settle time.Duration
}

func allPages(h *harness) []string { return h.names }
func nobody(*harness) []string     { return nil }

func TestE2EChaos(t *testing.T) {
	h := newChaosRoom(t)
	plans := []cellPlan{
		{name: "clean network", strict: allPages, limit: tight, settle: 5 * time.Second},
		{
			name: "400ms latency on every chunk", strict: allPages, limit: 6 * time.Second,
			at: PhaseQuestion, settle: 10 * time.Second,
			fault: func(h *harness, done chan<- struct{}) {
				h.proxy.setLatency(400 * time.Millisecond)
				go func() { h.waitPhase(PhaseScoring); h.proxy.setLatency(0); close(done) }()
			},
		},
		{
			name: "zombie wifi (stall, sockets open) across the answer deadline", strict: nobody,
			at: PhaseQuestion, settle: 15 * time.Second,
			fault: func(h *harness, done chan<- struct{}) {
				go func() {
					time.Sleep(4 * time.Second)
					h.proxy.set(netStall)
					time.Sleep(10 * time.Second)
					h.proxy.set(netOK)
					close(done)
				}()
			},
		},
		{
			// The hard one. Nothing errors: every socket on every screen just
			// goes silent forever, mid-question. Only the clients' own
			// watchdogs (silence, and the countdown sitting on zero) can
			// notice and redial.
			name: "zombie sockets: every open connection silently dead", strict: nobody,
			at: PhaseQuestion, settle: 25 * time.Second,
			fault: func(h *harness, done chan<- struct{}) {
				go func() {
					time.Sleep(2 * time.Second)
					h.t.Logf("zombified %d connections", h.proxy.zombify())
					close(done)
				}()
			},
		},
		{
			name: "network down across reveal -> betting", strict: nobody,
			at: PhaseReveal, settle: 20 * time.Second,
			fault: func(h *harness, done chan<- struct{}) {
				h.proxy.set(netDown)
				go func() { time.Sleep(6 * time.Second); h.proxy.set(netOK); close(done) }()
			},
		},
		{
			name: "flapping: down/up every 1.5s for the whole cell", strict: nobody,
			at: PhaseQuestion, settle: 20 * time.Second,
			fault: func(h *harness, done chan<- struct{}) {
				go func() {
					for h.truth().Phase != PhaseScoring {
						h.proxy.set(netDown)
						time.Sleep(1500 * time.Millisecond)
						h.proxy.set(netOK)
						time.Sleep(1500 * time.Millisecond)
					}
					close(done)
				}()
			},
		},
		{
			name: "deploy mid-question, deadline passes while the server is down", strict: nobody,
			at: PhaseQuestion, settle: 20 * time.Second,
			fault: func(h *harness, done chan<- struct{}) {
				go func() { time.Sleep(2 * time.Second); h.deploy(8 * time.Second); close(done) }()
			},
		},
		{
			name:   "one phone offline for a whole phase; the rest stay live",
			strict: func(h *harness) []string { return []string{"tv", "phone-2", "phone-3"} },
			limit:  tight, at: PhaseQuestion, settle: 15 * time.Second,
			fault: func(h *harness, done chan<- struct{}) {
				h.obs.send(map[string]any{"cmd": "offline", "page": "phone-1", "on": true})
				go func() {
					time.Sleep(10 * time.Second)
					h.obs.send(map[string]any{"cmd": "offline", "page": "phone-1", "on": false})
					close(done)
				}()
			},
		},
	}
	for _, p := range plans {
		if !t.Run(p.name, func(t *testing.T) { h.t = t; h.playCell(p) }) {
			break // later cells assume the game is back on the board
		}
	}
	h.t = t
	if errs := h.obs.pageErrors(); len(errs) > 0 {
		t.Errorf("JavaScript errors on the pages: %v", errs)
	}
}

// waitPhase blocks until the server is in p (or 90s pass).
func (h *harness) waitPhase(p Phase) {
	for end := time.Now().Add(90 * time.Second); time.Now().Before(end); {
		if h.truth().Phase == p {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// playCell picks the next cell, lets the clock run it to scoring with the
// plan's fault injected, checks every screen caught up, and returns the game
// to the board.
func (h *harness) playCell(p cellPlan) {
	h.t.Helper()
	h.pickNextCell()
	done := make(chan struct{})
	if p.fault == nil {
		close(done)
	}
	h.followToScoring(p, done)
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		h.t.Fatal("fault never cleared")
	}
	h.converge("after "+p.name, h.names, p.settle)
	h.do(ActionRequest{Action: ActionNext, FromPhase: PhaseScoring})
	h.converge("back to the board", h.names, p.settle)
}

func (h *harness) pickNextCell() {
	h.t.Helper()
	for _, c := range h.truth().Board {
		if !c.Played {
			id := c.ID
			h.do(ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &id})
			// Two of three tables answer, so the clock -- not an early
			// close -- ends the question.
			for _, team := range h.answering() {
				if err := h.svc().SubmitAnswer(h.f.ctx, h.f.tenant.ID, h.game.ID, team, "42"); err != nil {
					h.t.Fatalf("answer: %v", err)
				}
			}
			return
		}
	}
	h.t.Fatal("board is out of cells")
}

// followToScoring watches the server until scoring, injecting the fault on
// the planned phase. Strict pages are held to the limit on every version
// throughout; once the fault clears, EVERY page is held to settle, counted
// from the later of the version landing and the network healing.
func (h *harness) followToScoring(p cellPlan, done chan struct{}) {
	h.t.Helper()
	w := &watch{h: h, p: p, strict: map[int64]time.Time{}, all: map[int64]time.Time{}, start: time.Now()}
	var lastPhase Phase
	var enteredAt time.Time
	for end := time.Now().Add(90 * time.Second); ; {
		if time.Now().After(end) {
			h.t.Fatalf("server stuck in %s: the clock never ended the phase", h.truth().Phase)
		}
		tr := h.truth()
		if tr.Phase != lastPhase {
			h.logPhase(lastPhase, tr.Phase, enteredAt)
			lastPhase, enteredAt = tr.Phase, time.Now()
			if p.fault != nil && tr.Phase == p.at {
				p.fault(h, done)
			}
		}
		w.healed(done)
		w.track(tr.StateVersion)
		w.check()
		if tr.Phase == PhaseScoring {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	w.report()
}

// watch holds every version the server has published against the screens.
type watch struct {
	h        *harness
	p        cellPlan
	strict   map[int64]time.Time // version -> when the server had it
	all      map[int64]time.Time
	start    time.Time
	healedAt time.Time
}

func (w *watch) healed(done chan struct{}) {
	if w.p.fault == nil || !w.healedAt.IsZero() {
		return
	}
	select {
	case <-done:
		w.healedAt = time.Now()
	default:
	}
}

func (w *watch) track(v int64) {
	if _, ok := w.strict[v]; !ok {
		w.strict[v] = time.Now()
		w.all[v] = time.Now()
	}
}

func (w *watch) check() {
	for v, at := range w.strict {
		late := w.h.laggards(v, w.p.strict(w.h))
		if len(late) == 0 {
			delete(w.strict, v)
		} else if time.Since(at) > w.p.limit {
			w.h.t.Fatalf("v%d reached the server %v ago but these screens still lack it: %v",
				v, time.Since(at).Round(10*time.Millisecond), late)
		}
	}
	if w.healedAt.IsZero() {
		return
	}
	for v, at := range w.all {
		from := at
		if w.healedAt.After(at) {
			from = w.healedAt
		}
		late := w.h.laggards(v, w.h.names)
		if len(late) == 0 {
			delete(w.all, v)
		} else if time.Since(from) > w.p.settle {
			w.h.t.Fatalf("v%d: %v after the network healed these screens still lack it: %v",
				v, time.Since(from).Round(10*time.Millisecond), late)
		}
	}
}

// report measures real lag: from the instant a server published a version
// to the instant each browser received it (SSE frame or poll response). For
// a faulted cell, a version published while the network was broken is
// counted from the heal.
func (w *watch) report() {
	strict := map[string]bool{}
	for _, n := range w.p.strict(w.h) {
		strict[n] = true
	}
	var worstStrict, worstAll lagSample
	for v, pub := range w.h.pubs.since(w.start) {
		for _, page := range w.h.names {
			got := w.h.obs.arrivedAt(page, v)
			if got.IsZero() {
				continue // superseded before it arrived; the in-loop check covers stuck pages
			}
			if strict[page] && (w.healedAt.IsZero() || pub.Before(w.healedAt)) {
				worstStrict = worstStrict.worse(lagSample{got.Sub(pub), page, v})
			}
			if !w.healedAt.IsZero() {
				from := pub
				if w.healedAt.After(pub) {
					from = w.healedAt
				}
				worstAll = worstAll.worse(lagSample{got.Sub(from), page, v})
			}
		}
	}
	if w.p.limit > 0 {
		w.h.t.Logf("LAG (live screens, whole cell): worst %s (limit %v)", worstStrict, w.p.limit)
	}
	if !w.healedAt.IsZero() {
		w.h.t.Logf("LAG (every screen, after the network healed): worst %s (budget %v)", worstAll, w.p.settle)
	}
}

type lagSample struct {
	d    time.Duration
	page string
	v    int64
}

func (a lagSample) worse(b lagSample) lagSample {
	if b.d > a.d {
		return b
	}
	return a
}

func (a lagSample) String() string {
	if a.page == "" {
		return "n/a"
	}
	return fmt.Sprintf("%v (%s, v%d)", a.d.Round(time.Millisecond), a.page, a.v)
}

func (h *harness) logPhase(from, to Phase, enteredAt time.Time) {
	if from == "" {
		return
	}
	h.t.Logf("server %s -> %s after %v", from, to, time.Since(enteredAt).Round(10*time.Millisecond))
}
