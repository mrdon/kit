package agents

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
)

// Seat is one AI person in a headless game. Skill is the chance each of
// their flicks lands in the phone's green band (casual ~0.3, practiced ~0.7,
// expert ~0.9); the harness marks their flicks Perfect at that rate.
type Seat struct {
	Strategy string
	Agent    Agent
	Skill    float64
}

// RaceStat is what happened in one race (one heat).
type RaceStat struct {
	Round, Heat  int
	Frogs        int
	Rotations    float64 // hops per frog (every frog hops once per rotation)
	Hops         int
	BackHops     int
	LeadChanges  int
	Winner       int           // seat that won, -1 for a bot
	Perfects     int           // perfect flicks this race
	Bonuses      int           // ...that won a coaster
	MidLeaderWon bool          // the leader halfway through the race (by hops) went on to win
	LastRotWon   bool          // the leader one rotation before the end went on to win
	Duration     time.Duration // first "get ready" to the frog reaching the finish
	Level        int           // index into the config's Levels
	TrackLen     int
	People       int     // people (seats) racing
	PersonHops   int     // hops of people's frogs (flicks, plus auto-hops when they didn't)
	Launches     int     // hops that hit a launch pad
	Chutes       int     // hops that went down a chute
	MidSpread    float64 // coasters from last frog to first, halfway through the race (by hops)
	Fun          FunStat // pack, comeback and streak numbers (fun.go)
}

// NightResult is what happened in one simulated night.
type NightResult struct {
	Scores   []int // per seat
	Champion int   // seat index, -1 if none
	Races    []RaceStat
	GameTime time.Duration // the whole night in real time, minus beer breaks (host starts each round at once)
}

var epoch = time.Date(2026, 1, 1, 19, 0, 0, 0, time.UTC)

type pending struct {
	at  time.Time
	key string // the turn it was meant for: a late flick must not hop a later turn
	cmd engine.Command
}

func turnKey(g *engine.Game) string {
	return fmt.Sprint(g.Round(), g.HeatIndex(), g.Turn(), g.Sub())
}

// Play runs one full night headless on a virtual clock. Each seat is a
// person; the engine adds bots where a race needs them.
func Play(cfg engine.GameConfig, seed uint64, seats []Seat) (NightResult, error) {
	n := newNight(cfg, seed, seats)
	for steps := 0; n.g.Phase() != engine.PhaseFinal; steps++ {
		if steps > 2_000_000 {
			return n.res, fmt.Errorf("seed %d: game did not finish", seed)
		}
		switch n.g.Phase() {
		case engine.PhaseRacing:
		case engine.PhaseLobby, engine.PhaseFinal:
			return n.res, fmt.Errorf("seed %d: stuck in phase %s", seed, n.g.Phase())
		}
		st := n.g.Snapshot()
		n.stats(st)
		if st.Sub == engine.SubRaceEnd {
			// results wait for the host, who moves on at once here
			n.g.Apply(engine.Command{Type: engine.CmdHost}, n.now)
			continue
		}
		n.decide(st)
		if err := n.tick(); err != nil {
			return n.res, fmt.Errorf("seed %d: %w", seed, err)
		}
	}
	return n.result(), nil
}

// night is one headless game in progress: the engine, the virtual clock, the
// seats' queued commands and the race being measured.
type night struct {
	g        *engine.Game
	seats    []Seat
	seatOf   map[string]int
	skillRNG *rand.Rand
	now      time.Time
	res      NightResult
	queue    []pending
	asked    string
	lastHop  int

	race      *RaceStat
	raceStart time.Time
	leaders   []string // the leader after each hop
	spreads   []int    // first-to-last gap after each hop
	leader    string
	trace     *trace
}

func newNight(cfg engine.GameConfig, seed uint64, seats []Seat) *night {
	n := &night{g: engine.NewGame(cfg, seed), seats: seats, seatOf: map[string]int{},
		skillRNG: rand.New(rand.NewPCG(seed, 0xf11c)), now: epoch,
		res: NightResult{Scores: make([]int, len(seats)), Champion: -1}}
	for i := range seats {
		id := fmt.Sprintf("ai-%d", i)
		n.seatOf[id] = i
		n.g.Apply(engine.Command{Type: engine.CmdJoin, Player: id, Name: fmt.Sprintf("%s #%d", seats[i].Strategy, i), Color: "#ffffff"}, n.now)
	}
	n.g.Apply(engine.Command{Type: engine.CmdHost}, n.now)
	return n
}

// stats measures races: one starts at its countdown, every new hop is
// counted, and the results screen closes it.
func (n *night) stats(st engine.State) {
	if st.Sub == engine.SubReady && n.race == nil {
		n.startRace(st)
	}
	if st.LastHop != nil && st.LastHop.Seq != n.lastHop {
		n.lastHop = st.LastHop.Seq
		if n.race != nil {
			n.countHop(st)
		}
	}
	if st.Sub == engine.SubRaceEnd && n.race != nil {
		n.endRace(st)
	}
}

func (n *night) startRace(st engine.State) {
	n.race = &RaceStat{Round: st.Round, Heat: st.Heat, Frogs: len(st.Ranking), TrackLen: st.Track.Length,
		Level: (st.Round - 1) % len(n.g.Config().Levels)}
	for _, f := range st.Ranking {
		if _, ok := n.seatOf[f]; ok {
			n.race.People++
		}
	}
	n.raceStart, n.leaders, n.spreads, n.leader = n.now, nil, nil, st.Ranking[0]
	n.trace = newTrace(st.Ranking, st.Track.Length)
}

func (n *night) countHop(st engine.State) {
	r, hop := n.race, st.LastHop
	r.Hops++
	if hop.Back {
		r.BackHops++
	}
	if hop.Perfect {
		r.Perfects++
	}
	r.Bonuses += hop.Bonus
	if _, ok := n.seatOf[hop.Frog]; ok {
		r.PersonHops++
	}
	if sp := hop.Special; sp != nil {
		switch sp.Kind {
		case engine.SpotLaunch:
			r.Launches++
		case engine.SpotChute:
			r.Chutes++
		}
	}
	n.spreads = append(n.spreads, spread(st.Stacks))
	changed := st.Ranking[0] != n.leader
	if changed {
		r.LeadChanges++
		n.leader = st.Ranking[0]
	}
	n.leaders = append(n.leaders, n.leader)
	n.trace.hop(st, changed)
}

// spread is how many coasters lie between the last frog and the first.
func spread(stacks [][]string) int {
	first, last := -1, -1
	for i, stack := range stacks {
		if len(stack) > 0 {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	return last - first
}

func (n *night) endRace(st engine.State) {
	r := n.race
	r.Rotations = float64(r.Hops) / float64(max(1, r.Frogs))
	r.Duration = n.now.Sub(n.raceStart)
	r.Winner = -1
	if i, ok := n.seatOf[st.Winner]; ok {
		r.Winner = i
	}
	r.MidLeaderWon = n.leaders[len(n.leaders)/2] == st.Winner
	r.MidSpread = float64(n.spreads[len(n.spreads)/2])
	if k := len(n.leaders) - r.Frogs; k > 0 {
		r.LastRotWon = n.leaders[k-1] == st.Winner
	}
	r.Fun = n.trace.finish(st.Winner)
	n.res.Races = append(n.res.Races, *r)
	n.race = nil
}

// decide asks the seat whose turn it is, once per turn, and queues its
// commands (with the seat's flick skill deciding which flicks are perfect).
func (n *night) decide(st engine.State) {
	if st.Sub != engine.SubFlick {
		return
	}
	i, ok := n.seatOf[st.Current]
	if !ok {
		return
	}
	k := fmt.Sprint(st.Round, st.Heat, st.Turn)
	if k == n.asked {
		return
	}
	n.asked = k
	cmds, after := n.seats[i].Agent.Act(n.g, st.Current)
	for _, c := range cmds {
		c.Player = st.Current
		if c.Type == engine.CmdFlick && n.seats[i].Skill > 0 {
			// a trailing frog may get a wider band (rules.catch_up_band)
			p := bandSkill(n.seats[i].Skill, n.g.Config().Rules.FlickBand, n.g.FlickBandFor(st.Current))
			c.Perfect = n.skillRNG.Float64() < p
		}
		n.queue = append(n.queue, pending{at: n.now.Add(after), key: turnKey(n.g), cmd: c})
	}
}

// tick moves the clock to whatever happens next: a queued command, or the
// engine's next deadline.
func (n *night) tick() error {
	next := n.g.NextDeadline()
	if len(n.queue) > 0 && (next.IsZero() || !n.queue[0].at.After(next)) {
		p := n.queue[0]
		n.queue = n.queue[1:]
		if p.at.After(n.now) {
			n.now = p.at
		}
		n.g.Advance(n.now)
		if turnKey(n.g) == p.key {
			n.g.Apply(p.cmd, n.now)
		}
		return nil
	}
	if next.IsZero() {
		return fmt.Errorf("racing with nothing to wait for (sub %s)", n.g.Sub())
	}
	n.now = next
	n.g.Advance(n.now)
	return nil
}

func (n *night) result() NightResult {
	final := n.g.Snapshot()
	for _, p := range final.Players {
		if i, ok := n.seatOf[p.ID]; ok {
			n.res.Scores[i] = p.Score
		}
	}
	if i, ok := n.seatOf[final.Champion]; ok {
		n.res.Champion = i
	}
	n.res.GameTime = n.now.Sub(epoch)
	return n.res
}

// SeatsFor builds seats for a list of strategy names, each with its own RNG.
func SeatsFor(names []string, seed uint64) ([]Seat, error) {
	seats := make([]Seat, len(names))
	for i, n := range names {
		a, err := New(n, rand.New(rand.NewPCG(seed, uint64(i)+1)))
		if err != nil {
			return nil, fmt.Errorf("seat %d: %w", i, err)
		}
		seats[i] = Seat{Strategy: n, Agent: a}
	}
	return seats, nil
}

// Crowd is n seats all playing one strategy.
func Crowd(n int, strategy string, seed uint64) ([]Seat, error) {
	names := make([]string, n)
	for i := range names {
		names[i] = strategy
	}
	return SeatsFor(names, seed)
}
