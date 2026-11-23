package engine

import (
	"fmt"
	"slices"
	"time"
)

// --- Joining ---------------------------------------------------------------

func (g *Game) Join(id, name, color, hat string) {
	if id == "" {
		return
	}
	if len(name) > 24 {
		name = name[:24]
	}
	p, ok := g.players[id]
	if !ok {
		p = &Player{ID: id}
		g.players[id] = p
		g.joinSeq = append(g.joinSeq, id)
		g.say("%s is in.", name)
	}
	p.Name, p.Hat = name, hat
	p.Color = g.uniqueColor(id, color)
	p.Connected = true
	g.markSeen(id)
	if !ok && g.phase == PhaseRacing {
		g.lateJoin(id)
	}
	g.syncBots()
	g.freeColor(id, p.Color)
	g.changed()
}

// lateJoin seats someone who arrives mid-round: in the smallest heat that
// hasn't started yet, else in the race running now (on the first coaster, on
// top of whoever's there) if it has room, else they race next round.
func (g *Game) lateJoin(id string) {
	if best := g.openHeat(); best >= 0 {
		h := g.heats[best]
		// take a bot's seat if the heat had one
		if i := slices.IndexFunc(h.Frogs, func(f string) bool { return g.players[f].Bot }); i >= 0 && len(h.Frogs) >= g.cfg.Rules.MinFrogs {
			h.Frogs = slices.Delete(h.Frogs, i, i+1)
		}
		h.Frogs = append(h.Frogs, id)
		g.players[id].Heat = best + 1
		return
	}
	if h := g.curHeat(); h != nil && h.Status == HeatRacing && g.humansIn(h) < g.cfg.Rules.HeatSize {
		h.Frogs = append(h.Frogs, id)
		g.players[id].Heat = g.heat + 1
		g.stacks[0] = append(g.stacks[0], id)
	}
}

// openHeat is the index of the smallest heat that hasn't started and has room
// for one more person, or -1.
func (g *Game) openHeat() int {
	best := -1
	for i, h := range g.heats {
		if h.Status == HeatRacing || h.Status == HeatDone {
			continue
		}
		if g.humansIn(h) < g.cfg.Rules.HeatSize && (best < 0 || g.humansIn(h) < g.humansIn(g.heats[best])) {
			best = i
		}
	}
	return best
}

func (g *Game) humansIn(h *Heat) int {
	n := 0
	for _, f := range h.Frogs {
		if p := g.players[f]; p != nil && !p.Bot {
			n++
		}
	}
	return n
}

// --- Connections -----------------------------------------------------------

// SetConnected marks a person connected or not (the "leave" command).
func (g *Game) SetConnected(id string, c bool) {
	if p, ok := g.players[id]; ok {
		p.Connected = c
		if c {
			g.markSeen(id)
		}
		g.syncBots()
		g.changed()
	}
}

// Seen records that a person's phone is still there (a heartbeat: a stream
// ping, a poll, any request), first processing any timers due at now. With
// Options.SeenWindow set, a person who isn't seen for that long drops out on
// their own, so a server never has to notice a socket closing. Seeing someone
// who had dropped out brings them back. Unknown ids and bots are ignored.
func (g *Game) Seen(id string, now time.Time) {
	g.Advance(now)
	g.seen(id)
}

func (g *Game) seen(id string) {
	p, ok := g.players[id]
	if !ok || p.Bot {
		return
	}
	g.markSeen(id)
	if !p.Connected {
		p.Connected = true
		g.syncBots()
		g.changed()
	}
}

func (g *Game) markSeen(id string) {
	if p := g.players[id]; p != nil && !p.Bot {
		g.lastSeen[id] = g.now
	}
}

// seenExpiry is when id drops out for not being seen, or zero if they can't.
func (g *Game) seenExpiry(id string) time.Time {
	p := g.players[id]
	if g.opts.SeenWindow <= 0 || p == nil || p.Bot || !p.Connected {
		return time.Time{}
	}
	return g.lastSeen[id].Add(g.opts.SeenWindow)
}

// nextExpiry is the earliest time a connected person drops out unseen.
func (g *Game) nextExpiry() time.Time {
	var first time.Time
	for _, id := range g.joinSeq {
		if t := g.seenExpiry(id); !t.IsZero() && (first.IsZero() || t.Before(first)) {
			first = t
		}
	}
	return first
}

// expireUnseen disconnects everyone not seen within the window.
func (g *Game) expireUnseen() {
	for _, id := range g.joinSeq {
		if t := g.seenExpiry(id); !t.IsZero() && !g.now.Before(t) {
			g.players[id].Connected = false
			g.syncBots()
			g.changed()
		}
	}
}

// --- Colours ---------------------------------------------------------------

// freeColor moves any bot off a colour a human just picked, so every frog on
// the TV is distinguishable.
func (g *Game) freeColor(owner, color string) {
	used := map[string]bool{}
	for _, p := range g.players {
		used[p.Color] = true
	}
	for _, p := range g.players {
		if p.Bot && p.ID != owner && p.Color == color {
			for _, c := range palette {
				if !used[c] {
					p.Color = c
					used[c] = true
					break
				}
			}
		}
	}
}

// uniqueColor keeps every person's frog a different colour: if another
// connected person already has this one, hand out the first free colour.
func (g *Game) uniqueColor(owner, color string) string {
	used := map[string]bool{}
	for _, p := range g.players {
		if !p.Bot && p.ID != owner && p.Connected {
			used[p.Color] = true
		}
	}
	if !used[color] {
		return color
	}
	for _, c := range palette {
		if !used[c] {
			return c
		}
	}
	return color
}

var palette = []string{"#2ecc71", "#e84393", "#e67e22", "#3498db", "#9b59b6", "#f1c40f",
	"#1abc9c", "#e74c3c", "#8e5a2b", "#fd79a8", "#00cec9", "#636e72"}

// colourBots gives each bot in h a colour nobody else in the race has
// (preferring one no person tonight has picked). The same bot can race in
// several heats, so this runs as each heat lines up.
func (g *Game) colourBots(h *Heat) {
	inRace := map[string]int{}
	for _, f := range h.Frogs {
		inRace[g.players[f].Color]++
	}
	picked := map[string]bool{}
	for _, p := range g.players {
		if !p.Bot && p.Connected {
			picked[p.Color] = true
		}
	}
	for _, f := range h.Frogs {
		b := g.players[f]
		if !b.Bot || inRace[b.Color] <= 1 {
			continue
		}
		free := freeColour(inRace, picked)
		if free == "" {
			continue
		}
		inRace[b.Color]--
		b.Color = free
		inRace[free]++
	}
}

// freeColour is a palette colour nobody in the race has, preferring one no
// person picked; "" if the race uses them all.
func freeColour(inRace map[string]int, picked map[string]bool) string {
	free := ""
	for _, c := range palette {
		if inRace[c] == 0 && (free == "" || !picked[c]) {
			free = c
			if !picked[c] {
				break
			}
		}
	}
	return free
}

// --- Bots ------------------------------------------------------------------

func botID(i int) string { return fmt.Sprintf("bot-%d", i) }

func (g *Game) addBot(i int) {
	if _, ok := g.players[botID(i)]; ok {
		return
	}
	b := botFrogs[i]
	b.ID = botID(i)
	b.Bot = true
	b.Connected = true
	used := map[string]bool{}
	for _, p := range g.players {
		used[p.Color] = true
	}
	if used[b.Color] {
		for _, c := range palette {
			if !used[c] {
				b.Color = c
				break
			}
		}
	}
	g.players[b.ID] = &b
	g.joinSeq = append(g.joinSeq, b.ID)
}

// people is every connected person, in join order.
func (g *Game) people() []string {
	var ids []string
	for _, id := range g.joinSeq {
		if p := g.players[id]; !p.Bot && p.Connected {
			ids = append(ids, id)
		}
	}
	return ids
}

// botsFor is how many bots a race with this many people gets.
func (g *Game) botsFor(people int) int {
	return min(max(0, g.cfg.Rules.MinFrogs-people), len(botFrogs))
}

// syncBots keeps exactly the bots the next round will need (the smallest
// heat's shortfall), and sends bots home as people join. Only between rounds:
// a round in progress keeps its frogs.
func (g *Game) syncBots() {
	if g.phase == PhaseRacing && !g.roundOver() {
		return
	}
	g.resizeBots()
}

// resizeBots is syncBots without the "not mid-round" guard.
func (g *Game) resizeBots() {
	sizes := heatSizes(len(g.people()), g.cfg.Rules.HeatSize)
	want := g.botsFor(slices.Min(sizes))
	for i := range want {
		g.addBot(i)
	}
	for i := want; i < len(botFrogs); i++ {
		id := botID(i)
		if _, ok := g.players[id]; ok && !g.inHeats(id) {
			delete(g.players, id)
			g.joinSeq = slices.DeleteFunc(g.joinSeq, func(x string) bool { return x == id })
		}
	}
}

// inHeats: a frog in this round's heats stays until the next round, so the
// beer-break recap can still show it.
func (g *Game) inHeats(id string) bool {
	for _, h := range g.heats {
		if slices.Contains(h.Frogs, id) {
			return true
		}
	}
	return false
}

func (g *Game) name(id string) string {
	if p, ok := g.players[id]; ok {
		return p.Name
	}
	return "???"
}
