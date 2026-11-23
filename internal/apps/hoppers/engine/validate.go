package engine

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"time"
)

// Valid reports whether k is a known kind.
func (k SpotKind) Valid() bool { return slices.Contains(SpotKinds, k) }

// Valid reports whether l is a known layout.
func (l Layout) Valid() bool { return slices.Contains(Layouts, l) }

// Validate reports every problem with the config, one per line.
func (c GameConfig) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	r := c.Rules
	if r.Rounds < 1 {
		bad("rules.rounds: %d, want at least 1", r.Rounds)
	}
	if r.HeatSize < 1 {
		bad("rules.heat_size: %d, want at least 1", r.HeatSize)
	}
	if r.MinFrogs < 0 || r.MinFrogs > len(botFrogs) {
		bad("rules.min_frogs: %d, want 0..%d (there are %d bots)", r.MinFrogs, len(botFrogs), len(botFrogs))
	}
	if r.FlickTimeout < time.Second {
		bad("rules.flick_timeout: %s, want at least 1s (write it like 8s)", r.FlickTimeout)
	}
	if r.FlickBand <= 0 || r.FlickBand >= 1 {
		bad("rules.flick_band: %g, want between 0 and 1", r.FlickBand)
	}
	if r.MaxRoll < 1 {
		bad("rules.max_roll: %d, want at least 1", r.MaxRoll)
	}
	if r.SkillCap < 1 {
		bad("rules.skill_cap: %d, want at least 1", r.SkillCap)
	}
	if r.SlipChance < 0 || r.SlipChance > 1 {
		bad("rules.slip_chance: %g, want 0..1", r.SlipChance)
	}
	if r.TurnBack < 0 || r.TurnBack > 1 {
		bad("rules.turn_back: %g, want 0..1", r.TurnBack)
	}
	if len(r.PlacePoints) == 0 {
		bad("rules.place_points: empty, want points for at least 1st place")
	}
	for i, p := range r.PlacePoints {
		if p < 0 {
			bad("rules.place_points[%d]: %d, points can't be negative", i, p)
		}
	}
	errs = append(errs, validateTweaks(r)...)
	if len(c.Levels) == 0 {
		bad("levels: empty, want at least one level")
	}
	for i, l := range c.Levels {
		if err := l.Validate(); err != nil {
			bad("levels[%d]: %w", i, err)
		}
	}
	return errors.Join(errs...)
}

// Validate checks one level.
func (l Level) Validate() error {
	var errs []error
	if !l.Layout.Valid() {
		errs = append(errs, fmt.Errorf("layout: unknown %q (want one of %v)", l.Layout, Layouts))
	}
	if err := l.Track.Validate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Validate checks a track: specials sit strictly between start and finish,
// one per spot, launches go forward and chutes back to a plain spot on the
// track (no chains).
func (t Track) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	n := t.Length
	if n < MinTrackLen || n > MaxTrackLen {
		bad("track.length: %d, want %d..%d", n, MinTrackLen, MaxTrackLen)
		return errors.Join(errs...)
	}
	if t.Random != nil {
		if len(t.Specials) > 0 {
			bad("track: has both specials and random; pick one")
		}
		if t.Random.Launches < 0 || t.Random.Chutes < 0 {
			bad("track.random: counts can't be negative")
		} else if _, err := RandomTrack(rand.New(rand.NewPCG(1, 2)), n, t.Random.Launches, t.Random.Chutes); err != nil {
			bad("track.random: %w", err)
		}
	}
	at := map[int]int{}
	for i, s := range t.Specials {
		at[s.At] = i
	}
	for i, s := range t.Specials {
		p := fmt.Sprintf("track.specials[%d]", i)
		if !s.Kind.Valid() {
			bad("%s: unknown kind %q (want one of %v)", p, s.Kind, SpotKinds)
		}
		switch {
		case s.At == 0:
			bad("%s: at 0 is the start; specials go on 1..%d", p, n-1)
		case s.At == n:
			bad("%s: at %d is the finish; specials go on 1..%d", p, n, n-1)
		case s.At < 0 || s.At > n:
			bad("%s: at %d is off the track (0..%d)", p, s.At, n)
		}
		if s.To < 0 || s.To >= n {
			bad("%s: to %d is out of range (0..%d; the finish can't be a destination)", p, s.To, n-1)
		}
		if s.Kind == SpotLaunch && s.To <= s.At {
			bad("%s: launch at %d goes to %d; a launch must go forward", p, s.At, s.To)
		}
		if s.Kind == SpotChute && s.To >= s.At {
			bad("%s: chute at %d goes to %d; a chute must go back", p, s.At, s.To)
		}
		if j := at[s.At]; j != i {
			bad("%s: spot %d already has specials[%d]; one special per spot", p, s.At, j)
		}
		if j, ok := at[s.To]; ok && j != i {
			bad("%s: goes to %d, which is specials[%d]; specials can't chain", p, s.To, j)
		}
	}
	return errors.Join(errs...)
}
