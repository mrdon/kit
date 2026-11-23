package engine

import (
	"fmt"
	"slices"
)

// Rubber-band knobs: small rule tweaks that keep the pack together. Every
// one is off at its zero value, so a config that leaves them out plays
// exactly as before. cmd/hopsim -fun rates them (docs/hoppers-fun-metrics.md).

// MaxCatchUpBand caps how wide the green band can grow for a trailing frog.
const MaxCatchUpBand = 0.9

// slipChance is the chance id turns around on this hop.
func (g *Game) slipChance(id string) float64 {
	r := g.cfg.Rules
	switch {
	case g.backward[id]:
		return r.TurnBack
	case r.LeaderSlip > 0 && g.leader() == id:
		return r.LeaderSlip
	default:
		return r.SlipChance
	}
}

// hopRange is the longest roll id can make on this hop (before any perfect
// flick bonus): backward hops and the leader's hops can be shorter.
func (g *Game) hopRange(id string, back bool) int {
	r := g.cfg.Rules
	switch {
	case back && r.BackMaxRoll > 0:
		return r.BackMaxRoll
	case !back && r.LeaderMaxRoll > 0 && g.leader() == id:
		return r.LeaderMaxRoll
	default:
		return r.MaxRoll
	}
}

// trailBonus is the extra coasters id's forward hop gets for being in last
// place: on the rearmost occupied spot while some frog is further on.
func (g *Game) trailBonus(id string) int {
	r := g.cfg.Rules
	if r.TrailBonus <= 0 {
		return 0
	}
	pos, _ := g.position(id)
	lead, _ := g.position(g.leader())
	if pos < 0 || pos >= lead || slices.ContainsFunc(g.stacks[:pos], func(st []string) bool { return len(st) > 0 }) {
		return 0
	}
	return r.TrailBonus
}

// FlickBandFor is the green band width id's phone shows: FlickBand, widened
// for a frog behind the leader by CatchUpBand times FlickBand per whole track
// length behind (so half a track behind with CatchUpBand 2 doubles it), up to
// MaxCatchUpBand. With CatchUpBand 0 it is FlickBand for everyone.
func (g *Game) FlickBandFor(id string) float64 {
	r := g.cfg.Rules
	if r.CatchUpBand <= 0 {
		return r.FlickBand
	}
	pos, _ := g.position(id)
	lead, _ := g.position(g.leader())
	n := g.track().Length
	if pos < 0 || lead < 0 || n <= 0 || lead <= pos {
		return r.FlickBand
	}
	gap := float64(lead-pos) / float64(n)
	return min(MaxCatchUpBand, r.FlickBand*(1+r.CatchUpBand*gap))
}

// validateTweaks checks the rubber-band knobs.
func validateTweaks(r Rules) []error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if r.BackMaxRoll < 0 || r.BackMaxRoll > max(1, r.MaxRoll) {
		bad("rules.back_max_roll: %d, want 0 (same as max_roll) or 1..%d", r.BackMaxRoll, r.MaxRoll)
	}
	if r.LeaderMaxRoll < 0 || r.LeaderMaxRoll > max(1, r.MaxRoll) {
		bad("rules.leader_max_roll: %d, want 0 (same as max_roll) or 1..%d", r.LeaderMaxRoll, r.MaxRoll)
	}
	if r.LeaderSlip < 0 || r.LeaderSlip > 1 {
		bad("rules.leader_slip: %g, want 0 (same as slip_chance) or up to 1", r.LeaderSlip)
	}
	if r.CatchUpBand < 0 || r.CatchUpBand > 20 {
		bad("rules.catch_up_band: %g, want 0 (off) to 20", r.CatchUpBand)
	}
	if r.TrailBonus < 0 || r.TrailBonus > 3 {
		bad("rules.trail_bonus: %d, want 0 (off) to 3", r.TrailBonus)
	}
	return errs
}
