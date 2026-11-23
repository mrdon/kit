package engine

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
)

// RandomTrack places launches and chutes on a track of the given length. Frogs
// start bunched and string out, so launches sit in the first half (where the
// back of the pack is for most of a race) and chutes in the second (where the
// leaders are): both pull the field together. Hop lengths are 3..6 coasters.
// The result always passes Validate.
func RandomTrack(rng *rand.Rand, length, launches, chutes int) (Track, error) {
	mid := length / 2
	return RandomTrackIn(rng, length, launches, chutes, Zones{LaunchLo: 3, LaunchHi: mid, ChuteLo: mid, ChuteHi: length - 2})
}

// Zones are the spots RandomTrackIn may put launches and chutes on
// (inclusive; a special's landing spot may fall outside its zone).
type Zones struct{ LaunchLo, LaunchHi, ChuteLo, ChuteHi int }

// RandomTrackIn is RandomTrack with the launch and chute zones given, for
// trying other placements (cmd/hopsim -placement).
func RandomTrackIn(rng *rand.Rand, length, launches, chutes int, z Zones) (Track, error) {
	t := Track{Length: length}
	if launches < 0 || chutes < 0 {
		return t, errors.New("negative special count")
	}
	if launches+chutes == 0 {
		return t, nil
	}
	if length < MinTrackLen {
		return t, fmt.Errorf("track %d is too short", length)
	}
	if launches+chutes > length/3 {
		return t, fmt.Errorf("%d specials don't fit on a %d-coaster track (at most %d)", launches+chutes, length, length/3)
	}
	for range 1000 {
		t.Specials = t.Specials[:0]
		used := map[int]bool{}  // spots with a special
		dests := map[int]bool{} // spots something lands on
		ok := true
		place := func(kind SpotKind, lo, hi int) {
			for range 200 {
				at := lo + rng.IntN(max(1, hi-lo+1))
				d := 3 + rng.IntN(4)
				to := at + d
				if kind == SpotChute {
					to = at - d
				}
				// keep specials a coaster apart so the track reads clearly
				if at < 1 || at >= length || to < 1 || to >= length-1 || used[at] || used[at-1] || used[at+1] || dests[at] || used[to] {
					continue
				}
				used[at], dests[to] = true, true
				t.Specials = append(t.Specials, Special{At: at, Kind: kind, To: to})
				return
			}
			ok = false
		}
		for range launches {
			place(SpotLaunch, z.LaunchLo, z.LaunchHi)
		}
		for range chutes {
			place(SpotChute, z.ChuteLo, z.ChuteHi)
		}
		if ok && t.Validate() == nil {
			slices.SortFunc(t.Specials, func(a, b Special) int { return a.At - b.At })
			return t, nil
		}
	}
	return Track{Length: length}, fmt.Errorf("couldn't place %d launches and %d chutes on %d coasters", launches, chutes, length)
}

// resolve fills in defaults for zero fields (so a partly built config works)
// and replaces random specials with real ones.
func (c GameConfig) resolve(rng *rand.Rand) GameConfig {
	d := Default()
	r := &c.Rules
	if r.Rounds <= 0 {
		r.Rounds = d.Rules.Rounds
	}
	if r.HeatSize <= 0 {
		r.HeatSize = d.Rules.HeatSize
	}
	if r.FlickTimeout <= 0 {
		r.FlickTimeout = d.Rules.FlickTimeout
	}
	if r.FlickBand <= 0 {
		r.FlickBand = d.Rules.FlickBand
	}
	if r.MaxRoll <= 0 {
		r.MaxRoll = d.Rules.MaxRoll
	}
	if r.SkillCap <= 0 {
		r.SkillCap = r.MaxRoll + 1
	}
	if r.TurnBack == 0 {
		r.TurnBack = d.Rules.TurnBack
	}
	if len(r.PlacePoints) == 0 {
		r.PlacePoints = d.Rules.PlacePoints
	}
	if len(c.Levels) == 0 {
		c.Levels = d.Levels
	}
	c.Levels = slices.Clone(c.Levels)
	for i := range c.Levels {
		l := &c.Levels[i]
		if l.Layout == "" {
			l.Layout = LayoutSwitchback
		}
		if l.Track.Length <= 0 {
			l.Track.Length = 20
		}
		if rs := l.Track.Random; rs != nil {
			t, err := RandomTrack(rng, l.Track.Length, rs.Launches, rs.Chutes)
			if err != nil {
				panic(fmt.Sprintf("hoppers: level %d: %v", i, err))
			}
			l.Track = t
		}
		l.Track.Specials = slices.Clone(l.Track.Specials)
		if l.Track.Specials == nil {
			l.Track.Specials = []Special{}
		}
	}
	return c
}

// WithLength returns a copy with every level's track this long (and any
// specials that no longer fit dropped). For quick experiments (-track flags).
func (c GameConfig) WithLength(n int) GameConfig {
	c.Levels = slices.Clone(c.Levels)
	for i := range c.Levels {
		t := &c.Levels[i].Track
		t.Length = n
		t.Specials = slices.DeleteFunc(slices.Clone(t.Specials), func(s Special) bool { return s.At >= n || s.To >= n })
	}
	return c
}

// WithRandomSpecials returns a copy where every level gets random specials.
func (c GameConfig) WithRandomSpecials(launches, chutes int) GameConfig {
	c.Levels = slices.Clone(c.Levels)
	for i := range c.Levels {
		c.Levels[i].Track.Specials = nil
		c.Levels[i].Track.Random = &RandomSpecials{Launches: launches, Chutes: chutes}
	}
	return c
}
