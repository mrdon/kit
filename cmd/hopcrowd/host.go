package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mrdon/kit/internal/apps/hoppers"
	"github.com/mrdon/kit/internal/apps/hoppers/engine"
)

// hostBot watches the host stream, logs the night and presses the host
// button (POST /advance with the stage it saw, as the console does).
type hostBot struct {
	c        *conn
	o        opts
	log      func(string, ...any)
	onRacing func() // once, when round 2 starts

	mu         sync.Mutex
	last       hoppers.Stage
	lastSeq    int
	lastPhase  engine.Phase
	heatKey    string
	resultsKey string
	racing     bool
	done       chan struct{}
	once       sync.Once
}

func (h *hostBot) run(ctx context.Context) error {
	h.done = make(chan struct{})
	go func() {
		for {
			if err := h.c.stream(ctx, h.onFrame); err != nil && ctx.Err() == nil {
				h.log("host stream: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-h.done:
				return
			case <-time.After(time.Second):
			}
		}
	}()
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("night did not finish: %w", ctx.Err())
	}
}

// press waits, then presses from wherever the game is by then. A 409 means
// the game moved on its own in between; the next frame presses again.
func (h *hostBot) press(after time.Duration) {
	go func() {
		time.Sleep(after)
		h.mu.Lock()
		from := h.last
		h.mu.Unlock()
		if _, err := h.c.post(context.Background(), "/advance", from, nil); err != nil {
			h.log("host press: %v", err)
		}
	}()
}

func (h *hostBot) onFrame(s *hoppers.Frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = hoppers.Stage{Phase: s.Phase, Sub: s.Sub, Round: s.Round, Heat: s.Heat}
	h.logFrame(s)
	if s.Phase == engine.PhaseRacing && s.Round >= 2 && !h.racing { // a round in, so people have points
		h.racing = true
		if h.onRacing != nil {
			h.onRacing()
		}
	}
	if s.Phase != h.lastPhase {
		switch s.Phase {
		case engine.PhaseLobby:
			h.press(3 * time.Second)
		case engine.PhaseRacing:
			// presses mid-round wait for each heat's results, below
		case engine.PhaseFinal:
			js, _ := json.Marshal(s.Standings)
			h.log("FINAL champion=%s standings=%s", s.Champion, js)
			go func() { time.Sleep(6 * time.Second); h.once.Do(func() { close(h.done) }) }()
		}
	}
	// Results wait for the host: a short look at a heat's results, a longer
	// beer break when the round is over (the last one leads to the final).
	if s.Phase == engine.PhaseRacing && s.Sub == engine.SubRaceEnd {
		if k := fmt.Sprint(s.Round, s.Heat); k != h.resultsKey {
			h.resultsKey = k
			if s.RoundOver {
				h.press(h.o.breakFor)
			} else {
				h.press(h.o.resultsFor)
			}
		}
	}
	h.lastPhase = s.Phase
}

func (h *hostBot) logFrame(s *hoppers.Frame) {
	for _, e := range s.Events {
		if e.Seq > h.lastSeq {
			h.lastSeq = e.Seq
			h.log("r%d h%d t%d  %s", s.Round, s.Heat, s.Turn, e.Text)
		}
	}
	k := fmt.Sprint(s.Round, s.Heat, s.Sub == engine.SubRaceEnd)
	if s.Phase != engine.PhaseRacing || k == h.heatKey {
		return
	}
	h.heatKey = k
	if s.Sub == engine.SubRaceEnd {
		var pl []string
		for _, r := range s.Results {
			pl = append(pl, fmt.Sprintf("%d.%s+%d", r.Place, r.Player, r.Points))
		}
		h.log("RESULT %s: %s", s.HeatName, strings.Join(pl, " "))
	} else if s.Heat > 0 && s.Heat <= len(s.Heats) {
		h.log("HEAT %s (%d/%d) frogs %v", s.HeatName, s.Heat, len(s.Heats), s.Heats[s.Heat-1].Frogs)
	}
}
