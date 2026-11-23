package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"time"
)

// SaveVersion is the save format's schema version. Bump it when savedGame
// changes in a way older code can't read, and teach Restore the old one.
const SaveVersion = 1

// savedGame is the whole game as JSON: every private field of Game except
// the per-server Options (join URL, build id, speed, seen window) and the
// transient dirty flag.
type savedGame struct {
	Version      int        `json:"version"`      // SaveVersion
	StateVersion uint64     `json:"stateVersion"` // Game.Version()
	Config       GameConfig `json:"config"`
	RNG          []byte     `json:"rng"` // rand.PCG MarshalBinary
	Now          time.Time  `json:"now"`
	Quiet        bool       `json:"quiet,omitempty"`

	Players  []Player             `json:"players"` // in join order
	LastSeen map[string]time.Time `json:"lastSeen,omitempty"`

	Phase     Phase      `json:"phase"`
	Sub       Sub        `json:"sub"`
	Round     int        `json:"round"`
	Heats     []Heat     `json:"heats"`
	Heat      int        `json:"heat"`
	Turn      int        `json:"turn"`
	Stacks    [][]string `json:"stacks"`
	Order     []string   `json:"order"`
	NextOrder []string   `json:"nextOrder"`
	HopIndex  int        `json:"hopIndex"`
	Finished  bool       `json:"finished"`
	Deadline  time.Time  `json:"deadline"`
	BotAt     time.Time  `json:"botAt"`
	Winner    string     `json:"winner"`
	Champion  string     `json:"champion"`

	Backward map[string]bool `json:"backward"`
	Results  []Result        `json:"results"`

	EventSeq int      `json:"eventSeq"`
	Events   []Event  `json:"events"`
	LastHop  *HopInfo `json:"lastHop"`
}

// Save is the whole game, RNG state included, as JSON: Restore it (here or
// in another process) and it plays on exactly as this one would. Options
// (join URL, build id, speed, seen window) are not saved; pass them to
// RestoreWith.
func (g *Game) Save() ([]byte, error) {
	rng, err := g.src.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("save rng: %w", err)
	}
	c := g.deepCopy()
	s := savedGame{
		Version: SaveVersion, StateVersion: c.version, Config: c.cfg, RNG: rng, Now: c.now, Quiet: c.quiet,
		LastSeen: c.lastSeen,
		Phase:    c.phase, Sub: c.sub, Round: c.round, Heat: c.heat, Turn: c.turn, Stacks: c.stacks,
		Order: c.order, NextOrder: c.nextOrder, HopIndex: c.hopIndex, Finished: c.finished,
		Deadline: c.deadline, BotAt: c.botAt, Winner: c.winner, Champion: c.champion,
		Backward: c.backward, Results: c.results, EventSeq: c.eventSeq, Events: c.events, LastHop: c.lastHop,
	}
	for _, id := range c.joinSeq {
		s.Players = append(s.Players, *c.players[id])
	}
	for _, h := range c.heats {
		s.Heats = append(s.Heats, *h)
	}
	data, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("save game: %w", err)
	}
	return data, nil
}

// Restore rebuilds a game saved with Save, with no Options.
func Restore(data []byte) (*Game, error) { return RestoreWith(data, Options{}) }

// RestoreWith rebuilds a game saved with Save under this server's options.
// Use the same Speed and SeenWindow the game was started with.
func RestoreWith(data []byte, opts Options) (*Game, error) {
	var s savedGame
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("restore game: %w", err)
	}
	if s.Version != SaveVersion {
		return nil, fmt.Errorf("restore game: save version %d, want %d", s.Version, SaveVersion)
	}
	if err := s.Config.Validate(); err != nil {
		return nil, fmt.Errorf("restore game: config: %w", err)
	}
	src := &rand.PCG{}
	if err := src.UnmarshalBinary(s.RNG); err != nil {
		return nil, fmt.Errorf("restore game: rng: %w", err)
	}
	g := &Game{
		cfg: s.Config, opts: opts, src: src, rng: rand.New(src), now: s.Now, quiet: s.Quiet, version: s.StateVersion,
		players: map[string]*Player{}, lastSeen: maps.Clone(s.LastSeen),
		phase: s.Phase, sub: s.Sub, round: s.Round, heat: s.Heat, turn: s.Turn, stacks: s.Stacks,
		order: s.Order, nextOrder: s.NextOrder, hopIndex: s.HopIndex, finished: s.Finished,
		deadline: s.Deadline, botAt: s.BotAt, winner: s.Winner, champion: s.Champion,
		backward: s.Backward, results: s.Results, eventSeq: s.EventSeq, events: s.Events, lastHop: s.LastHop,
	}
	for _, p := range s.Players {
		g.players[p.ID] = &p
		g.joinSeq = append(g.joinSeq, p.ID)
	}
	for _, h := range s.Heats {
		g.heats = append(g.heats, &h)
	}
	if err := g.restoreCheck(); err != nil {
		return nil, fmt.Errorf("restore game: %w", err)
	}
	return g, nil
}

// restoreCheck fills empty maps and rejects a save that would crash the
// engine (a hand-edited or truncated blob).
func (g *Game) restoreCheck() error {
	if g.lastSeen == nil {
		g.lastSeen = map[string]time.Time{}
	}
	if g.backward == nil {
		g.backward = map[string]bool{}
	}
	switch g.phase {
	case PhaseLobby, PhaseRacing, PhaseFinal:
	default:
		return fmt.Errorf("unknown phase %q", g.phase)
	}
	switch g.sub {
	case "", SubReady, SubFlick, SubHop, SubRaceEnd:
	default:
		return fmt.Errorf("unknown sub %q", g.sub)
	}
	if g.round < 0 || g.heat < 0 || g.hopIndex < 0 {
		return errors.New("negative round, heat or turn index")
	}
	if want := g.track().Length + 1; len(g.stacks) != want {
		return fmt.Errorf("%d stacks, want %d for the track", len(g.stacks), want)
	}
	for _, st := range g.stacks {
		for _, f := range st {
			if g.players[f] == nil {
				return fmt.Errorf("frog %q on the track isn't a player", f)
			}
		}
	}
	for _, h := range g.heats {
		for _, f := range h.Frogs {
			if g.players[f] == nil {
				return fmt.Errorf("frog %q in %s isn't a player", f, h.Name)
			}
		}
	}
	return nil
}
