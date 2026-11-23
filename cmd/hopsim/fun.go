package main

import (
	"math"

	"github.com/mrdon/kit/internal/apps/hoppers/agents"
)

// funMetrics are the numbers behind the fun score (-fun). Win shares come
// from 8-person skill-mix nights (no bots); the rest from the crowd's own
// nights. Shares are 0..1; win shares are multiples of a fair 1/frogs.
type funMetrics struct {
	ExpertShare     float64 `json:"expertShare"`     // the expert in 1 expert + 7 casual
	PracticedShare  float64 `json:"practicedShare"`  // the practiced player in 1 practiced + 7 casual
	CasualShare     float64 `json:"casualShare"`     // casuals in 2 expert, 3 practiced, 3 casual
	CasualVsExperts float64 `json:"casualVsExperts"` // the casual in 1 casual + 7 expert (shown, not scored)
	UnderdogWins    float64 `json:"underdogWins"`    // races won by a frog last or second-to-last at halfway
	SpreadMean      float64 `json:"spreadMean"`      // first-to-last spots at each lap end
	SpreadP90       float64 `json:"spreadP90"`
	SpreadMeanFrac  float64 `json:"spreadMeanFrac"` // ...as a share of the track
	SpreadP90Frac   float64 `json:"spreadP90Frac"`
	Stragglers      float64 `json:"stragglers"`     // frog-races > 1/3 track behind the leader at halfway
	StragglerTurns  float64 `json:"stragglerTurns"` // own turns per frog-race started > 1/3 track behind
	Runaways        float64 `json:"runaways"`       // races with a lap end where the leader is > 1/3 track ahead of second
	Lap1LeaderWins  float64 `json:"lap1LeaderWins"`
	MidLeaderWins   float64 `json:"midLeaderWins"`
	LeadChanges     float64 `json:"leadChanges"` // per race
	LateDrama       float64 `json:"lateDrama"`   // races with a lead change in the final lap
	LongBackShare   float64 `json:"longBackShare"`
	LongBackFrogs   float64 `json:"longBackFrogRaces"`
	StuckMean       float64 `json:"stuckMean"` // longest run of own turns with no net forward progress
	StuckP90        float64 `json:"stuckP90"`
	LastFrogFrac    float64 `json:"lastFrogFrac"` // where the last frog is at lap ends, share of the track (context)
	LeaderFrac      float64 `json:"leaderFrac"`   // ...and the leader (context)
	HopsPerFrog     float64 `json:"hopsPerFrog"`  // context, not scored
	RaceMinutes     float64 `json:"raceMinutes"`  // context, not scored
}

// crowdFun measures every race of a crowd's nights (win shares are filled
// in from the skill mixes).
func crowdFun(nights []agents.NightResult) funMetrics {
	var m funMetrics
	var races, frogRaces, turnarounds, longBack float64
	var spreads, fracs, stuck, mins []float64
	for _, n := range nights {
		for _, rc := range n.Races {
			fs := rc.Fun
			races++
			frogRaces += float64(rc.Frogs)
			for _, s := range fs.LapSpreads {
				spreads = append(spreads, float64(s))
				fracs = append(fracs, float64(s)/float64(rc.TrackLen))
			}
			for i, l := range fs.LapLast {
				m.LastFrogFrac += float64(l) / float64(rc.TrackLen)
				m.LeaderFrac += float64(l+fs.LapSpreads[i]) / float64(rc.TrackLen)
			}
			m.UnderdogWins += b2f(fs.UnderdogWon)
			m.Stragglers += float64(fs.Stragglers)
			m.StragglerTurns += float64(fs.StragglerTurns)
			m.Runaways += b2f(fs.Runaway)
			m.Lap1LeaderWins += b2f(fs.Lap1LeaderWon)
			m.MidLeaderWins += b2f(rc.MidLeaderWon)
			m.LeadChanges += float64(rc.LeadChanges)
			m.LateDrama += b2f(fs.LateLeadChange)
			turnarounds += float64(fs.Turnarounds)
			longBack += float64(fs.LongBack)
			m.LongBackFrogs += float64(fs.FrogsLongBack)
			m.HopsPerFrog += rc.Rotations
			mins = append(mins, rc.Duration.Minutes())
			for _, s := range fs.Stuck {
				stuck = append(stuck, float64(s))
			}
		}
	}
	races, frogRaces = max(races, 1), max(frogRaces, 1)
	for _, v := range []*float64{&m.UnderdogWins, &m.Runaways, &m.Lap1LeaderWins, &m.MidLeaderWins, &m.LeadChanges, &m.LateDrama, &m.HopsPerFrog} {
		*v /= races
	}
	for _, v := range []*float64{&m.Stragglers, &m.StragglerTurns, &m.LongBackFrogs} {
		*v /= frogRaces
	}
	m.LongBackShare = longBack / max(1, turnarounds)
	m.LastFrogFrac /= max(1, float64(len(spreads)))
	m.LeaderFrac /= max(1, float64(len(spreads)))
	m.SpreadMean, m.SpreadP90 = mean(spreads), p90(spreads)
	m.SpreadMeanFrac, m.SpreadP90Frac = mean(fracs), p90(fracs)
	m.StuckMean, m.StuckP90 = mean(stuck), p90(stuck)
	m.RaceMinutes = mean(mins)
	return m
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// curve scores a metric 0..1: 1 on [lo, hi], falling linearly to 0 at
// zeroLo below and zeroHi above (infinite ends never fall off).
type curve struct{ zeroLo, lo, hi, zeroHi float64 }

var inf = math.Inf(1)

func atMost(good, bad float64) curve  { return curve{-inf, -inf, good, bad} }
func atLeast(good, bad float64) curve { return curve{bad, good, inf, inf} }

func (c curve) score(v float64) float64 {
	switch {
	case v >= c.lo && v <= c.hi:
		return 1
	case v < c.lo:
		return math.Max(0, (v-c.zeroLo)/(c.lo-c.zeroLo))
	default:
		return math.Max(0, (c.zeroHi-v)/(c.zeroHi-c.hi))
	}
}

// target is one row of the fun score.
type target struct {
	key    string
	name   string
	goal   string // the target, for people
	weight float64
	curve  curve
	pct    bool // print as a percentage
	get    func(funMetrics) float64
}

// targets is the fun score: every scored metric, its target, how fast the
// score falls off it, and its weight. The heaviest rows are the ones a bar
// crowd feels most: a frog left behind, a race that's over early, and an
// expert who wins everything. docs/hoppers-fun-metrics.md explains each row.
var targets = []target{
	{"expertShare", "expert win share (1E+7C)", "1.05-1.30x", 3, curve{0.9, 1.05, 1.3, 2.0}, false, func(m funMetrics) float64 { return m.ExpertShare }},
	{"casualShare", "casual win share (mixed)", ">=0.90x", 2, atLeast(0.9, 0.5), false, func(m funMetrics) float64 { return m.CasualShare }},
	{"underdogWins", "underdog wins", "10-15%", 1.5, curve{0.02, 0.10, 0.15, 0.30}, true, func(m funMetrics) float64 { return m.UnderdogWins }},
	{"spreadMeanFrac", "spread mean / track", "<=25%", 1.5, atMost(0.25, 0.60), true, func(m funMetrics) float64 { return m.SpreadMeanFrac }},
	{"spreadP90Frac", "spread p90 / track", "<=45%", 1, atMost(0.45, 0.90), true, func(m funMetrics) float64 { return m.SpreadP90Frac }},
	{"stragglers", "stragglers at halfway", "<=10%", 3, atMost(0.10, 0.40), true, func(m funMetrics) float64 { return m.Stragglers }},
	{"stragglerTurns", "turns far behind / frog", "<=1.0", 1, atMost(1, 4), false, func(m funMetrics) float64 { return m.StragglerTurns }},
	{"runaways", "runaway leads", "<=10%", 3, atMost(0.10, 0.40), true, func(m funMetrics) float64 { return m.Runaways }},
	{"lap1LeaderWins", "lap-1 leader wins", "20-30%", 3, curve{0.10, 0.20, 0.30, 0.55}, true, func(m funMetrics) float64 { return m.Lap1LeaderWins }},
	{"midLeaderWins", "halfway leader wins", "30-40%", 3, curve{0.15, 0.30, 0.40, 0.65}, true, func(m funMetrics) float64 { return m.MidLeaderWins }},
	{"leadChanges", "lead changes / race", "3-8", 1, curve{1, 3, 8, 14}, false, func(m funMetrics) float64 { return m.LeadChanges }},
	{"lateDrama", "late drama", "35-75%", 1, curve{0.10, 0.35, 0.75, 1.0}, true, func(m funMetrics) float64 { return m.LateDrama }},
	{"longBackShare", "turnarounds -> 3+ back", "<=15%", 1, atMost(0.15, 0.45), true, func(m funMetrics) float64 { return m.LongBackShare }},
	{"longBackFrogRaces", "frog-races with 3+ back", "<=5%", 0.5, atMost(0.05, 0.20), true, func(m funMetrics) float64 { return m.LongBackFrogs }},
	{"stuckMean", "stuck turns, mean", "<=1.5", 1, atMost(1.5, 4), false, func(m funMetrics) float64 { return m.StuckMean }},
	{"stuckP90", "stuck turns, p90", "<=3", 1, atMost(3, 7), false, func(m funMetrics) float64 { return m.StuckP90 }},
}

// funScore is each target's 0..1 score and their weighted mean (0..100).
func funScore(m funMetrics) (map[string]float64, float64) {
	scores := map[string]float64{}
	var sum, weights float64
	for _, t := range targets {
		s := t.curve.score(t.get(m))
		scores[t.key] = s
		sum += t.weight * s
		weights += t.weight
	}
	return scores, 100 * sum / weights
}
