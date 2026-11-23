package engine

import (
	"cmp"
	"slices"
	"time"
)

func (g *Game) placePoints(place int) int {
	pp := g.cfg.Rules.PlacePoints
	switch {
	case place < 1 || len(pp) == 0:
		return 0
	case place <= len(pp):
		return pp[place-1]
	default:
		return pp[len(pp)-1]
	}
}

// endRace scores the race by finishing place. Bots take their place (beat
// them!) but don't collect points.
func (g *Game) endRace(lead string) {
	g.winner = lead
	h := g.curHeat()
	g.results = nil
	for i, id := range g.ranking() {
		place := i + 1
		p := g.players[id]
		pts := 0
		if p != nil && !p.Bot {
			pts = g.placePoints(place)
			p.Score += pts
			if place == 1 {
				p.Wins++
			}
		}
		g.results = append(g.results, Result{Player: id, Kind: ResultPlace, Frog: id, Place: place, Won: pts > 0, Points: pts})
	}
	h.Status = HeatDone
	h.Winner = lead
	h.Results = slices.Clone(g.results)
	if hop := g.lastHop; hop != nil && hop.Frog != lead && slices.Contains(hop.Moved, lead) {
		g.say("%s rides %s to the finish and wins %s!", g.name(lead), g.name(hop.Frog), h.Name)
	} else {
		g.say("%s wins %s!", g.name(lead), h.Name)
	}
	// The results stay up until the host moves on: no timer.
	g.sub = SubRaceEnd
	g.deadline = time.Time{}
	if g.roundOver() && !g.finalRound() {
		if len(g.heats) > 1 {
			g.say("Beer break. Round %d of %d next.", g.round+1, g.cfg.Rules.Rounds)
		} else {
			g.say("Beer break. Race %d when you're ready.", g.round+1)
		}
		g.syncBots()
	}
}

// pickChampion: the person with the most points (ties: more heat wins, then
// who joined first). The Final is just the last heat, seeded with the top
// scorers.
func (g *Game) pickChampion() string {
	if s := g.standingIDs(g.scorers()); len(s) > 0 && g.players[s[0]].Score > 0 {
		return s[0]
	}
	return ""
}

// scorers is every person in the standings: connected, or scored tonight.
func (g *Game) scorers() []string {
	var ids []string
	for _, id := range g.joinSeq {
		if p := g.players[id]; !p.Bot && (p.Connected || p.Score > 0) {
			ids = append(ids, id)
		}
	}
	return ids
}

// standingIDs sorts people by points, then heat wins, then who joined first.
func (g *Game) standingIDs(ids []string) []string {
	out := slices.Clone(ids)
	seq := map[string]int{}
	for i, id := range g.joinSeq {
		seq[id] = i
	}
	slices.SortStableFunc(out, func(a, b string) int {
		pa, pb := g.players[a], g.players[b]
		return cmp.Or(
			cmp.Compare(pb.Score, pa.Score),
			cmp.Compare(pb.Wins, pa.Wins),
			cmp.Compare(seq[a], seq[b]),
		)
	})
	return out
}

func (g *Game) standings() []Standing {
	var out []Standing
	for i, id := range g.standingIDs(g.scorers()) {
		p := g.players[id]
		out = append(out, Standing{Player: id, Rank: i + 1, Points: p.Score, Wins: p.Wins})
	}
	return out
}
