package engine

import (
	"fmt"
	"math/rand/v2"
)

// maxLine is about two lines of the TV's announcer box, read across a bar.
const maxLine = 48

func (g *Game) say(format string, args ...any) {
	if g.quiet {
		return
	}
	g.eventSeq++
	g.events = append(g.events, Event{Seq: g.eventSeq, Text: fmt.Sprintf(format, args...)})
	if len(g.events) > 8 {
		g.events = g.events[len(g.events)-8:]
	}
}

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

// announceHop calls a hop: the main line, then extra beats, most newsworthy
// first. They drop off once the line would outgrow two lines of the TV
// announcer (a lead change always fits in). A lead change is only called once
// the new leader is past halfway: early on the bunch swaps the lead almost
// every hop and the line would stop meaning anything. The RNG draws happen
// in a fixed order (main line, special, landing), so a seed always reads the
// same.
func (g *Game) announceHop(id, oldLeader string, m hopMove, auto bool) {
	me := g.name(id)
	line := g.hopLine(me, m, auto && !g.players[id].Bot)
	if m.special {
		line = g.specialLine(me, m.sp.Kind, line)
	}
	nl := g.leader()
	at, _ := g.position(nl)
	lead := nl != oldLeader && 2*at > g.track().Length // a lead change always makes the line
	var extra []string
	if lead && nl == id {
		extra = append(extra, "Takes the lead!")
	} else if lead {
		extra = append(extra, g.name(nl)+" takes the lead!")
	}
	if m.bonus > 0 {
		extra = append(extra, "Perfect flick, +1.")
	}
	if e := g.carryLine(m); e != "" {
		extra = append(extra, e)
	}
	for i, e := range extra {
		if len(line)+1+len(e) > maxLine && (i > 0 || !lead) {
			break
		}
		line += " " + e
	}
	g.say("%s", line)
}

// hopLine is the main call for a hop. missed means a person's frog hopped by
// itself because they didn't flick in time.
func (g *Game) hopLine(me string, m hopMove, missed bool) string {
	roll := m.roll
	switch {
	case m.back && m.turned:
		return fmt.Sprintf(pick(g.rng, []string{"%s turns around. Back %d!", "%s goes the other way. Back %d!", "%s turns for no reason. Back %d!"}), me, roll)
	case m.back:
		return fmt.Sprintf(pick(g.rng, []string{"%s is still going the wrong way. Back %d.", "Wrong way, %s. Back %d.", "%s, still backwards. Back %d."}), me, roll)
	case m.turned:
		return fmt.Sprintf(pick(g.rng, []string{"%s turns back around. Forward %d.", "%s remembers where the finish is. Forward %d."}), me, roll)
	case m.bonked:
		return fmt.Sprintf("%s hits the start, turns around. Forward %d.", me, roll)
	case missed:
		return fmt.Sprintf(pick(g.rng, []string{"No flick. %s hops %d.", "Too slow, %s. Hops %d."}), me, roll)
	default:
		return fmt.Sprintf(pick(g.rng, []string{"%s hops %d.", "%s flicks a %d.", "%s jumps %d."}), me, roll)
	}
}

// specialLine replaces the main call when the frog hit a launch pad or chute.
func (g *Game) specialLine(me string, kind SpotKind, line string) string {
	switch kind {
	case SpotLaunch:
		return fmt.Sprintf(pick(g.rng, []string{"%s hits a launch pad.", "%s finds a launch pad."}), me)
	case SpotChute:
		return fmt.Sprintf(pick(g.rng, []string{"%s slides down a chute.", "%s drops down a chute."}), me)
	}
	return line
}

// carryLine is who rode along, or what the frog landed on or slid under.
func (g *Game) carryLine(m hopMove) string {
	riders := len(m.moving) - 1
	switch {
	case m.special && riders == 1:
		return fmt.Sprintf("Takes %s along.", g.name(m.moving[1]))
	case m.special && riders > 1:
		return fmt.Sprintf("Takes %d frogs along.", riders)
	case m.special && m.landedOn != "":
		return fmt.Sprintf("Lands on %s.", g.name(m.landedOn))
	case m.special:
		return ""
	case m.back && riders == 1 && m.roll > 0:
		return fmt.Sprintf("Takes %s with it.", g.name(m.moving[1]))
	case m.back && riders > 1 && m.roll > 0:
		return fmt.Sprintf("Takes %d frogs with it.", riders)
	case m.back && m.under != "":
		return fmt.Sprintf("Slides under %s.", g.name(m.under))
	case !m.back && riders == 1:
		return g.name(m.moving[1]) + " rides along."
	case !m.back && riders > 1:
		return fmt.Sprintf("%d frogs ride along.", riders)
	case m.landedOn != "":
		return fmt.Sprintf(pick(g.rng, []string{"Lands on %s's head.", "Lands on top of %s.", "On top of %s."}), g.name(m.landedOn))
	}
	return ""
}
