package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
)

// printTable prints the -table report: win share by skill, per band width.
func printTable(cfg engine.GameConfig, cfgFor func(uint64) engine.GameConfig, bands []float64, f flags) error {
	fmt.Printf("8 people per race (no bots), track %d (%s), %d races per row. Hit rate = chance a flick lands in the band,\n", cfg.Levels[0].Track.Length, cfg.Levels[0].Name, *f.games)
	fmt.Printf("assuming flick strength ~ Normal(band centre, sigma): casual sigma %.2f, practiced %.2f, expert %.2f (meter units).\n", sigma[0], sigma[1], sigma[2])
	for _, w := range bands {
		if err := skillTable(cfgFor, w, *f.games, *f.strategy, *f.seed, *f.workers); err != nil {
			return fmt.Errorf("band %g: %w", w, err)
		}
	}
	return nil
}

// printReports prints the pacing table, per crowd and per level.
func printReports(cfg engine.GameConfig, reports []report, perLevel [][]report, f flags) {
	var cup []string
	for _, l := range cfg.Levels {
		sp := fmt.Sprintf("%d specials", len(l.Track.Specials))
		if l.Track.Random != nil {
			sp = fmt.Sprintf("random %d+%d (%s)", l.Track.Random.Launches, l.Track.Random.Chutes, *f.placement)
		}
		cup = append(cup, fmt.Sprintf("%s %s %d, %s", l.Name, l.Layout, l.Track.Length, sp))
	}
	r := cfg.Rules
	fmt.Printf("cup: %s\nheats of <=%d, %d rounds, bots fill to %d, slip %.2f, flicks by %q\n\n", strings.Join(cup, " | "), r.HeatSize, r.Rounds, r.MinFrogs, r.SlipChance, *f.strategy)
	printHeader("people")
	for _, r := range reports {
		printRow(strconv.Itoa(r.Players), r)
	}
	if len(cfg.Levels) > 1 {
		fmt.Println("\nby level (night columns are for the whole night):")
		printHeader("people / level")
		for i, r := range reports {
			for j, l := range cfg.Levels {
				lr := perLevel[i][j]
				lr.NightMinutes, lr.NightMinutesP90, lr.WithBreaks = r.NightMinutes, r.NightMinutesP90, r.WithBreaks
				printRow(fmt.Sprintf("%d %s", r.Players, l.Name), lr)
			}
		}
	}
	fmt.Println("\nrots: hops per frog. flicks/p: hops per person per race. mid->win: the leader halfway through (by hops) wins;")
	fmt.Println("1rot->win: the leader one rotation from the end wins. spread: coasters from last frog to first at mid race.")
	fmt.Println("spec: launch pads + chutes hit per race.")
	fmt.Printf("night min: racing and countdowns (the sim's host moves on from results at once);\n")
	fmt.Printf("+breaks adds %s of results per race and %d beer breaks of %s.\n", *f.results, cfg.Rules.Rounds-1, *f.beer)
}

func printHeader(first string) {
	fmt.Printf("%-16s %5s %5s %5s %8s %12s %8s %9s %6s %5s %6s %6s %14s %8s\n",
		first, "heats", "frogs", "rots", "flicks/p", "race min", "mid->win", "1rot->win", "leads", "back%", "spread", "spec", "night min", "+breaks")
}

func printRow(label string, r report) {
	fmt.Printf("%-16s %5.1f %5.1f %5.1f %8.1f %5.1f (p90 %3.1f) %7.0f%% %8.0f%% %6.1f %4.0f%% %6.1f %6.2f %6.1f (p90 %3.0f) %8.0f\n",
		label, r.HeatsPerRound, r.FrogsPerRace, r.RotationsPerRace, r.FlicksPerPerson, r.RaceMinutes, r.RaceMinutesP90,
		100*r.MidLeaderWins, 100*r.LastRotLeaderWin, r.LeadChanges, 100*r.BackwardShare, r.MidSpread, r.SpecialsPerRace, r.NightMinutes, r.NightMinutesP90, r.WithBreaks)
}
