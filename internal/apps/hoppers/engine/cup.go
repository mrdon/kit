package engine

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
)

// The cup: a night races one level per round, like a Mario Kart cup. Before a
// night the host gets a generated config (Generate, from their Preferences)
// and can swap or tweak any race (SetLevel, RerollLevel, EditLevel); the
// GameConfig that comes out is what the game runs (YAML() to inspect or keep
// it).

// BuiltinLevels is the level library that ships with the game.
func BuiltinLevels() []Level {
	return []Level{
		{Name: "Bar Sprint", Layout: LayoutStraight, Theme: "bar", Track: Track{Length: 14}},
		{Name: "Lake Sprint", Layout: LayoutStraight, Theme: "lake", Track: Track{Length: 13}},
		{Name: "Lake Loop", Layout: LayoutSwitchback, Theme: "lake", Track: Track{Length: 20}},
		{Name: "Bar Switchback", Layout: LayoutSwitchback, Theme: "bar", Track: Track{Length: 20}},
	}
}

// Preferences are the host's taste for a night: what Generate builds a
// GameConfig from, and what gets saved between nights. Zero values mean
// "the default" (empty lists allow everything; no specials).
type Preferences struct {
	Rounds   int      `yaml:"rounds,omitempty" json:"rounds,omitempty"`
	HeatSize int      `yaml:"heat_size,omitempty" json:"heatSize,omitempty"`
	Layouts  []Layout `yaml:"layouts,omitempty" json:"layouts,omitempty"` // allowed layouts
	Themes   []string `yaml:"themes,omitempty" json:"themes,omitempty"`   // allowed themes
	// Launches and chutes to place at random on every race (0, 0 = specials off).
	Launches int `yaml:"launches,omitempty" json:"launches,omitempty"`
	Chutes   int `yaml:"chutes,omitempty" json:"chutes,omitempty"`
}

// DefaultPreferences is what a host starts from: one launch and one chute
// on every race. The zero Preferences has specials off.
func DefaultPreferences() Preferences {
	return Preferences{Launches: 1, Chutes: 1}
}

// Validate checks the preferences.
func (p Preferences) Validate() error {
	var errs []error
	if p.Rounds < 0 || p.HeatSize < 0 || p.Launches < 0 || p.Chutes < 0 {
		errs = append(errs, errors.New("preferences: counts can't be negative"))
	}
	for _, l := range p.Layouts {
		if !l.Valid() {
			errs = append(errs, fmt.Errorf("preferences.layouts: unknown %q (want one of %v)", l, Layouts))
		}
	}
	return errors.Join(errs...)
}

// allowed is the part of lib these preferences allow (all of lib if they
// allow none of it).
func (p Preferences) allowed(lib []Level) []Level {
	if len(lib) == 0 {
		lib = BuiltinLevels()
	}
	out := slices.DeleteFunc(slices.Clone(lib), func(l Level) bool {
		return (len(p.Layouts) > 0 && !slices.Contains(p.Layouts, l.Layout)) ||
			(len(p.Themes) > 0 && !slices.Contains(p.Themes, l.Theme))
	})
	if len(out) == 0 {
		return lib
	}
	return out
}

// Generate builds a night from preferences: Default() rules (with the
// preferred rounds and heat size) and a RandomCup from lib (nil: the built-in
// library).
func Generate(rng *rand.Rand, p Preferences, lib []Level) (GameConfig, error) {
	c := Default()
	if err := p.Validate(); err != nil {
		return c, err
	}
	if p.Rounds > 0 {
		c.Rules.Rounds = p.Rounds
	}
	if p.HeatSize > 0 {
		c.Rules.HeatSize = p.HeatSize
	}
	p.Rounds = c.Rules.Rounds
	cup, err := RandomCup(rng, p, lib)
	if err != nil {
		return c, err
	}
	c.Levels = cup
	return c, c.Validate()
}

// RandomCup draws one level per round (p.Rounds, default 3) from the levels
// in lib that p allows: it opens with a straight sprint when it can, never
// races the same level twice in a row, and prefers a change of layout and
// theme from the race before (and levels it hasn't used yet). The first race
// is the shortest straight with no specials, so people learn the game on it.
// Specials are placed now, so the cup is concrete.
func RandomCup(rng *rand.Rand, p Preferences, lib []Level) ([]Level, error) {
	pool := p.allowed(lib)
	rounds := p.Rounds
	if rounds <= 0 {
		rounds = Default().Rules.Rounds
	}
	var cup []Level
	for i := range rounds {
		var prev *Level
		if i > 0 {
			prev = &cup[i-1]
		}
		l, err := p.draw(rng, pool, prev, cup, i == 0)
		if err != nil {
			return nil, err
		}
		cup = append(cup, l)
	}
	return cup, nil
}

// RandomLevel draws one level that differs from not (if given) the way
// RandomCup's races differ from the one before.
func RandomLevel(rng *rand.Rand, p Preferences, lib []Level, not *Level) (Level, error) {
	return p.draw(rng, p.allowed(lib), not, nil, false)
}

func (p Preferences) draw(rng *rand.Rand, pool []Level, prev *Level, used []Level, first bool) (Level, error) {
	best, bestScore := []int{}, -1<<30
	for i, l := range pool {
		score := 0
		if first {
			// The opener teaches the game: a short, plain straight.
			if l.Layout == LayoutStraight {
				score += 50
			}
			score -= l.Track.Length
		}
		if prev != nil {
			if l.Name == prev.Name && l.Layout == prev.Layout && l.Theme == prev.Theme {
				score -= 100 // never back to back
			}
			if l.Layout != prev.Layout {
				score += 2
			}
			if l.Theme != prev.Theme {
				score += 2
			}
		}
		for _, u := range used {
			if u.Name == l.Name {
				score -= 3
			}
		}
		switch {
		case score > bestScore:
			best, bestScore = []int{i}, score
		case score == bestScore:
			best = append(best, i)
		}
	}
	if len(best) == 0 {
		return Level{}, errors.New("empty level library")
	}
	l := pool[best[rng.IntN(len(best))]]
	l.Track.Specials = slices.Clone(l.Track.Specials)
	launches, chutes := p.Launches, p.Chutes
	if rs := l.Track.Random; rs != nil && launches+chutes == 0 {
		launches, chutes = rs.Launches, rs.Chutes
	}
	if first {
		launches, chutes = 0, 0 // no launches or chutes until people know the game
		l.Track.Specials = nil
	}
	if launches+chutes > 0 || l.Track.Random != nil {
		t, err := RandomTrack(rng, l.Track.Length, launches, chutes)
		if err != nil {
			return Level{}, fmt.Errorf("%s: %w", l.Name, err)
		}
		l.Track = t
	}
	return l, l.Validate()
}

// SetLevel puts l in race i (0-based; one past the end appends a race).
func (c *GameConfig) SetLevel(i int, l Level) error {
	if i < 0 || i > len(c.Levels) {
		return fmt.Errorf("race %d: no such race (have %d)", i+1, len(c.Levels))
	}
	if err := l.Validate(); err != nil {
		return fmt.Errorf("race %d: %w", i+1, err)
	}
	c.Levels = slices.Clone(c.Levels)
	if i == len(c.Levels) {
		c.Levels = append(c.Levels, l)
	} else {
		c.Levels[i] = l
	}
	return nil
}

// RerollLevel replaces race i with a fresh random level that differs from
// the one it replaces.
func (c *GameConfig) RerollLevel(i int, rng *rand.Rand, p Preferences, lib []Level) error {
	if i < 0 || i >= len(c.Levels) {
		return fmt.Errorf("race %d: no such race (have %d)", i+1, len(c.Levels))
	}
	l, err := RandomLevel(rng, p, lib, &c.Levels[i])
	if err != nil {
		return err
	}
	return c.SetLevel(i, l)
}

// EditLevel changes race i in place (layout, theme, length, specials...).
// If the edited level fails Validate nothing changes and the error says why.
func (c *GameConfig) EditLevel(i int, edit func(*Level)) error {
	if i < 0 || i >= len(c.Levels) {
		return fmt.Errorf("race %d: no such race (have %d)", i+1, len(c.Levels))
	}
	l := c.Levels[i]
	l.Track.Specials = slices.Clone(l.Track.Specials)
	edit(&l)
	return c.SetLevel(i, l)
}

// SingleLevel is a one-level cup on a plain track of length n: every round
// races it. Handy for tests and experiments.
func SingleLevel(n int) []Level {
	return []Level{{Name: fmt.Sprintf("Plain %d", n), Layout: LayoutSwitchback, Theme: "bar", Track: Track{Length: n}}}
}
