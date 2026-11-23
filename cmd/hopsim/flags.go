package main

import (
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/mrdon/kit/internal/apps/hoppers/agents"
	"github.com/mrdon/kit/internal/apps/hoppers/engine"
)

// flags is everything on hopsim's command line.
type flags struct {
	configPath, specials, placement, playerList, strategy, skill, band *string
	games, track, rounds, heat, minFrogs, maxRoll, workers             *int
	backRoll, leaderRoll, trailBonus                                   *int
	slip, turnBack, leaderSlip, catchUp                                *float64
	flick, results, beer                                               *time.Duration
	seed                                                               *uint64
	asJSON, table, fun, live                                           *bool
	set                                                                map[string]bool
}

func parseFlags() flags {
	d := engine.Default().Rules
	f := flags{
		configPath: flag.String("config", "", "night config, YAML or JSON (default: the default cup); the flags below override it"),
		specials:   flag.String("specials", "", "launches,chutes placed at random on every level, a fresh track each night (e.g. 2,2)"),
		placement:  flag.String("placement", "pack", "with -specials or -live: pack (launches in the first half, chutes in the second; the engine's generator), tight (launches in the first 30%, chutes past 60%: where the back and front of the pack are) or uniform"),
		games:      flag.Int("games", 500, "nights to simulate per crowd size"),
		playerList: flag.String("players", "1,4,8,15,30", "crowd sizes to simulate (comma-separated)"),
		strategy:   flag.String("strategy", "human", "how people flick (have: "+strings.Join(agents.Names(), ",")+", exec:<cmd>)"),
		track:      flag.Int("track", 0, "make every level this many coasters (default: each level's own)"),
		rounds:     flag.Int("rounds", d.Rounds, "rounds per night"),
		heat:       flag.Int("heat", d.HeatSize, "most people per race; bigger crowds race in heats"),
		minFrogs:   flag.Int("minfrogs", d.MinFrogs, "bots fill a race up to this many frogs"),
		slip:       flag.Float64("slip", d.SlipChance, "chance per hop a frog turns around"),
		turnBack:   flag.Float64("turnback", d.TurnBack, "chance per hop a wrong-way frog turns back"),
		maxRoll:    flag.Int("roll", d.MaxRoll, "hops go 1..roll coasters"),
		backRoll:   flag.Int("backroll", d.BackMaxRoll, "backward hops go 1..backroll coasters (0: same as -roll)"),
		leaderRoll: flag.Int("leaderroll", d.LeaderMaxRoll, "the leader's forward hops roll 1..leaderroll (0: same as -roll)"),
		leaderSlip: flag.Float64("leaderslip", d.LeaderSlip, "the leader's chance per hop to turn around (0: same as -slip)"),
		catchUp:    flag.Float64("catchup", d.CatchUpBand, "green band widening per track length behind the leader, times the band (0: off)"),
		trailBonus: flag.Int("trailbonus", d.TrailBonus, "extra coasters on forward hops for the frog(s) in last place (0: off)"),
		flick:      flag.Duration("flick", d.FlickTimeout, "time before a frog auto-hops"),
		results:    flag.Duration("results", 20*time.Second, "assumed time the host leaves a race's results up before moving on (only for the with-breaks total)"),
		beer:       flag.Duration("beer", 5*time.Minute, "assumed length of each beer break (only for the with-breaks total)"),
		seed:       flag.Uint64("seed", 1, "base seed (night i uses seed+i)"),
		workers:    flag.Int("workers", runtime.NumCPU(), "parallel games (use 1 for exec: agents)"),
		asJSON:     flag.Bool("json", false, "print results as JSON"),
		skill:      flag.String("skill", "", "per-seat chance a flick is perfect, comma-separated, cycled over seats (default: a casual's hit rate at -band; -fun: a casual/practiced/expert mix)"),
		band:       flag.String("band", fmt.Sprint(engine.BandWidth), "green band width(s) on the strength meter, 0..1 (several, comma-separated, for -table)"),
		table:      flag.Bool("table", false, "print win share by skill level in 8-frog races, for each -band"),
		fun:        flag.Bool("fun", false, "print the fun metrics and fun score (docs/hoppers-fun-metrics.md) instead of the pacing table"),
		live:       flag.Bool("live", false, "simulate what the live server runs: a fresh engine.Generate cup from DefaultPreferences every night (1+1 specials, none on race 1)"),
	}
	flag.Parse()
	f.set = map[string]bool{}
	flag.Visit(func(fl *flag.Flag) { f.set[fl.Name] = true })
	return f
}

// applyRules puts the rule flags that were given on r.
func (f flags) applyRules(r *engine.Rules) {
	for name, apply := range map[string]func(){
		"rounds": func() { r.Rounds = *f.rounds }, "heat": func() { r.HeatSize = *f.heat }, "minfrogs": func() { r.MinFrogs = *f.minFrogs },
		"slip": func() { r.SlipChance = *f.slip }, "turnback": func() { r.TurnBack = *f.turnBack }, "flick": func() { r.FlickTimeout = *f.flick },
		"roll":     func() { r.MaxRoll, r.SkillCap = *f.maxRoll, *f.maxRoll+1 },
		"backroll": func() { r.BackMaxRoll = *f.backRoll }, "leaderroll": func() { r.LeaderMaxRoll = *f.leaderRoll },
		"leaderslip": func() { r.LeaderSlip = *f.leaderSlip }, "catchup": func() { r.CatchUpBand = *f.catchUp },
		"trailbonus": func() { r.TrailBonus = *f.trailBonus },
	} {
		if f.set[name] {
			apply()
		}
	}
}

// config is the night to simulate: the file or default cup, with flag
// overrides. With -live it is the first night's generated cup.
func (f flags) config() (engine.GameConfig, error) {
	if p := *f.placement; p != "pack" && p != "tight" && p != "uniform" {
		return engine.GameConfig{}, fmt.Errorf("-placement %q: want pack, tight or uniform", p)
	}
	if *f.live {
		if *f.configPath != "" || *f.specials != "" || f.set["track"] {
			return engine.GameConfig{}, errors.New("-live generates its own cup: drop -config, -specials and -track")
		}
		return f.liveConfig(*f.seed)
	}
	cfg := engine.Default()
	if *f.configPath != "" {
		var err error
		if cfg, err = engine.LoadConfig(*f.configPath); err != nil {
			return cfg, fmt.Errorf("load: %w", err)
		}
	}
	f.applyRules(&cfg.Rules)
	if f.set["track"] {
		cfg = cfg.WithLength(*f.track)
	}
	if *f.specials != "" {
		var nl, nc int
		if _, err := fmt.Sscanf(*f.specials, "%d,%d", &nl, &nc); err != nil {
			return cfg, fmt.Errorf("bad -specials, want launches,chutes like 2,2: %w", err)
		}
		cfg = cfg.WithRandomSpecials(nl, nc)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("validate: %w", err)
	}
	return cfg, nil
}

// liveConfig is the night the live server (main.go's gameConfig, no -config
// flag) would generate from seed, with the rule flags on top.
func (f flags) liveConfig(seed uint64) (engine.GameConfig, error) {
	prefs := engine.DefaultPreferences()
	if f.set["rounds"] {
		prefs.Rounds = *f.rounds
	}
	cfg, err := engine.Generate(rand.New(rand.NewPCG(seed, 0xc0b)), prefs, nil)
	if err != nil {
		return cfg, fmt.Errorf("generate: %w", err)
	}
	f.applyRules(&cfg.Rules)
	if *f.placement != "pack" {
		rng := rand.New(rand.NewPCG(seed, 0x5eed))
		for i := range cfg.Levels {
			if t := cfg.Levels[i].Track; len(t.Specials) > 0 {
				nl, nc := countSpecials(t)
				cfg.Levels[i].Track = f.placeTrack(rng, t.Length, nl, nc)
			}
		}
	}
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("validate: %w", err)
	}
	return cfg, nil
}

func countSpecials(t engine.Track) (launches, chutes int) {
	for _, s := range t.Specials {
		if s.Kind == engine.SpotLaunch {
			launches++
		} else {
			chutes++
		}
	}
	return launches, chutes
}

// nightConfigs is night i's config: the engine places random specials from
// the night's seed; -placement uniform places them here instead; -live
// generates a fresh cup per night.
func (f flags) nightConfigs(cfg engine.GameConfig) (func(uint64) engine.GameConfig, error) {
	same := func(uint64) engine.GameConfig { return cfg }
	if *f.live {
		return func(s uint64) engine.GameConfig {
			c, err := f.liveConfig(s)
			if err != nil {
				panic(fmt.Sprintf("hopsim: night %d: %v", s, err)) // the first night validated the flags
			}
			return c
		}, nil
	}
	if *f.specials == "" || *f.placement == "pack" {
		return same, nil
	}
	var nl, nc int
	if _, err := fmt.Sscanf(*f.specials, "%d,%d", &nl, &nc); err != nil {
		return nil, fmt.Errorf("bad -specials: %w", err)
	}
	return func(s uint64) engine.GameConfig {
		c := cfg
		c.Levels = slices.Clone(cfg.Levels)
		rng := rand.New(rand.NewPCG(s, 0x5eed))
		for i := range c.Levels {
			c.Levels[i].Track = f.placeTrack(rng, c.Levels[i].Track.Length, nl, nc)
		}
		return c
	}, nil
}

// placeTrack places specials the -placement way (not pack: the engine does
// that itself).
func (f flags) placeTrack(rng *rand.Rand, length, launches, chutes int) engine.Track {
	if *f.placement == "uniform" {
		return uniformTrack(rng, length, launches, chutes)
	}
	z := engine.Zones{LaunchLo: 2, LaunchHi: max(2, length*3/10), ChuteLo: length * 6 / 10, ChuteHi: length - 2}
	t, err := engine.RandomTrackIn(rng, length, launches, chutes, z)
	if err != nil {
		panic(fmt.Sprintf("hopsim: tight placement: %v", err))
	}
	return t
}

// uniformTrack places specials anywhere on the track (to compare with the
// engine's pack-together placement).
func uniformTrack(rng *rand.Rand, length, launches, chutes int) engine.Track {
	for {
		t := engine.Track{Length: length}
		used := map[int]bool{}
		for k := range launches + chutes {
			kind, at := engine.SpotLaunch, 1+rng.IntN(length-1)
			to := at + 3 + rng.IntN(4)
			if k >= launches {
				kind, to = engine.SpotChute, at-3-rng.IntN(4)
			}
			if used[at] || used[to] {
				continue
			}
			used[at], used[to] = true, true
			t.Specials = append(t.Specials, engine.Special{At: at, Kind: kind, To: to})
		}
		if len(t.Specials) == launches+chutes && t.Validate() == nil {
			return t
		}
	}
}
