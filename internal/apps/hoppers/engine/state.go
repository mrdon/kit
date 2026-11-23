package engine

import "slices"

// State is a snapshot of the game as one surface sees it (see View). The TV
// view is everything public; Snapshot is the TV view.
type State struct {
	Type        MsgType    `json:"type"`
	Role        Role       `json:"role,omitempty"` // which surface this view is for
	Version     uint64     `json:"version"`        // goes up on every visible change; drop frames older than the last one seen
	ServerNow   int64      `json:"serverNow"`
	Phase       Phase      `json:"phase"`
	Sub         Sub        `json:"sub"`
	Round       int        `json:"round"`
	Rounds      int        `json:"rounds"`
	Turn        int        `json:"turn"`                // turns taken this race, the current one included (a new value = a new turn)
	TrackLen    int        `json:"trackLen"`            // = Track.Length (kept for older clients)
	Track       Track      `json:"track"`               // this race's track and its specials
	Layout      Layout     `json:"layout"`              // how the TV draws this race's track
	Theme       string     `json:"theme"`               // this race's art
	FlickBand   float64    `json:"flickBand"`           // width of the phone's green band
	FlickSpot   float64    `json:"flickSpot,omitempty"` // RolePlayer only: centre of the band (a secret from the TV)
	Players     []*Player  `json:"players"`
	Stacks      [][]string `json:"stacks"`
	Ranking     []string   `json:"ranking"`
	TurnOrder   []string   `json:"turnOrder"` // who hops from now on: current first, then about one more rotation
	Current     string     `json:"current"`
	OnDeck      string     `json:"onDeck"`
	Build       string     `json:"build"`
	Deadline    int64      `json:"deadline"`
	Results     []Result   `json:"results"` // the last race's places and points
	Winner      string     `json:"winner"`
	Backward    []string   `json:"backward"`
	Events      []Event    `json:"events"`
	LastHop     *HopInfo   `json:"lastHop"`
	JoinURL     string     `json:"joinUrl"`
	Heats       []*Heat    `json:"heats"`       // this round's races, in run order (kept through the beer break)
	Heat        int        `json:"heat"`        // 1-based heat racing now / up next; 0 outside a round
	HeatName    string     `json:"heatName"`    // "Heat 2", "The Final", "Race 1"
	Final       bool       `json:"final"`       // the race on now is The Final
	Standings   []Standing `json:"standings"`   // people only, best first
	Champion    string     `json:"champion"`    // set at the end of the night
	PlacePoints []int      `json:"placePoints"` // points for 1st, 2nd, ...
	HeatSize    int        `json:"heatSize"`
	// RoundOver: the round's last race is over and its results are up (the
	// beer break). The host's press starts the next round. With LastRace
	// too, the press shows the final standings instead.
	RoundOver bool `json:"roundOver"`
	LastRace  bool `json:"lastRace"` // the race on (or just over) is the night's last

	You  *You      `json:"you,omitempty"`  // RolePlayer only: this phone's own bits
	Host *HostInfo `json:"host,omitempty"` // RoleHost only: controls
}

// Snapshot is the TV's view of the game (everything public).
func (g *Game) Snapshot() State { return g.View(RoleTV, "") }

// publicState is everything any surface may see.
func (g *Game) publicState() State {
	var dl int64
	if !g.deadline.IsZero() {
		dl = g.deadline.UnixMilli()
	}
	s := State{
		Type:        MsgState,
		Version:     g.version,
		ServerNow:   g.now.UnixMilli(),
		Phase:       g.phase,
		Sub:         g.sub,
		Round:       g.round,
		Rounds:      g.cfg.Rules.Rounds,
		Turn:        g.turn,
		TrackLen:    g.track().Length,
		Track:       Track{Length: g.track().Length, Specials: slices.Clone(g.track().Specials)},
		Layout:      g.level().Layout,
		Theme:       g.level().Theme,
		FlickBand:   g.cfg.Rules.FlickBand,
		Players:     g.playersCopy(),
		Stacks:      g.stacksCopy(),
		Ranking:     g.ranking(),
		TurnOrder:   g.turnOrder(),
		Current:     g.current(),
		OnDeck:      g.onDeck(),
		Build:       g.opts.Build,
		Deadline:    dl,
		Results:     append([]Result{}, g.results...),
		Winner:      g.winner,
		Backward:    g.backwardIDs(),
		Events:      append([]Event{}, g.events...),
		LastHop:     g.lastHopCopy(),
		JoinURL:     g.opts.JoinURL,
		Heats:       g.heatsCopy(),
		Standings:   g.standings(),
		Champion:    g.champion,
		PlacePoints: slices.Clone(g.cfg.Rules.PlacePoints),
		HeatSize:    g.cfg.Rules.HeatSize,
	}
	if g.phase == PhaseRacing {
		s.RoundOver = g.roundOver()
		s.LastRace = g.finalRound() && g.heat+1 >= len(g.heats)
		if h := g.curHeat(); h != nil {
			s.Heat, s.HeatName, s.Final = g.heat+1, h.Name, h.Final
		}
	}
	return s
}

func (g *Game) playersCopy() []*Player {
	players := make([]*Player, 0, len(g.joinSeq))
	for _, id := range g.joinSeq {
		p := *g.players[id]
		players = append(players, &p)
	}
	return players
}

func (g *Game) stacksCopy() [][]string {
	stacks := make([][]string, len(g.stacks))
	for i, s := range g.stacks {
		stacks[i] = append([]string{}, s...)
	}
	return stacks
}

func (g *Game) heatsCopy() []*Heat {
	heats := make([]*Heat, len(g.heats))
	for i, h := range g.heats {
		c := *h
		c.Frogs = slices.Clone(h.Frogs)
		c.Results = slices.Clone(h.Results)
		heats[i] = &c
	}
	return heats
}
