package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

// scripted drives a night with a fixed, state-derived script: people flick
// after a delay that depends on the turn (some never do, some hit the band),
// one person drops out and one turns up late, the host sometimes skips the
// gap between heats. Same game in, same commands and times out, so two games
// driven by it stay in lockstep as long as the engine does.
type scripted struct {
	people int
	asked  string
	queue  []scriptCmd
}

type scriptCmd struct {
	at  time.Time
	key string
	cmd Command
}

func scriptTurnKey(g *Game) string { return fmt.Sprint(g.Round(), g.HeatIndex(), g.Turn(), g.Sub()) }

func newScripted(g *Game, people int) *scripted {
	for i := range people {
		g.Apply(Command{Type: CmdJoin, Player: fmt.Sprintf("p-%02d", i), Name: fmt.Sprintf("P%d", i), Color: palette[i%3]}, t0)
	}
	g.Apply(Command{Type: CmdHost}, t0)
	return &scripted{people: people}
}

// step does one thing (a queued command, a host press or a clock jump) and
// returns the new time, or false when the night is over.
func (s *scripted) step(g *Game, now time.Time) (time.Time, bool) {
	switch g.Phase() {
	case PhaseFinal:
		return now, false
	case PhaseLobby:
		return now, false
	case PhaseRacing:
	}
	s.plan(g, now)
	next := g.NextDeadline()
	if len(s.queue) > 0 && (next.IsZero() || !s.queue[0].at.After(next)) {
		p := s.queue[0]
		s.queue = s.queue[1:]
		if p.at.After(now) {
			now = p.at
		}
		g.Advance(now)
		if p.key == "" || scriptTurnKey(g) == p.key {
			g.Apply(p.cmd, now)
		}
		return now, true
	}
	if next.IsZero() {
		return now, false
	}
	g.Advance(next)
	return next, true
}

func (s *scripted) plan(g *Game, now time.Time) {
	turn := g.Turn()
	if g.Sub() == SubRaceEnd {
		// the host reads out the results, longer at the end of a round
		if k := scriptTurnKey(g); k != s.asked {
			s.asked = k
			wait := time.Duration(4+g.Round()) * time.Second
			if g.roundOver() {
				wait = 90 * time.Second
			}
			s.queue = append(s.queue, scriptCmd{at: now.Add(wait), key: k, cmd: Command{Type: CmdHost}})
		}
		return
	}
	if g.Sub() != SubFlick {
		return
	}
	cur := g.current()
	k := scriptTurnKey(g)
	if k == s.asked {
		return
	}
	s.asked = k
	if g.Round() == 1 && turn == 5 {
		s.queue = append(s.queue, scriptCmd{at: now.Add(300 * time.Millisecond), cmd: Command{Type: CmdJoin, Player: "late", Name: "Late", Color: "#e74c3c"}})
	}
	if g.Round() == 2 && turn == 3 && s.people > 1 {
		s.queue = append(s.queue, scriptCmd{at: now.Add(200 * time.Millisecond), cmd: Command{Type: CmdLeave, Player: "p-01"}})
	}
	if p := g.players[cur]; p == nil || p.Bot || turn%7 == 0 {
		return
	}
	delay := time.Duration(600+(turn%5)*450) * time.Millisecond
	s.queue = append(s.queue, scriptCmd{at: now.Add(delay), key: k, cmd: Command{Type: CmdFlick, Player: cur, Power: float64(turn%10) / 10, Perfect: turn%3 == 0}})
}

// fingerprint hashes the parts of a snapshot that existed before the save /
// version / view work, so the same seed can be checked against the old engine.
func fingerprint(h hash.Hash, s State) {
	fmt.Fprint(h, s.Phase, s.Sub, s.Round, s.Turn, s.Current, s.OnDeck, s.Deadline, s.ServerNow, s.Stacks, s.TurnOrder, s.Backward, s.Winner, s.Champion, s.Heat, s.HeatName)
	for _, p := range s.Players {
		fmt.Fprint(h, *p)
	}
	for _, e := range s.Events {
		fmt.Fprint(h, e.Seq, e.Text)
	}
	if s.LastHop != nil {
		hp := *s.LastHop
		fmt.Fprint(h, hp.Seq, hp.Frog, hp.From, hp.To, hp.Roll, hp.Moved, hp.LandedOn, hp.Under, hp.Back, hp.Turned, hp.Auto, hp.Power, hp.Perfect, hp.Bonus, hp.Landed, hp.Special)
	}
	for _, x := range s.Heats {
		fmt.Fprint(h, *x)
	}
	fmt.Fprint(h, s.Standings, s.Results, s.Track)
}

type goldenCase struct {
	name   string
	cfg    func() GameConfig
	people int
	seed   uint64
}

var goldenCases = []goldenCase{
	{"bots", func() GameConfig {
		return GameConfig{Rules: Rules{MinFrogs: 7, Rounds: 3, SlipChance: 0.2}, Levels: SingleLevel(16)}
	}, 2, 42},
	{"heats+specials", func() GameConfig {
		c, err := Generate(rand.New(rand.NewPCG(5, 5)), Preferences{Launches: 2, Chutes: 2}, nil)
		if err != nil {
			panic(err)
		}
		c.Rules.HeatSize = 6
		return c
	}, 14, 7},
	{"random specials", func() GameConfig {
		return Default().WithRandomSpecials(1, 2)
	}, 5, 99},
}

type nightResult struct {
	Champion string
	Scores   string
	Hops     int
	Digest   string
}

func playGolden(c goldenCase) nightResult {
	g := NewGame(c.cfg(), c.seed)
	s := newScripted(g, c.people)
	h := sha256.New()
	now := t0
	hops, lastSeq := 0, 0
	for ok := true; ok; {
		now, ok = s.step(g, now)
		st := g.publicState() // what Snapshot was before views
		fingerprint(h, st)
		if st.LastHop != nil && st.LastHop.Seq != lastSeq {
			lastSeq = st.LastHop.Seq
			hops++
		}
	}
	var scores []string
	for _, p := range g.Snapshot().Standings {
		scores = append(scores, fmt.Sprintf("%s=%d", p.Player, p.Points))
	}
	return nightResult{Champion: g.champion, Scores: strings.Join(scores, ","), Hops: hops, Digest: hex.EncodeToString(h.Sum(nil))[:16]}
}

// These values were captured from the engine before it was split into files,
// made serializable and given a version counter: the same seed must still
// play the same night, hop for hop and line for line. The digests were then
// re-taken on purpose once, when results started waiting for the host (no
// race-end timer, heat gap or break phase): winners, scores and hop counts
// didn't change. Re-taken again when a perfect flick stopped helping a frog
// going the wrong way: fewer backward hops get shortened, so these nights
// play out differently (new winners and scores). The announcer line changes
// that came with it (no early lead calls, no "holds still") only moved the
// digests.
var goldenWant = map[string]nightResult{
	"bots":            {Champion: "late", Scores: "late=23,p-00=22,p-01=12", Hops: 95, Digest: "79bf942cf79aed99"},
	"heats+specials":  {Champion: "p-07", Scores: "p-07=29,p-11=27,p-08=25,p-02=23,p-06=23,p-04=22,p-09=22,late=22,p-13=21,p-03=21,p-10=21,p-12=19,p-00=18,p-05=18,p-01=15", Hops: 239, Digest: "6c58f105709b96f1"},
	"random specials": {Champion: "p-00", Scores: "p-00=27,p-02=20,p-01=18,p-04=17,p-03=16,late=16", Hops: 60, Digest: "10797a8b7e22c7bb"},
}

func TestGoldenNights(t *testing.T) {
	for _, c := range goldenCases {
		got := playGolden(c)
		want, ok := goldenWant[c.name]
		if !ok {
			t.Errorf("%s: no golden value; got %#v", c.name, got)
			continue
		}
		if got != want {
			t.Errorf("%s: night changed\n got %#v\nwant %#v", c.name, got, want)
		}
	}
}
