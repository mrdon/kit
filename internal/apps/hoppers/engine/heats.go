package engine

import (
	"cmp"
	"fmt"
	"slices"
	"time"
)

// --- Rounds and heats ------------------------------------------------------

func (g *Game) finalRound() bool { return g.round >= g.cfg.Rules.Rounds }

// heatSizes splits n people into as few heats of at most HeatSize as
// possible, as evenly as possible (bigger heats first).
func heatSizes(n, size int) []int {
	if n <= 0 {
		return []int{0}
	}
	k := (n + size - 1) / size
	out := make([]int, k)
	for i := range out {
		out[i] = n / k
		if i < n%k {
			out[i]++
		}
	}
	return out
}

func (g *Game) startRound() {
	g.heats = nil
	g.resizeBots() // last chance to resize the field before frogs hit the bar
	g.round++
	g.phase = PhaseRacing
	for _, p := range g.players {
		p.Heat = 0
	}
	people := g.people()
	ladder := g.finalRound() && len(people) > g.cfg.Rules.HeatSize
	dealt := g.dealHeats(people, ladder)
	g.buildHeats(dealt, ladder)
	g.heat = 0
	if len(g.heats) > 1 {
		if ladder {
			g.say("Last round. Top %d make the final.", len(dealt[0])) // people only, not the bots filling in
		} else {
			g.say("Round %d of %d, %d heats. Check your phone.", g.round, g.cfg.Rules.Rounds, len(g.heats))
		}
	}
	g.startHeat()
}

// dealHeats splits this round's people into heats: by standings for the
// ladder round (the best together), else shuffled with colours spread out.
func (g *Game) dealHeats(people []string, ladder bool) [][]string {
	if ladder {
		// seed by standings: the best frogs race each other in The Final
		people = g.standingIDs(people)
	} else {
		g.rng.Shuffle(len(people), func(i, j int) { people[i], people[j] = people[j], people[i] })
	}
	sizes := heatSizes(len(people), g.cfg.Rules.HeatSize)
	if !ladder {
		return g.spreadColours(people, sizes)
	}
	var dealt [][]string
	for _, n := range sizes {
		dealt = append(dealt, slices.Clone(people[:n]))
		people = people[n:]
	}
	return dealt
}

// buildHeats turns dealt groups of people into the round's heats: bots fill
// each one up, The Final (top seeds) runs last, and everyone gets a name.
func (g *Game) buildHeats(dealt [][]string, ladder bool) {
	g.heats = nil
	for _, frogs := range dealt {
		n := len(frogs)
		h := &Heat{Frogs: frogs, Status: HeatWaiting}
		for i := range g.botsFor(n) {
			g.addBot(i)
			h.Frogs = append(h.Frogs, botID(i))
		}
		g.heats = append(g.heats, h)
	}
	if ladder {
		slices.Reverse(g.heats)
		g.heats[len(g.heats)-1].Final = true
	}
	for i, h := range g.heats {
		h.Num = i + 1
		switch {
		case h.Final:
			h.Name = "The Final"
		case len(g.heats) == 1:
			h.Name = fmt.Sprintf("Race %d", g.round)
		default:
			h.Name = fmt.Sprintf("Heat %d", i+1)
		}
		for _, f := range h.Frogs {
			if p := g.players[f]; !p.Bot {
				p.Heat = i + 1
			}
		}
	}
}

func (g *Game) curHeat() *Heat {
	if g.heat < len(g.heats) {
		return g.heats[g.heat]
	}
	return nil
}

// spreadColours deals (already shuffled) people into heats of the given sizes
// so frogs of the same colour race apart where possible: 30 people share 12
// colours, but two green frogs in one race are hard to tell apart on the TV.
// The biggest colour groups are dealt first, each person to the open heat
// with the fewest of their colour (then the most room left).
func (g *Game) spreadColours(people []string, sizes []int) [][]string {
	heats := make([][]string, len(sizes))
	count := make([]map[string]int, len(sizes))
	for i := range count {
		count[i] = map[string]int{}
	}
	groups := map[string][]string{}
	var colours []string
	for _, id := range people {
		c := g.players[id].Color
		if _, ok := groups[c]; !ok {
			colours = append(colours, c)
		}
		groups[c] = append(groups[c], id)
	}
	slices.SortStableFunc(colours, func(a, b string) int { return cmp.Compare(len(groups[b]), len(groups[a])) })
	for _, c := range colours {
		for _, id := range groups[c] {
			best := bestHeatFor(heats, sizes, count, c)
			heats[best] = append(heats[best], id)
			count[best][c]++
		}
	}
	return heats
}

// bestHeatFor is the open heat with the fewest frogs of colour c, then the
// most room left.
func bestHeatFor(heats [][]string, sizes []int, count []map[string]int, c string) int {
	best := -1
	for i := range heats {
		room := sizes[i] - len(heats[i])
		if room <= 0 {
			continue
		}
		if best < 0 || count[i][c] < count[best][c] ||
			(count[i][c] == count[best][c] && room > sizes[best]-len(heats[best])) {
			best = i
		}
	}
	return best
}

func (g *Game) startHeat() {
	h := g.curHeat()
	h.Status = HeatRacing
	g.colourBots(h)
	if g.heat+1 < len(g.heats) {
		g.heats[g.heat+1].Status = HeatNext
	}
	g.finished = false
	g.winner = ""
	g.results = nil
	g.backward = map[string]bool{}
	g.warmUp(h)
	switch {
	case h.Final:
		g.say("The final. Top scorers race.")
	case len(g.heats) > 1:
		g.say("%s of %d. Here we go.", h.Name, len(g.heats))
	default:
		g.say("Race %d of %d. Here we go.", g.round, g.cfg.Rules.Rounds)
	}
	// Turns rotate with no breaks: every frog hops once per rotation, in an
	// order drawn once and kept for the whole race. The only pause is this
	// countdown.
	g.order, g.hopIndex, g.turn = g.shuffledFrogs(), 0, 0
	g.nextOrder = slices.Clone(g.order)
	g.sub = SubReady
	g.deadline = g.now.Add(g.dur(3 * time.Second))
}

// warmUp scatters a heat's frogs over the first three plain coasters,
// stacking as they land (a frog can't sit on a special).
func (g *Game) warmUp(h *Heat) {
	g.resetStacks()
	ids := slices.Clone(h.Frogs)
	g.rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	var spots []int
	for s := 0; len(spots) < 3 && s < len(g.stacks)-1; s++ {
		if _, sp := g.specialAt(s); !sp {
			spots = append(spots, s)
		}
	}
	for _, id := range ids {
		s := spots[g.rng.IntN(len(spots))]
		g.stacks[s] = append(g.stacks[s], id)
	}
}

// afterRace is the host moving on from a race's results: the next heat
// starts (after its countdown), or the next round's first race, or after the
// night's last race the final standings and champion.
func (g *Game) afterRace() {
	switch {
	case g.heat+1 < len(g.heats):
		g.heat++
		g.startHeat()
	case g.finalRound():
		g.sub = ""
		g.deadline = time.Time{}
		g.phase = PhaseFinal
		g.champion = g.pickChampion()
		if c := g.players[g.champion]; c != nil {
			g.say("%s wins the game!", c.Name)
		} else {
			g.say("Game over. Final standings.")
		}
		g.syncBots()
	default:
		g.startRound()
	}
}

// roundOver: the round's last race is over and its results are up. This is
// the beer break; the host starts the next round (or shows the final).
func (g *Game) roundOver() bool {
	return g.phase == PhaseRacing && g.sub == SubRaceEnd && g.heat+1 >= len(g.heats)
}
