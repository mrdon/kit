package engine

import (
	"slices"
	"time"
)

// hopMove is one hop worked out: where the frog was going, which way, how far,
// and what it carried and hit.
type hopMove struct {
	from, to int
	landed   int // where the hop itself reached (to, unless a special moved it on)
	roll     int
	bonus    int
	turned   bool // changed direction on this hop
	bonked   bool // was going backwards, hit the start and turned around
	back     bool
	moving   []string // the frog and everyone riding on it
	landedOn string
	under    string
	special  bool
	sp       Special
}

// doHop hops the current frog: maybe it turns around, it rolls, a perfect
// flick may win a coaster, the stack moves, and the announcer calls it.
func (g *Game) doHop(auto bool, power float64, perfect bool) {
	id := g.current()
	from, idx := g.position(id)
	if from < 0 {
		g.sub = SubHop
		g.deadline = g.now
		return
	}
	m := g.rollHop(id, from, perfect && !auto)
	oldLeader := g.leader()
	g.moveStack(&m, idx)
	g.recordHop(id, m, auto, power)
	if g.finishHop(m.to) {
		return
	}
	g.announceHop(id, oldLeader, m, auto)
}

// rollHop decides direction and distance. Frogs are unpredictable: sometimes
// one turns around, and it keeps hopping the wrong way on every turn until it
// randomly turns again (or bonks the start). A perfect flick on a forward hop
// wins a coaster: one further, up to SkillCap. Going the wrong way it wins
// nothing. A missed or lazy flick never hurts.
func (g *Game) rollHop(id string, from int, perfect bool) hopMove {
	m := hopMove{from: from}
	m.turned = g.rng.Float64() < g.slipChance(id)
	if m.turned {
		g.backward[id] = !g.backward[id]
	}
	if g.backward[id] && from == 0 {
		g.backward[id] = false
		m.bonked, m.turned = true, false
	}
	m.back = g.backward[id]
	m.roll = 1 + g.rng.IntN(max(1, g.hopRange(id, m.back)))
	if perfect && !m.back && m.roll < g.cfg.Rules.SkillCap {
		m.roll, m.bonus = m.roll+1, 1
	}
	if !m.back {
		m.roll += g.trailBonus(id)
	}
	m.to = min(from+m.roll, g.track().Length)
	if m.back {
		m.to = max(from-m.roll, 0)
	}
	return m
}

// moveStack lifts the frog at idx and everyone riding it off m.from and puts
// them down at m.to. Backward hops drag riders along and slide UNDER whatever
// stack they hit. A launch pad or chute carries the frog and its riders on to
// sp.To, where they land on top of the stack, whichever way they were hopping.
func (g *Game) moveStack(m *hopMove, idx int) {
	from, to := m.from, m.to
	m.moving = append([]string{}, g.stacks[from][idx:]...)
	if to != from {
		g.stacks[from] = g.stacks[from][:idx]
	}
	m.landed = to
	m.sp, m.special = g.specialAt(to)
	switch {
	case to != from && m.special:
		m.to = m.sp.To
		m.landedOn = g.top(m.to)
		g.stacks[m.to] = append(g.stacks[m.to], m.moving...)
	case to == from:
		// a forward hop from the finish line can't move (not reached in play)
	case m.back:
		if len(g.stacks[to]) > 0 {
			m.under = g.stacks[to][0]
		}
		g.stacks[to] = append(m.moving, g.stacks[to]...)
	default:
		m.landedOn = g.top(to)
		g.stacks[to] = append(g.stacks[to], m.moving...)
	}
}

// top is the frog on top of the stack at s, or "".
func (g *Game) top(s int) string {
	if n := len(g.stacks[s]); n > 0 {
		return g.stacks[s][n-1]
	}
	return ""
}

// recordHop keeps the hop for the TV to animate. It shares the announcer's
// sequence (the next line's number) without using it up. Perfect is true only
// when the flick won the bonus coaster, so the screens celebrate exactly then.
func (g *Game) recordHop(id string, m hopMove, auto bool, power float64) {
	g.lastHop = &HopInfo{Seq: g.eventSeq + 1, Frog: id, From: m.from, To: m.to, Roll: m.roll, Moved: m.moving,
		LandedOn: m.landedOn, Under: m.under, Back: m.back, Turned: m.turned, Auto: auto, Power: power,
		Perfect: m.bonus > 0, Bonus: m.bonus, Landed: m.landed}
	if m.special {
		sp := m.sp
		g.lastHop.Special = &sp
	}
}

// finishHop records the end of a hop. In a quiet lookahead clone it also
// reports true so doHop skips building announcer text.
func (g *Game) finishHop(to int) bool {
	if to >= g.track().Length {
		g.finished = true
	}
	g.sub = SubHop
	g.deadline = g.now.Add(g.dur(2200 * time.Millisecond))
	return g.quiet
}

func (g *Game) lastHopCopy() *HopInfo {
	if g.lastHop == nil {
		return nil
	}
	h := *g.lastHop
	h.Moved = slices.Clone(h.Moved)
	if h.Special != nil {
		sp := *h.Special
		h.Special = &sp
	}
	return &h
}
