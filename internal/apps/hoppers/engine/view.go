package engine

import "slices"

// Role is which surface a View is for.
type Role string

const (
	RoleTV     Role = "tv"     // the big screen: everything public
	RolePlayer Role = "player" // a phone: public race info plus its own bits
	RoleHost   Role = "host"   // whoever runs the night: the TV view plus controls
)

// Roles is every surface.
var Roles = []Role{RoleTV, RolePlayer, RoleHost}

// You is a phone's own bits in the player view.
type You struct {
	ID        string `json:"id"`
	Connected bool   `json:"connected"`
	Heat      int    `json:"heat"`     // 1-based heat this round, 0 = not racing this round
	HeatName  string `json:"heatName"` // "Heat 2", "The Final", "Race 1", or ""
	Racing    bool   `json:"racing"`   // your frog is on the track now
	MyTurn    bool   `json:"myTurn"`   // flick now
	OnDeck    bool   `json:"onDeck"`   // you're up next
	Place     int    `json:"place"`    // where your frog is in this race (1 = in front), 0 if not racing
	Score     int    `json:"score"`
	Rank      int    `json:"rank"` // tonight's standings, 0 if not in them yet
	Wins      int    `json:"wins"`
}

// HostInfo is the host's controls in the host view.
type HostInfo struct {
	CanAdvance bool   `json:"canAdvance"` // the host button does something now
	Advance    string `json:"advance"`    // what the button says, "" when it does nothing
	// NextDeadline (unix ms, 0 = none) is when the game's own clock next
	// moves it on. Connection timeouts aren't in it.
	NextDeadline int64         `json:"nextDeadline"`
	Config       ConfigSummary `json:"config"`
}

// ConfigSummary is the night's setup at a glance.
type ConfigSummary struct {
	Rounds         int            `json:"rounds"`
	HeatSize       int            `json:"heatSize"`
	MinFrogs       int            `json:"minFrogs"`
	FlickTimeoutMs int64          `json:"flickTimeoutMs"`
	Levels         []LevelSummary `json:"levels"`
}

// LevelSummary is one race of the cup at a glance.
type LevelSummary struct {
	Name     string `json:"name"`
	Layout   Layout `json:"layout"`
	Theme    string `json:"theme"`
	Length   int    `json:"length"`
	Specials int    `json:"specials"`
}

// View is the game as one surface should see it. The TV gets everything
// public. A player gets what a phone needs (the TV's drawing data, the track,
// stacks, turn order and join link, are left out) plus You, their own bits;
// an unknown playerID gets the same view with no You. The host gets the TV
// view plus Host: what the button does, the next deadline and the setup.
// Nothing in any view is private to the host except Host itself.
func (g *Game) View(role Role, playerID string) State {
	s := g.publicState()
	s.Role = role
	if s.LastHop != nil && (role != RolePlayer || s.LastHop.Frog != playerID) {
		// a flick's power next to "perfect" would give away where the sweet
		// spot is; only the flicker's own phone gets it back
		s.LastHop.Power = 0
	}
	switch role {
	case RolePlayer:
		s.Track = Track{Length: s.Track.Length, Specials: []Special{}}
		s.Stacks, s.TurnOrder = nil, nil
		s.Layout, s.Theme, s.JoinURL = "", "", ""
		s.FlickSpot = FlickSpot
		s.FlickBand = g.FlickBandFor(playerID)
		s.You = g.you(playerID, s)
	case RoleHost:
		s.Host = g.hostInfo()
	case RoleTV:
	default:
	}
	return s
}

func (g *Game) you(id string, s State) *You {
	p := g.players[id]
	if p == nil || p.Bot {
		return nil
	}
	y := &You{ID: id, Connected: p.Connected, Heat: p.Heat, Score: p.Score, Wins: p.Wins,
		MyTurn: g.phase == PhaseRacing && g.sub == SubFlick && s.Current == id, OnDeck: s.OnDeck == id}
	if p.Heat > 0 && p.Heat <= len(g.heats) {
		y.HeatName = g.heats[p.Heat-1].Name
	}
	if i := slices.Index(s.Ranking, id); i >= 0 && g.phase == PhaseRacing {
		y.Racing, y.Place = true, i+1
	}
	for _, st := range s.Standings {
		if st.Player == id {
			y.Rank = st.Rank
		}
	}
	return y
}

func (g *Game) hostInfo() *HostInfo {
	h := &HostInfo{CanAdvance: g.canHostAdvance(), Advance: g.advanceLabel(), Config: g.configSummary()}
	if t := g.timerDue(); !t.IsZero() {
		h.NextDeadline = t.UnixMilli()
	}
	return h
}

// advanceLabel is what the host button says right now.
func (g *Game) advanceLabel() string {
	switch g.phase {
	case PhaseLobby:
		return "Start"
	case PhaseFinal:
		return "New game"
	case PhaseRacing:
		switch {
		case g.sub != SubRaceEnd:
		case g.heat+1 < len(g.heats):
			return "Next heat"
		case g.finalRound():
			return "Final scores"
		default:
			return "Next round"
		}
	}
	return ""
}

func (g *Game) configSummary() ConfigSummary {
	r := g.cfg.Rules
	c := ConfigSummary{Rounds: r.Rounds, HeatSize: r.HeatSize, MinFrogs: r.MinFrogs,
		FlickTimeoutMs: g.dur(r.FlickTimeout).Milliseconds()}
	for _, l := range g.cfg.Levels {
		c.Levels = append(c.Levels, LevelSummary{Name: l.Name, Layout: l.Layout, Theme: l.Theme, Length: l.Track.Length, Specials: len(l.Track.Specials)})
	}
	return c
}
