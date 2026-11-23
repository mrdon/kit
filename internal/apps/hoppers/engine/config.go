package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"gopkg.in/yaml.v3"
)

// GameConfig is everything that defines a night of Hoppers: the rules and the
// cup of levels raced round by round. It is what a level file (levels/*.yaml)
// holds, what a host config screen will edit, and what NewGame takes. Build it
// three ways: LoadConfig/ParseConfig a YAML file, start from Default() in Go and
// change fields, or put random specials in a level (Track.Random), which
// NewGame resolves from the game's seed.
//
// Things that only matter to one running server (join URL, build id, test
// speed-up) are not part of a saved night; they live in Options.
type GameConfig struct {
	Rules Rules `yaml:"rules" json:"rules"`
	// Levels is the cup: round N races Levels[N-1], cycling when there are
	// more rounds than levels. Every heat of a round uses the round's level.
	Levels []Level `yaml:"levels" json:"levels"`
}

// Rules are the game rules for the night.
type Rules struct {
	Rounds       int           `yaml:"rounds" json:"rounds"`              // rounds per night; with heats the last is the ladder (The Final)
	HeatSize     int           `yaml:"heat_size" json:"heatSize"`         // most people per race; bigger crowds race in heats
	MinFrogs     int           `yaml:"min_frogs" json:"minFrogs"`         // bots fill a race up to this many frogs (0..8)
	FlickTimeout time.Duration `yaml:"flick_timeout" json:"flickTimeout"` // time before a frog auto-hops
	FlickBand    float64       `yaml:"flick_band" json:"flickBand"`       // width of the phone's green "perfect" band, 0..1 of the meter
	MaxRoll      int           `yaml:"max_roll" json:"maxRoll"`           // hops go 1..MaxRoll coasters
	SkillCap     int           `yaml:"skill_cap" json:"skillCap"`         // longest hop a perfect flick's +1 can make
	SlipChance   float64       `yaml:"slip_chance" json:"slipChance"`     // chance per hop a frog turns around
	TurnBack     float64       `yaml:"turn_back" json:"turnBack"`         // chance per hop a wrong-way frog turns back
	PlacePoints  []int         `yaml:"place_points" json:"placePoints"`   // points for 1st, 2nd, ...; later places score the last entry

	// Rubber-band knobs (engine/tweaks.go). Zero means off: the rule above
	// applies unchanged.
	BackMaxRoll   int     `yaml:"back_max_roll,omitempty" json:"backMaxRoll,omitempty"`     // backward hops go 1..BackMaxRoll (0: MaxRoll)
	LeaderMaxRoll int     `yaml:"leader_max_roll,omitempty" json:"leaderMaxRoll,omitempty"` // the leader's forward hops roll 1..LeaderMaxRoll (0: MaxRoll)
	LeaderSlip    float64 `yaml:"leader_slip,omitempty" json:"leaderSlip,omitempty"`        // the leader's chance per hop to turn around (0: SlipChance)
	CatchUpBand   float64 `yaml:"catch_up_band,omitempty" json:"catchUpBand,omitempty"`     // green band widening per track length behind the leader, x FlickBand (0: off)
	TrailBonus    int     `yaml:"trail_bonus,omitempty" json:"trailBonus,omitempty"`        // extra coasters on forward hops for frogs in last place (0: off)
}

// Layout is how the TV draws a level's track. The engine only validates it.
type Layout string

const (
	LayoutStraight   Layout = "straight"   // one line across the screen (~12-14 spots fit)
	LayoutSwitchback Layout = "switchback" // the track doubles back (~20 spots)
)

// Layouts is every known layout.
var Layouts = []Layout{LayoutStraight, LayoutSwitchback}

// Level is one race's track and look.
type Level struct {
	Name   string `yaml:"name,omitempty" json:"name,omitempty"` // for people (host screen, logs); optional
	Layout Layout `yaml:"layout" json:"layout"`
	Theme  string `yaml:"theme" json:"theme"` // art only: the TV picks the look, the engine never reads it
	Track  Track  `yaml:"track" json:"track"`
}

// Track is the coasters from the start (0) to the finish (Length) and any
// special spots on them.
type Track struct {
	Length   int       `yaml:"length" json:"length"`
	Specials []Special `yaml:"specials,omitempty" json:"specials"`
	// Random, if set, is replaced at game start by randomly placed specials
	// (from the game's seed). It can't be combined with Specials.
	Random *RandomSpecials `yaml:"random,omitempty" json:"-"`
}

// SpotKind is what a special spot does.
type SpotKind string

const (
	SpotLaunch SpotKind = "launch" // a ladder: carries the frog forward to To
	SpotChute  SpotKind = "chute"  // a slide: drops the frog back to To
)

// SpotKinds is every known special spot kind.
var SpotKinds = []SpotKind{SpotLaunch, SpotChute}

// Special is a launch pad or chute: a frog that lands on At (hopping either
// way) goes to To, with every frog riding on it, and lands on top of the stack
// there.
type Special struct {
	At   int      `yaml:"at" json:"at"`
	Kind SpotKind `yaml:"kind" json:"kind"`
	To   int      `yaml:"to" json:"to"`
}

// RandomSpecials asks for randomly placed specials on a level.
type RandomSpecials struct {
	Launches int `yaml:"launches" json:"launches"`
	Chutes   int `yaml:"chutes" json:"chutes"`
}

// Track length limits (the TV can't show more than this).
const (
	MinTrackLen = 6
	MaxTrackLen = 60
)

// Default is the standard night: three rounds over a three-race cup, no
// specials.
func Default() GameConfig {
	return GameConfig{
		Rules: Rules{
			Rounds:       3,
			HeatSize:     10,
			MinFrogs:     7,
			FlickTimeout: 8 * time.Second,
			FlickBand:    BandWidth,
			MaxRoll:      3,
			SkillCap:     4,
			SlipChance:   0.08,
			TurnBack:     0.5,
			PlacePoints:  slices.Clone(DefaultPlacePoints),
		},
		Levels: DefaultLevels(),
	}
}

// DefaultLevels is the default cup: a short straight sprint, then two
// switchbacks.
func DefaultLevels() []Level {
	return []Level{
		{Name: "Bar Sprint", Layout: LayoutStraight, Theme: "bar", Track: Track{Length: 14}},
		{Name: "Lake Loop", Layout: LayoutSwitchback, Theme: "lake", Track: Track{Length: 20}},
		{Name: "Bar Switchback", Layout: LayoutSwitchback, Theme: "bar", Track: Track{Length: 20}},
	}
}

// LoadConfig reads and validates a config file (YAML or JSON).
func LoadConfig(path string) (GameConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return GameConfig{}, fmt.Errorf("load config: %w", err)
	}
	cfg, err := ParseConfig(data)
	if err != nil {
		return GameConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// ParseConfig decodes a config over Default() (fields left out keep their
// default) and validates it. It takes YAML (snake_case keys, as in
// levels/*.yaml) or JSON (camelCase keys, as GameConfig marshals, with
// durations as "8s" or nanoseconds). Unknown fields are errors, so a typo
// never silently does nothing.
func ParseConfig(data []byte) (GameConfig, error) {
	cfg, err := decodeConfig(data)
	if err != nil {
		return GameConfig{}, err
	}
	if err := cfg.Validate(); err != nil {
		return GameConfig{}, err
	}
	return cfg, nil
}

func decodeConfig(data []byte) (GameConfig, error) {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '{' {
		cfg, err := parseJSON(trimmed)
		var syn *json.SyntaxError
		if !errors.As(err, &syn) {
			return cfg, err
		}
		// not JSON after all: a YAML flow mapping like {rules: {rounds: 2}}
	}
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return GameConfig{}, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

// parseJSON decodes JSON over Default(). Lists replace the default lists
// whole (encoding/json would otherwise decode into the old elements).
func parseJSON(data []byte) (GameConfig, error) {
	cfg := Default()
	cfg.Levels, cfg.Rules.PlacePoints = nil, nil
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return GameConfig{}, fmt.Errorf("parse config: %w", err)
	}
	if dec.More() {
		return GameConfig{}, errors.New("parse config: more than one JSON value")
	}
	d := Default()
	if cfg.Levels == nil {
		cfg.Levels = d.Levels
	}
	if cfg.Rules.PlacePoints == nil {
		cfg.Rules.PlacePoints = d.Rules.PlacePoints
	}
	return cfg, nil
}

// UnmarshalJSON reads Rules with durations written either way: "8s" (people)
// or nanoseconds (how Rules marshals). Unknown fields are errors.
func (r *Rules) UnmarshalJSON(data []byte) error {
	type plain Rules
	aux := struct {
		*plain
		FlickTimeout jsonDuration `json:"flickTimeout"`
	}{plain: (*plain)(r), FlickTimeout: jsonDuration(r.FlickTimeout)}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&aux); err != nil {
		return fmt.Errorf("rules: %w", err)
	}
	r.FlickTimeout = time.Duration(aux.FlickTimeout)
	return nil
}

// jsonDuration is a time.Duration that reads "8s" as well as nanoseconds.
type jsonDuration time.Duration

func (d *jsonDuration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("duration %q: %w", s, err)
		}
		*d = jsonDuration(v)
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("duration %s: want \"8s\" or nanoseconds: %w", data, err)
	}
	*d = jsonDuration(n)
	return nil
}

// YAML encodes the config as a level file.
func (c GameConfig) YAML() ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	return b.Bytes(), nil
}
