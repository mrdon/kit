package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"sync"
	"time"

	"github.com/mrdon/kit/internal/apps/hoppers"
	"github.com/mrdon/kit/internal/apps/hoppers/engine"
)

// phone is one fake person: a frog, a cookie jar, a flick style.
type phone struct {
	n            int
	name, style  string
	color, hat   string
	poll         bool // use the poll fallback instead of the stream
	c            *conn
	log          func(format string, args ...any)
	mu           sync.Mutex
	id           string
	score        int
	flickKey     string
	stop         context.CancelFunc // ends the current stream/poll
	disconnected bool
}

func newJar() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

// join makes the frog (POST /join sets the identity cookie in the jar).
func (p *phone) join(ctx context.Context) error {
	var r struct {
		PlayerID string `json:"playerId"`
	}
	if _, err := p.c.post(ctx, "/join", map[string]string{"name": p.name, "color": p.color, "hat": p.hat}, &r); err != nil {
		return err
	}
	p.mu.Lock()
	p.id = r.PlayerID
	p.mu.Unlock()
	return nil
}

// listen opens the stream (or the poll loop) in the background.
func (p *phone) listen(ctx context.Context) {
	lctx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.stop, p.disconnected = cancel, false
	p.mu.Unlock()
	go func() {
		for lctx.Err() == nil {
			var err error
			if p.poll {
				err = p.c.poll(lctx, 500*time.Millisecond, p.onFrame)
			} else {
				err = p.c.stream(lctx, p.onFrame)
			}
			if lctx.Err() == nil {
				p.log("phone %d (%s) stream dropped (%v), reconnecting", p.n, p.name, err)
				time.Sleep(time.Second)
			}
		}
	}()
}

// drop kills the phone's connection (the browser closed, wifi gone).
func (p *phone) drop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stop != nil {
		p.stop()
	}
	p.disconnected = true
}

func (p *phone) onFrame(f *hoppers.Frame) {
	p.mu.Lock()
	if f.You != nil {
		p.id, p.score = f.You.ID, f.You.Score
	}
	p.mu.Unlock()
	p.act(f)
}

// act flicks on this phone's turn, after a delay that depends on its style.
func (p *phone) act(s *hoppers.Frame) {
	if s.You == nil || !s.You.MyTurn || s.Sub != engine.SubFlick {
		return
	}
	key := fmt.Sprint(s.Round, s.Heat, s.Turn)
	p.mu.Lock()
	if key == p.flickKey {
		p.mu.Unlock()
		return
	}
	p.flickKey = key
	p.mu.Unlock()
	band, spot := s.FlickBand, s.FlickSpot // only phones are told where the band sits
	var wait time.Duration
	switch p.style {
	case "asleep": // (fake-person styles, not game values)
		if rand.IntN(2) == 0 {
			return // let the timer auto-hop
		}
		wait = 5 * time.Second
	case "quick":
		wait = time.Duration(200+rand.IntN(500)) * time.Millisecond
	case "slow":
		wait = time.Duration(2000+rand.IntN(3000)) * time.Millisecond
	default:
		wait = time.Duration(600+rand.IntN(1800)) * time.Millisecond
	}
	go func() {
		time.Sleep(wait)
		pw := rand.Float64()
		flick := map[string]any{"power": pw, "perfect": pw > spot-band/2 && pw < spot+band/2}
		if _, err := p.c.post(context.Background(), "/flick", flick, nil); err != nil {
			p.log("phone %d flick: %v", p.n, err)
		}
	}()
}

func (p *phone) snapshot() (id string, score int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.id, p.score
}
