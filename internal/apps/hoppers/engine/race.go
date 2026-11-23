package engine

import (
	"slices"
	"time"
)

// Flick hops the current frog if it's id's turn. power is the flick's
// strength from the phone (0..1, clamped, recorded only); perfect means it
// landed in the green band, which may earn a bonus coaster (people only).
func (g *Game) Flick(id string, power float64, perfect bool) {
	if g.phase != PhaseRacing || g.sub != SubFlick || g.current() != id {
		return
	}
	if p := g.players[id]; p == nil || p.Bot {
		perfect = false
	}
	g.doHop(false, min(1, max(0, power)), perfect)
	g.changed()
}

// HostAdvance is the host's one button: start the night, move on from a
// race's results (next heat, next round, or the final standings), or reset
// after the final. Mid-race it does nothing.
func (g *Game) HostAdvance() {
	switch g.phase {
	case PhaseLobby:
		g.startRound()
	case PhaseRacing:
		switch g.sub {
		case SubRaceEnd:
			g.afterRace()
		case SubReady, SubFlick, SubHop:
			return
		}
	case PhaseFinal:
		g.resetNight()
	}
	g.changed()
}

// canHostAdvance reports whether the host's button does anything now.
func (g *Game) canHostAdvance() bool {
	return g.phase != PhaseRacing || g.sub == SubRaceEnd
}

// resetNight goes back to the lobby after the final, keeping everyone.
func (g *Game) resetNight() {
	for _, p := range g.players {
		p.Score, p.Wins, p.Heat = 0, 0, 0
	}
	g.round = 0
	g.heats, g.heat = nil, 0
	g.champion, g.winner = "", ""
	g.results = nil
	g.phase = PhaseLobby
	g.syncBots()
	g.resetStacks()
	g.say("New game. Scan to join.")
}

// --- Turns -----------------------------------------------------------------

// shuffledFrogs is every frog on the track in random order.
func (g *Game) shuffledFrogs() []string {
	ids := g.racingFrogs()
	g.rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	return ids
}

// nextTurn moves to the next frog, rolling into the next rotation when
// everyone has hopped. The order is fixed for the whole race; anyone who
// joined mid-race goes at the end of it.
func (g *Game) nextTurn() {
	g.hopIndex++
	if g.hopIndex >= len(g.order) {
		for _, f := range g.racingFrogs() {
			if !slices.Contains(g.nextOrder, f) {
				g.nextOrder = append(g.nextOrder, f)
			}
		}
		g.order, g.hopIndex = g.nextOrder, 0
		g.nextOrder = slices.Clone(g.order)
	}
	g.beginHop()
}

// turnOrder is who hops from now on: the current frog first, then the rest
// of this rotation and all of the next.
func (g *Game) turnOrder() []string {
	if g.phase != PhaseRacing || g.sub == SubRaceEnd || g.hopIndex >= len(g.order) {
		return []string{}
	}
	return append(slices.Clone(g.order[g.hopIndex:]), g.nextOrder...)
}

func (g *Game) racingFrogs() []string {
	var ids []string
	for _, st := range g.stacks {
		ids = append(ids, st...)
	}
	return ids
}

func (g *Game) current() string {
	if g.phase == PhaseRacing && g.hopIndex < len(g.order) {
		return g.order[g.hopIndex]
	}
	return ""
}

func (g *Game) onDeck() string {
	if t := g.turnOrder(); len(t) > 1 && g.sub != SubRaceEnd {
		return t[1]
	}
	return ""
}

func (g *Game) beginHop() {
	g.sub = SubFlick
	g.turn++
	cur := g.current()
	now := g.now
	g.deadline = now.Add(g.dur(g.cfg.Rules.FlickTimeout))
	p := g.players[cur]
	if p == nil || p.Bot || !p.Connected {
		g.botAt = now.Add(g.dur(time.Duration(1000+g.rng.IntN(1000)) * time.Millisecond))
	} else {
		g.botAt = time.Time{}
	}
}

func (g *Game) position(id string) (space, idx int) {
	for s, st := range g.stacks {
		for i, f := range st {
			if f == id {
				return s, i
			}
		}
	}
	return -1, -1
}

func (g *Game) leader() string {
	for s := len(g.stacks) - 1; s >= 0; s-- {
		if n := len(g.stacks[s]); n > 0 {
			return g.stacks[s][n-1]
		}
	}
	return ""
}

// ranking returns frogs from first place to last: further along the track
// first, and on a shared coaster the top of the stack ahead of those below.
func (g *Game) ranking() []string {
	var r []string
	for s := len(g.stacks) - 1; s >= 0; s-- {
		st := g.stacks[s]
		for i := len(st) - 1; i >= 0; i-- {
			r = append(r, st[i])
		}
	}
	return r
}

func (g *Game) backwardIDs() []string {
	ids := []string{}
	for id, b := range g.backward {
		if b {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// --- Timers ----------------------------------------------------------------

// timerDue is when the race's own clock next fires (countdown, bot hop, auto
// hop, hop animation), or zero when waiting on the host (lobby, results,
// final).
func (g *Game) timerDue() time.Time {
	if g.phase != PhaseRacing {
		return time.Time{}
	}
	if g.sub == SubFlick && !g.botAt.IsZero() && g.botAt.Before(g.deadline) {
		return g.botAt
	}
	return g.deadline
}

// step processes at most one timed transition that is due at g.now.
func (g *Game) step() {
	if g.phase != PhaseRacing {
		return
	}
	now := g.now
	if g.sub == SubFlick {
		if !g.botAt.IsZero() && !now.Before(g.botAt) {
			g.doHop(false, 0, false)
			g.changed()
		} else if !now.Before(g.deadline) {
			g.doHop(true, 0, false)
			g.changed()
		}
		return
	}
	if now.Before(g.deadline) {
		return
	}
	switch g.sub {
	case SubReady:
		g.beginHop()
	case SubHop:
		if g.finished {
			g.endRace(g.leader())
		} else {
			g.nextTurn()
		}
	case SubRaceEnd:
		return // waits for the host
	case SubFlick:
		return
	default:
		return
	}
	g.changed()
}
