// Command hopsim plays thousands of Hoppers nights headless and reports the
// pacing and drama numbers used to tune the rules: race length, how often the
// mid-race leader holds on, lead changes, backwards hops, and how long a whole
// night takes for different crowd sizes (heats kick in above -heat people).
//
//	go run ./cmd/hopsim -players 8,15,30              # the default cup, per level too
//	go run ./cmd/hopsim -players 30 -track 14 -heat 8
//	go run ./cmd/hopsim -config internal/apps/hoppers/engine/levels/chutes-and-ladders.yaml
//	go run ./cmd/hopsim -specials 2,2                  # a fresh random 2+2 track every night
//	go run ./cmd/hopsim -specials 2,2 -placement uniform  # ...placed anywhere, to compare
//	go run ./cmd/hopsim -players 4 -strategy 'exec:python3 my_flicker.py' -workers 1
//	go run ./cmd/hopsim -players 8 -skill 0.9,0.3   # seat skills (cycled): chance a flick is perfect
//	go run ./cmd/hopsim -table                       # win share by skill level, 8-frog races
//	go run ./cmd/hopsim -table -band 0.08,0.12,0.2   # ...for several green-band widths
//	go run ./cmd/hopsim -live -players 4,8,15        # what the live server runs: a fresh generated cup each night
//	go run ./cmd/hopsim -fun -live -games 2000 -players 4,8,15   # fun metrics + fun score (docs/hoppers-fun-metrics.md)
//	go run ./cmd/hopsim -fun -live -catchup 2 -json  # ...with a rubber-band knob, as JSON
package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/mrdon/kit/internal/apps/hoppers/agents"
	"github.com/mrdon/kit/internal/apps/hoppers/engine"
)

type report struct {
	Players          int     `json:"players"`
	Nights           int     `json:"nights"`
	HeatsPerRound    float64 `json:"heatsPerRound"`
	FrogsPerRace     float64 `json:"frogsPerRace"`
	RotationsPerRace float64 `json:"rotationsPerRace"` // hops per frog
	HopsPerRace      float64 `json:"hopsPerRace"`
	FlicksPerPerson  float64 `json:"flicksPerPerson"` // hops of each person's frog per race
	RaceMinutes      float64 `json:"raceMinutes"`
	RaceMinutesP90   float64 `json:"raceMinutesP90"`
	MidLeaderWins    float64 `json:"midLeaderWins"`    // share of races won by the leader halfway through (by hops)
	LastRotLeaderWin float64 `json:"lastRotLeaderWin"` // ...by the leader one rotation before the end
	LeadChanges      float64 `json:"leadChangesPerRace"`
	BackwardShare    float64 `json:"backwardHopShare"`
	MidSpread        float64 `json:"midSpread"`       // coasters first to last frog, halfway through
	SpecialsPerRace  float64 `json:"specialsPerRace"` // launch pads + chutes hit
	LaunchesPerRace  float64 `json:"launchesPerRace"`
	ChutesPerRace    float64 `json:"chutesPerRace"`
	NightMinutes     float64 `json:"nightMinutes"` // racing and countdowns; the host moves on from results at once
	NightMinutesP90  float64 `json:"nightMinutesP90"`
	WithBreaks       float64 `json:"nightMinutesWithBreaks"` // plus -results per race and -beer per round break
}

// fail logs the error and exits with code (2: bad input, 1: a run failed).
func fail(code int, msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(code)
}

func main() {
	f := parseFlags()
	cfg, err := f.config()
	if err != nil {
		fail(2, "config", err)
	}
	cfgFor, err := f.nightConfigs(cfg)
	if err != nil {
		fail(2, "config", err)
	}
	bands, err := parseSkills(*f.band)
	if err != nil {
		fail(2, "-band", err)
	}
	if *f.table {
		cfg.Rules.Rounds, cfg.Rules.HeatSize = 1, 8
		cfgFor = func(uint64) engine.GameConfig { return cfg }
		if err := printTable(cfg, cfgFor, bands, f); err != nil {
			fail(1, "simulate", err)
		}
		return
	}
	skills := []float64{hitRate(bands[0], sigma[0])}
	if *f.fun {
		skills = funCrowd(bands[0])
	}
	if *f.skill != "" {
		if skills, err = parseSkills(*f.skill); err != nil {
			fail(2, "-skill", err)
		}
	}
	if *f.fun {
		if err := runFun(cfg, cfgFor, bands[0], skills, f); err != nil {
			fail(1, "simulate", err)
		}
		return
	}
	reports, perLevel, err := runCrowds(cfg, cfgFor, skills, f)
	if err != nil {
		fail(1, "simulate", err)
	}
	if *f.asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"config": cfg, "results": reports}); err != nil {
			fail(1, "write json", err)
		}
		return
	}
	printReports(cfg, reports, perLevel, f)
}

// runCrowds simulates every crowd size, overall and per level.
func runCrowds(cfg engine.GameConfig, cfgFor func(uint64) engine.GameConfig, skills []float64, f flags) ([]report, [][]report, error) {
	var reports []report
	var perLevel [][]report // [crowd][level]
	for field := range strings.SplitSeq(*f.playerList, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || n < 1 {
			return nil, nil, fmt.Errorf("bad -players entry %q", field)
		}
		r, nights, err := simulate(cfgFor, n, *f.games, *f.strategy, *f.seed, *f.workers, skills)
		if err != nil {
			return nil, nil, fmt.Errorf("%d players: %w", n, err)
		}
		breaks := float64(cfg.Rules.Rounds - 1)
		races := r.HeatsPerRound * float64(cfg.Rules.Rounds)
		r.WithBreaks = r.NightMinutes + (races-breaks)*f.results.Minutes() + breaks*f.beer.Minutes()
		reports = append(reports, r)
		var lv []report
		for i := range cfg.Levels {
			uses := 0 // rounds that race level i
			for rd := range cfg.Rules.Rounds {
				if rd%len(cfg.Levels) == i {
					uses++
				}
			}
			lv = append(lv, summarize(nights, func(rc agents.RaceStat) bool { return rc.Level == i }, max(1, uses)))
		}
		perLevel = append(perLevel, lv)
	}
	return reports, perLevel, nil
}

// seatSkills cycles a skill list over n seats.
func seatSkills(skills []float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = skills[i%len(skills)]
	}
	return out
}

func parseSkills(s string) ([]float64, error) {
	var out []float64
	for f := range strings.SplitSeq(s, ",") {
		v, err := strconv.ParseFloat(strings.TrimSpace(f), 64)
		if err != nil || v < 0 || v > 1 {
			return nil, fmt.Errorf("bad -skill entry %q (want 0..1)", f)
		}
		out = append(out, v)
	}
	return out, nil
}

// Skill model for -table: someone aiming at the middle of the green band
// lands at Normal(centre, sigma) on the meter (0..1). sigma is a guess at
// how consistent a thumb is after a pint: casual (first flicks of the night),
// practiced (a few races in), expert (has been practising at the bar).
var sigma = [3]float64{0.20, 0.10, 0.05}

var levels = [3]string{"casual", "practiced", "expert"}

// hitRate is the chance a flick lands in a band of width w.
func hitRate(w, sigma float64) float64 { return math.Erf(w / 2 / (sigma * math.Sqrt2)) }

type mix struct {
	name   string
	levels []int // index into sigma, per seat
}

var mixes = []mix{
	{"1 expert + 7 casual", []int{2, 0, 0, 0, 0, 0, 0, 0}},
	{"1 practiced + 7 casual", []int{1, 0, 0, 0, 0, 0, 0, 0}},
	{"1 casual + 7 expert", []int{0, 2, 2, 2, 2, 2, 2, 2}},
	{"2 expert, 3 practiced, 3 casual", []int{2, 2, 1, 1, 1, 0, 0, 0}},
	{"all casual", []int{0, 0, 0, 0, 0, 0, 0, 0}},
	{"no skill (nobody hits the band)", []int{-1, -1, -1, -1, -1, -1, -1, -1}},
}

// skillTable plays 8-person races (no bots) for each skill mix at band width
// w and prints each skill level's win share against a fair 1/8.
func skillTable(cfgFor func(uint64) engine.GameConfig, w float64, games int, strategy string, seed uint64, workers int) error {
	hit := hitRates(w)
	fmt.Printf("\nband %.2f: hit rate casual %.0f%%, practiced %.0f%%, expert %.0f%%\n", w, 100*hit[0], 100*hit[1], 100*hit[2])
	fmt.Printf("%-34s %-44s %8s %9s %9s\n", "mix", "win share / fair share", "mid->win", "+1s/race", "race min")
	for _, m := range mixes {
		r, nights, err := simulate(cfgFor, len(m.levels), games, strategy, seed, workers, m.skills(hit))
		if err != nil {
			return err
		}
		races, bonus := 0.0, 0.0
		for _, n := range nights {
			for _, rc := range n.Races {
				races++
				bonus += float64(rc.Bonuses)
			}
		}
		share := m.shares(nights)
		var parts []string
		for l := 2; l >= 0; l-- {
			if !math.IsNaN(share[l]) {
				parts = append(parts, fmt.Sprintf("%s %.2fx", levels[l], share[l]))
			}
		}
		fmt.Printf("%-34s %-44s %7.0f%% %9.1f %9.1f\n", m.name, strings.Join(parts, ", "), 100*r.MidLeaderWins, bonus/max(1, races), r.RaceMinutes)
	}
	return nil
}

// hitRates is each skill level's hit rate on a band of width w.
func hitRates(w float64) [3]float64 {
	var hit [3]float64
	for i := range hit {
		hit[i] = hitRate(w, sigma[i])
	}
	return hit
}

// skills is the mix's per-seat hit rates.
func (m mix) skills(hit [3]float64) []float64 {
	out := make([]float64, len(m.levels))
	for i, l := range m.levels {
		if l >= 0 {
			out[i] = hit[l]
		}
	}
	return out
}

// shares is each skill level's race win share as a multiple of a fair
// 1/seats (NaN for a level with no seats in the mix).
func (m mix) shares(nights []agents.NightResult) [3]float64 {
	var wins, seats [3]float64
	for _, l := range m.levels {
		if l >= 0 {
			seats[l]++
		}
	}
	races := 0.0
	for _, n := range nights {
		for _, rc := range n.Races {
			races++
			if rc.Winner >= 0 && m.levels[rc.Winner] >= 0 {
				wins[m.levels[rc.Winner]]++
			}
		}
	}
	var out [3]float64
	for l := range out {
		out[l] = math.NaN()
		if seats[l] > 0 {
			out[l] = wins[l] / seats[l] / max(1, races) * float64(len(m.levels))
		}
	}
	return out
}

func simulate(cfgFor func(uint64) engine.GameConfig, players, games int, strategy string, seed uint64, workers int, skills []float64) (report, []agents.NightResult, error) {
	sk := seatSkills(skills, players)
	results := make([]agents.NightResult, games)
	errs := make(chan error, games)
	var wg sync.WaitGroup
	jobs := make(chan int)
	for range max(1, workers) {
		wg.Go(func() {
			for i := range jobs {
				s := seed + uint64(i)
				seats, err := agents.Crowd(players, strategy, s)
				if err != nil {
					errs <- err
					continue
				}
				for j := range seats {
					seats[j].Skill = sk[j]
				}
				r, err := agents.Play(cfgFor(s), s, seats)
				if err != nil {
					errs <- err
				}
				results[i] = r
			}
		})
	}
	for i := range games {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(errs)
	for err := range errs {
		return report{}, nil, err
	}

	r := summarize(results, func(agents.RaceStat) bool { return true }, cfgFor(seed).Rules.Rounds)
	r.Players = players
	return r, results, nil
}

// summarize averages the races that keep() picks out of a set of nights.
func summarize(results []agents.NightResult, keep func(agents.RaceStat) bool, rounds int) report {
	r := report{Nights: len(results)}
	var races, hops, back, mid, last, people float64
	var raceMins, nightMins []float64
	for _, n := range results {
		nightMins = append(nightMins, n.GameTime.Minutes())
		for _, rc := range n.Races {
			if !keep(rc) {
				continue
			}
			races++
			r.FrogsPerRace += float64(rc.Frogs)
			r.RotationsPerRace += rc.Rotations
			hops += float64(rc.Hops)
			back += float64(rc.BackHops)
			r.LeadChanges += float64(rc.LeadChanges)
			raceMins = append(raceMins, rc.Duration.Minutes())
			if rc.MidLeaderWon {
				mid++
			}
			if rc.LastRotWon {
				last++
			}
			people += float64(rc.People)
			r.FlicksPerPerson += float64(rc.PersonHops)
			r.MidSpread += rc.MidSpread
			r.LaunchesPerRace += float64(rc.Launches)
			r.ChutesPerRace += float64(rc.Chutes)
		}
	}
	races = max(races, 1)
	r.HeatsPerRound = races / float64(max(1, len(results))) / float64(rounds)
	r.FrogsPerRace /= races
	r.RotationsPerRace /= races
	r.HopsPerRace = hops / races
	r.LeadChanges /= races
	r.BackwardShare = back / max(1, hops)
	r.MidLeaderWins = mid / races
	r.LastRotLeaderWin = last / races
	r.FlicksPerPerson /= max(1, people)
	r.MidSpread /= races
	r.LaunchesPerRace /= races
	r.ChutesPerRace /= races
	r.SpecialsPerRace = r.LaunchesPerRace + r.ChutesPerRace
	r.RaceMinutes, r.RaceMinutesP90 = mean(raceMins), p90(raceMins)
	r.NightMinutes, r.NightMinutesP90 = mean(nightMins), p90(nightMins)
	return r
}

func mean(xs []float64) float64 {
	t := 0.0
	for _, x := range xs {
		t += x
	}
	return t / max(1, float64(len(xs)))
}

func p90(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[len(s)*9/10]
}
