package engine

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"time"
)

// Command is everything a player or host can do. UIs only send commands and
// render views, so a headless agent can drive a game exactly like a person.
// Player is who is acting; the server fills it in from the phone's identity.
type Command struct {
	Type CommandType `json:"type"`

	Player string `json:"player,omitempty"` // who is acting (ignored for "host")
	Name   string `json:"name,omitempty"`   // join
	Color  string `json:"color,omitempty"`  // join
	Hat    string `json:"hat,omitempty"`    // join
	// Power is the flick's strength (0..1: swipe speed in screen-heights per
	// second, scaled) and Perfect whether it landed in the phone's green band.
	// The phone judges the flick and the server trusts it (it's a bar game):
	// Power is clamped and only recorded, Perfect may earn a bonus coaster.
	Power   float64 `json:"power,omitempty"`
	Perfect bool    `json:"perfect,omitempty"`
}

// CommandType is what a Command does.
type CommandType string

const (
	CmdJoin  CommandType = "join"  // a phone joins (or edits its frog)
	CmdSeen  CommandType = "seen"  // a phone is still there (heartbeat); see Game.Seen
	CmdLeave CommandType = "leave" // a phone says it's gone (prefer letting Seen time out)
	CmdFlick CommandType = "flick" // flick your frog on your turn
	CmdHost  CommandType = "host"  // the host's one button on the TV
)

// CommandTypes is every command the engine applies.
var CommandTypes = []CommandType{CmdJoin, CmdSeen, CmdLeave, CmdFlick, CmdHost}

// Apply runs one command at time now, first processing any timers that are
// due. It reports whether the visible state changed (timers included).
func (g *Game) Apply(c Command, now time.Time) (changed bool, err error) {
	advanced := g.Advance(now)
	g.dirty = false
	switch c.Type {
	case CmdJoin:
		g.Join(c.Player, c.Name, c.Color, c.Hat)
	case CmdSeen:
		g.seen(c.Player)
	case CmdLeave:
		g.SetConnected(c.Player, false)
	case CmdFlick:
		g.markSeen(c.Player)
		g.Flick(c.Player, c.Power, c.Perfect)
	case CmdHost:
		g.HostAdvance()
	default:
		return advanced, fmt.Errorf("apply: unknown command %q", c.Type)
	}
	changed = g.dirty
	g.dirty = false
	return advanced || changed, nil
}

// Advance processes every timed transition due at or before now (several can
// cascade, e.g. a hop finishing ends the race). Reports whether state changed.
func (g *Game) Advance(now time.Time) bool {
	if now.After(g.now) {
		g.now = now
	}
	changed := false
	for range 1000 {
		g.dirty = false
		g.expireUnseen()
		g.step()
		if !g.dirty {
			break
		}
		changed = true
	}
	g.dirty = false
	return changed
}

// NextDeadline is the earliest time the game will change on its own: the
// countdown, a bot's (or an absent person's) hop, the flick timeout, the end
// of a hop's animation, or a person timing out unseen (Options.SeenWindow).
// It is zero only when the game is waiting on a command (lobby, a race's
// results, final, with nobody to time out). A driver that wakes only at NextDeadline (and on commands)
// misses nothing; a simulator jumps its clock straight here.
func (g *Game) NextDeadline() time.Time {
	t := g.timerDue()
	if e := g.nextExpiry(); !e.IsZero() && (t.IsZero() || e.Before(t)) {
		t = e
	}
	return t
}

// Version goes up on every visible change (a command or a timer). Clients
// use it to drop stale frames; servers as a stream id and poll cursor.
func (g *Game) Version() uint64 { return g.version }

// Now is the engine's current time.
func (g *Game) Now() time.Time { return g.now }

// Phase and Sub expose the coarse state without building a full snapshot.
func (g *Game) Phase() Phase { return g.phase }
func (g *Game) Sub() Sub     { return g.sub }

// Clone returns an independent deep copy with its own RNG. AI players use it
// to simulate possible futures ("what if the race played out 500 times?")
// without touching the real game.
func (g *Game) Clone(seed uint64) *Game {
	c := g.deepCopy()
	c.quiet = true
	c.src = newSource(seed)
	c.rng = rand.New(c.src)
	return c
}

// deepCopy copies everything but the RNG (which still points at g's).
func (g *Game) deepCopy() *Game {
	c := *g
	c.players = make(map[string]*Player, len(g.players))
	for id, p := range g.players {
		cp := *p
		c.players[id] = &cp
	}
	c.joinSeq = slices.Clone(g.joinSeq)
	c.lastSeen = maps.Clone(g.lastSeen)
	c.stacks = make([][]string, len(g.stacks))
	for i, s := range g.stacks {
		c.stacks[i] = slices.Clone(s)
	}
	c.order = slices.Clone(g.order)
	c.nextOrder = slices.Clone(g.nextOrder)
	c.heats = g.heatsCopy()
	c.backward = maps.Clone(g.backward)
	c.results = slices.Clone(g.results)
	c.events = slices.Clone(g.events)
	c.lastHop = g.lastHopCopy()
	return &c
}

// Leader reports the frog currently in front (top of the furthest stack).
func (g *Game) Leader() string { return g.leader() }

// Turn counts the turns taken this race, the current one included: a new
// value means a new frog's turn.
func (g *Game) Turn() int { return g.turn }

// Round is the current round number.
func (g *Game) Round() int { return g.round }

// HeatIndex is the 0-based heat racing now (or up next) within the round.
func (g *Game) HeatIndex() int { return g.heat }

// Ranking is every racing frog from first place to last.
func (g *Game) Ranking() []string { return g.ranking() }

// Config is the game's configuration (defaults filled in, random specials
// placed).
func (g *Game) Config() GameConfig { return g.cfg }

// Level is the level for the current round.
func (g *Game) Level() Level { return *g.level() }
