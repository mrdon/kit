// Package engine is the Hoppers game rules with no I/O: no websockets, no
// rendering, no locks and no wall clock. Callers drive it with Commands and
// timestamps (Apply / Advance) and read Snapshots or per-surface Views.
// Randomness comes from a seeded RNG, so a game is fully reproducible, and the
// whole game (RNG included) saves to JSON and restores (Save / Restore). The
// live server wraps it with a mutex and real time; the simulator (cmd/hopsim)
// fast-forwards a virtual clock from one NextDeadline to the next and plays
// thousands of games headless.
//
// The rules in one sentence: flick your frog on your turn (a flick at just the
// right strength is a perfect flick, +1 coaster); first to the finish wins; frogs
// stack and sometimes hop the wrong way. Turns simply rotate: every frog hops
// once per rotation, in a fresh random order each time.
//
// A night is Rounds rounds. Each round every person races once: in one race
// when the crowd fits (bots fill the field up to MinFrogs), or split into
// heats of at most HeatSize that run one after another. Every finishing place
// scores and points add up over the night; most points is the champion. With
// heats, the last round is seeded by standings: the top scorers race last, in
// The Final.
//
// Files: engine.go (types, NewGame), api.go (commands, clock), players.go
// (joining, connections, bots, colours), heats.go (rounds and heats), race.go
// (turns and timers), hop.go (one hop), announce.go (announcer lines),
// scoring.go (places, points, standings), state.go / view.go (snapshots and
// per-surface views), save.go (save / restore), packs.go (built-in configs).
package engine

import (
	"math/rand/v2"
	"time"
)

// Phase is the coarse state of the night.
type Phase string

const (
	PhaseLobby  Phase = "lobby"  // frogs joining, waiting for host to start round 1
	PhaseRacing Phase = "racing" // a round is running (one heat at a time)
	PhaseFinal  Phase = "final"  // all rounds done, champion + final standings
)

// Sub is the step within a round ("" outside one).
type Sub string

const (
	SubReady   Sub = "ready"   // countdown before a race's first hop
	SubFlick   Sub = "flick"   // waiting for the current frog's owner to flick
	SubHop     Sub = "hop"     // hop animation + announcer beat
	SubRaceEnd Sub = "raceEnd" // a heat's race is over: places, points, standings, until the host moves on
)

// HeatStatus is where a heat is in the round.
type HeatStatus string

const (
	HeatWaiting HeatStatus = "waiting"
	HeatNext    HeatStatus = "next"
	HeatRacing  HeatStatus = "racing"
	HeatDone    HeatStatus = "done"
)

// ResultKind is how a frog finished a race.
type ResultKind string

const ResultPlace ResultKind = "place" // finished in a place (the only kind today)

// MsgType tags messages the server sends to browsers.
type MsgType string

const MsgState MsgType = "state" // a full snapshot

// DefaultPlacePoints: every place scores, so passing anyone matters, and the
// winner gets a clear jump over 2nd. Places past the end of the list score the
// last entry (so a field bigger than the list still gets 1 each).
var DefaultPlacePoints = []int{11, 9, 8, 7, 6, 5, 4, 3, 2, 1}

// BandWidth is the width of the green "perfect" band on the phone's strength
// meter, as a share of the meter (play.html uses the same number). A perfect
// flick on a forward hop earns one coaster (none going the wrong way). The skill
// is in hitting the band; tuned in cmd/hopsim -table.
const BandWidth = 0.12

// FlickSpot is the centre of the perfect band on the power scale (0..1): a
// firm flick, but not a full-strength one. It never moves, so a regular can
// learn it; only phones are told where it is (State.FlickSpot in the player
// view), never the TV.
const FlickSpot = 0.62

// DefaultSeenWindow is how long a phone can go unseen (no command, no Seen
// heartbeat) before it counts as gone, for servers that turn connection
// timeouts on (Options.SeenWindow). An SSE ping every 8s fits twice.
const DefaultSeenWindow = 20 * time.Second

// Options are per-server settings that are not part of a saved night: Save
// leaves them out, and RestoreWith takes them again.
type Options struct {
	Speed float64 // >1 speeds up every timer (fast test runs); 0 or 1 = normal
	// SeenWindow, if set, makes a person disconnected once they haven't been
	// seen (a command or Seen) for this long. 0 = only "leave" disconnects.
	SeenWindow time.Duration
	JoinURL    string
	Build      string // server build id; clients reload when it changes
}

type Player struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	Hat       string `json:"hat"`
	Bot       bool   `json:"bot"`
	Score     int    `json:"score"`
	Connected bool   `json:"connected"`
	Heat      int    `json:"heat"` // 1-based heat this round (index into State.Heats + 1); 0 = not racing this round
	Wins      int    `json:"wins"` // races (heats) won tonight; heat winners wear a prize rosette
}

// Result is one frog's finish in a race.
type Result struct {
	Player string     `json:"player"`
	Kind   ResultKind `json:"kind"` // always ResultPlace
	Frog   string     `json:"frog"` // same as Player (kept for older clients)
	Place  int        `json:"place"`
	Won    bool       `json:"won"` // scored points
	Points int        `json:"points"`
}

// Heat is one race within a round.
type Heat struct {
	Num     int        `json:"num"`   // 1-based run order
	Name    string     `json:"name"`  // "Heat 2", "The Final", or "Race 3" when the round is one race
	Final   bool       `json:"final"` // the top heat of the ladder round
	Frogs   []string   `json:"frogs"` // everyone racing in it, people and bots
	Status  HeatStatus `json:"status"`
	Winner  string     `json:"winner"`  // frog that reached the finish
	Results []Result   `json:"results"` // places, once done
}

// Standing is one person's line in the night's table.
type Standing struct {
	Player string `json:"player"`
	Rank   int    `json:"rank"`
	Points int    `json:"points"`
	Wins   int    `json:"wins"`
}

type Event struct {
	Seq  int    `json:"seq"`
	Text string `json:"text"`
}

type HopInfo struct {
	Seq      int      `json:"seq"`
	Frog     string   `json:"frog"`
	From     int      `json:"from"`
	To       int      `json:"to"`
	Roll     int      `json:"roll"`
	Moved    []string `json:"moved"`
	LandedOn string   `json:"landedOn"`
	Under    string   `json:"under"` // slipped backwards and slid UNDER this frog's stack
	Back     bool     `json:"back"`
	Turned   bool     `json:"turned"` // slipped on this hop and changed direction
	Auto     bool     `json:"auto"`
	Power    float64  `json:"power,omitempty"` // flick strength the phone sent (0..1), recorded only
	Perfect  bool     `json:"perfect"`         // a perfect flick won the bonus (same as Bonus > 0); celebrate only then
	Bonus    int      `json:"bonus"`           // coasters the perfect flick won: 1 or 0 (Roll already includes it). Only forward hops win one
	// Landed is the spot the hop itself reached. It equals To unless the frog
	// landed on a special, which then carried it (and its riders) to To.
	Landed  int      `json:"landed"`
	Special *Special `json:"special"` // the launch pad or chute it hit, or nil
}

// Game is one night. Every field is private; Save / Restore carry all of
// them (see save.go), so add any new field there too.
type Game struct {
	cfg     GameConfig // defaults filled in, random specials resolved
	opts    Options
	src     *rand.PCG // the RNG's state, kept so Save can marshal it
	rng     *rand.Rand
	now     time.Time // the time of the command/advance currently being processed
	dirty   bool      // state changed since the last Apply/Advance returned
	quiet   bool      // lookahead clone: skip announcer text
	version uint64    // bumped on every visible change

	players  map[string]*Player
	joinSeq  []string
	lastSeen map[string]time.Time // people only: last command or heartbeat

	phase     Phase
	sub       Sub
	round     int
	heats     []*Heat
	heat      int        // index into heats of the heat racing / up next
	turn      int        // turns taken this race (the current one included)
	stacks    [][]string // stacks[space] = frog IDs, bottom to top
	order     []string   // this rotation's hop order
	nextOrder []string   // the next rotation's, drawn ahead so "up next" works across the boundary
	hopIndex  int
	finished  bool
	deadline  time.Time
	botAt     time.Time
	winner    string
	champion  string

	backward map[string]bool // frogs currently turned around
	results  []Result        // the last race's places

	eventSeq int
	events   []Event
	lastHop  *HopInfo
}

var botFrogs = []Player{
	{Name: "Sir Hops-a-Lot", Color: "#2ecc71", Hat: "crown"},
	{Name: "Lily Padsworth", Color: "#e84393", Hat: "flower"},
	{Name: "Big Croaker", Color: "#e67e22", Hat: "cowboy"},
	{Name: "Ribbit Ross", Color: "#3498db", Hat: "beanie"},
	{Name: "Toadally Tina", Color: "#9b59b6", Hat: "party"},
	{Name: "Puddle Jumper", Color: "#f1c40f", Hat: "viking"},
	{Name: "Swamp Thing", Color: "#1abc9c", Hat: "propeller"},
	{Name: "Leapin' Larry", Color: "#e74c3c", Hat: "tophat"},
}

// NewGame starts a night. Zero fields in cfg take their Default() value
// (except SlipChance and MinFrogs, where 0 means none), and random specials
// are placed from seed. It panics on a config that fails Validate: check
// configs from people with Validate (LoadConfig/ParseConfig already do).
func NewGame(cfg GameConfig, seed uint64) *Game { return NewGameWith(cfg, Options{}, seed) }

// NewGameWith is NewGame with per-server options.
func NewGameWith(cfg GameConfig, opts Options, seed uint64) *Game {
	cfg = cfg.resolve(rand.New(rand.NewPCG(seed, 0x7261636b)))
	if err := cfg.Validate(); err != nil {
		panic("hoppers: bad config: " + err.Error())
	}
	src := newSource(seed)
	g := &Game{
		cfg:      cfg,
		opts:     opts,
		src:      src,
		rng:      rand.New(src),
		players:  map[string]*Player{},
		lastSeen: map[string]time.Time{},
		phase:    PhaseLobby,
		backward: map[string]bool{},
	}
	g.syncBots()
	g.resetStacks()
	return g
}

// newSource is the game RNG's source for a seed.
func newSource(seed uint64) *rand.PCG { return rand.NewPCG(seed, seed^0x9e3779b97f4a7c15) }

// levelIndex is the level for the current round (the first in the lobby; the
// round just raced during its beer break and the final).
func (g *Game) levelIndex() int { return max(0, g.round-1) % len(g.cfg.Levels) }

func (g *Game) level() *Level { return &g.cfg.Levels[g.levelIndex()] }

func (g *Game) track() *Track { return &g.level().Track }

// specialAt is the special on spot s of the current track, if any.
func (g *Game) specialAt(s int) (Special, bool) {
	for _, sp := range g.track().Specials {
		if sp.At == s {
			return sp, true
		}
	}
	return Special{}, false
}

// changed marks a visible change: Apply / Advance report it and the version
// counter moves on.
func (g *Game) changed() {
	g.dirty = true
	g.version++
}

func (g *Game) resetStacks() {
	g.stacks = make([][]string, g.track().Length+1)
}

// dur scales a timer by the configured speed (fast test runs).
func (g *Game) dur(d time.Duration) time.Duration {
	if g.opts.Speed > 1 {
		return time.Duration(float64(d) / g.opts.Speed)
	}
	return d
}
