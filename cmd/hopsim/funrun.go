package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
)

// funResult is one crowd size's fun metrics and score.
type funResult struct {
	Players int                `json:"players"`
	Metrics funMetrics         `json:"metrics"`
	Scores  map[string]float64 `json:"scores"` // per target, 0..1
	Score   float64            `json:"score"`  // weighted, 0..100
}

// funCrowd is the default crowd for -fun: a bar's mix of thumbs, cycled over
// the seats (3 casual, 3 practiced, 2 expert in 8).
func funCrowd(band float64) []float64 {
	hit := hitRates(band)
	return mix{levels: []int{0, 1, 2, 0, 1, 0, 2, 1}}.skills(hit)
}

// Mixes (index into mixes) the win-share metrics come from.
const (
	mixExpert    = 0 // 1 expert + 7 casual
	mixPracticed = 1 // 1 practiced + 7 casual
	mixCasual    = 2 // 1 casual + 7 expert
	mixMixed     = 3 // 2 expert, 3 practiced, 3 casual
)

// mixShares plays the skill mixes and fills in the win-share metrics.
func mixShares(cfgFor func(uint64) engine.GameConfig, band float64, f flags) (funMetrics, error) {
	hit := hitRates(band)
	var share [4][3]float64
	for i, k := range []int{mixExpert, mixPracticed, mixCasual, mixMixed} {
		m := mixes[k]
		_, nights, err := simulate(cfgFor, len(m.levels), *f.games, *f.strategy, *f.seed, *f.workers, m.skills(hit))
		if err != nil {
			return funMetrics{}, fmt.Errorf("%s: %w", m.name, err)
		}
		share[i] = m.shares(nights)
	}
	return funMetrics{ExpertShare: share[0][2], PracticedShare: share[1][1], CasualVsExperts: share[2][0], CasualShare: share[3][0]}, nil
}

// runFun measures every crowd size and prints (or writes as JSON) the fun
// metrics and score.
func runFun(cfg engine.GameConfig, cfgFor func(uint64) engine.GameConfig, band float64, skills []float64, f flags) error {
	ws, err := mixShares(cfgFor, band, f)
	if err != nil {
		return err
	}
	var out []funResult
	for field := range strings.SplitSeq(*f.playerList, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || n < 1 {
			return fmt.Errorf("bad -players entry %q", field)
		}
		_, nights, err := simulate(cfgFor, n, *f.games, *f.strategy, *f.seed, *f.workers, skills)
		if err != nil {
			return fmt.Errorf("%d players: %w", n, err)
		}
		m := crowdFun(nights)
		m.ExpertShare, m.PracticedShare, m.CasualShare, m.CasualVsExperts = ws.ExpertShare, ws.PracticedShare, ws.CasualShare, ws.CasualVsExperts
		sc, score := funScore(m)
		out = append(out, funResult{Players: n, Metrics: m, Scores: sc, Score: score})
	}
	if *f.asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"config": cfg, "live": *f.live, "nights": *f.games, "fun": out}); err != nil {
			return fmt.Errorf("write json: %w", err)
		}
		return nil
	}
	printFun(cfg, out, f)
	return nil
}

// printFun prints the metric table next to the score, one column per crowd
// size (value, then its 0..1 score).
func printFun(cfg engine.GameConfig, out []funResult, f flags) {
	r := cfg.Rules
	src := "the default cup"
	if *f.live {
		src = "live: a fresh Generate cup per night, 1+1 specials except race 1"
	} else if *f.configPath != "" {
		src = *f.configPath
	}
	fmt.Printf("%s; %d nights per crowd and per skill mix; slip %.2f turnback %.2f roll 1..%d backroll %d leaderroll %d leaderslip %.2f catchup %.1f trailbonus %d; specials %s\n\n",
		src, *f.games, r.SlipChance, r.TurnBack, r.MaxRoll, r.BackMaxRoll, r.LeaderMaxRoll, r.LeaderSlip, r.CatchUpBand, r.TrailBonus, *f.placement)
	fmt.Printf("%-28s %-11s %4s", "metric", "target", "wt")
	for _, o := range out {
		fmt.Printf(" %16s", fmt.Sprintf("%d people", o.Players))
	}
	fmt.Println()
	for _, t := range targets {
		fmt.Printf("%-28s %-11s %4.1f", t.name, t.goal, t.weight)
		for _, o := range out {
			fmt.Printf(" %10s (%.2f)", fmtMetric(t, t.get(o.Metrics)), o.Scores[t.key])
		}
		fmt.Println()
	}
	info := []struct {
		name string
		get  func(funMetrics) string
	}{
		{"practiced win share (1P+7C)", func(m funMetrics) string { return fmt.Sprintf("%.2fx", m.PracticedShare) }},
		{"casual vs 7 experts", func(m funMetrics) string { return fmt.Sprintf("%.2fx", m.CasualVsExperts) }},
		{"spread spots mean / p90", func(m funMetrics) string { return fmt.Sprintf("%.1f / %.0f", m.SpreadMean, m.SpreadP90) }},
		{"last frog / leader at lap end", func(m funMetrics) string {
			return fmt.Sprintf("%.0f%% / %.0f%%", 100*m.LastFrogFrac, 100*m.LeaderFrac)
		}},
		{"hops per frog", func(m funMetrics) string { return fmt.Sprintf("%.1f", m.HopsPerFrog) }},
		{"race minutes", func(m funMetrics) string { return fmt.Sprintf("%.1f", m.RaceMinutes) }},
	}
	for _, in := range info {
		fmt.Printf("%-28s %-11s %4s", in.name, "(info)", "")
		for _, o := range out {
			fmt.Printf(" %16s", in.get(o.Metrics))
		}
		fmt.Println()
	}
	fmt.Printf("%-28s %-11s %4s", "FUN SCORE (0-100)", "", "")
	for _, o := range out {
		fmt.Printf(" %16.1f", o.Score)
	}
	fmt.Println()
}

func fmtMetric(t target, v float64) string {
	switch {
	case t.pct:
		return fmt.Sprintf("%.1f%%", 100*v)
	case strings.HasSuffix(t.goal, "x"):
		return fmt.Sprintf("%.2fx", v)
	default:
		return fmt.Sprintf("%.2f", v)
	}
}
