package hoppers

import (
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
)

// Snapshot is one game at one instant: what the broker fans out and what
// every surface's frame is projected from.
//
// It carries the engine's views already built, not the engine itself. A
// stream projects a snapshot for its own surface on every change, and
// restoring the engine once per phone per frame would be twenty restores for
// one hop; building the views once at publish is one. The phone view is the
// spectator's, plus each joined phone's own bits.
//
// Everything is JSON round-trippable: the relay carries snapshots between
// web processes as JSON.
type Snapshot struct {
	GameID       uuid.UUID `json:"gameId"`
	TenantID     uuid.UUID `json:"tenantId"`
	Name         string    `json:"name"`
	Title        string    `json:"title"`
	StateVersion int64     `json:"stateVersion"`

	TV    engine.State     `json:"tv"`
	Host  *engine.HostInfo `json:"host"`
	Phone engine.State     `json:"phone"`
	// Mine is each joined phone's own bits, by engine player id.
	Mine map[string]PlayerBits `json:"mine"`
	// HopPower is the last hop's flick strength. Only the flicker's own phone
	// gets it back: next to "perfect" it would give away the sweet spot.
	HopPower float64 `json:"hopPower"`
}

// PlayerBits is what one phone's view has that the spectator's does not.
type PlayerBits struct {
	You       *engine.You `json:"you"`
	FlickBand float64     `json:"flickBand"`
}

// Frame is what every stream event, poll and command response carries: the
// surface's engine view plus the transport fields kit's clients read.
type Frame struct {
	engine.State
	// DeadlineMs is the countdown the screens show (unix ms), 0 when the game
	// is waiting on a person. Only a race runs on a clock: outside one the
	// engine's deadline is stale, and a countdown past its deadline reads to
	// the clients as a dead stream.
	DeadlineMs int64 `json:"deadlineMs"`
	// Game is the game's id. Versions count per game, so a new id resets a
	// client's stale-frame floor.
	Game string `json:"game"`
}

// buildSnapshot projects a live engine into a snapshot.
func buildSnapshot(row *Game, g *engine.Game) *Snapshot {
	s := &Snapshot{
		GameID: row.ID, TenantID: row.TenantID, Name: row.Name, Title: row.Title,
		StateVersion: int64(g.Version()),
		TV:           g.View(engine.RoleTV, ""),
		Host:         g.View(engine.RoleHost, "").Host,
		Phone:        g.View(engine.RolePlayer, ""),
		Mine:         map[string]PlayerBits{},
	}
	for _, p := range s.TV.Players {
		if p.Bot {
			continue
		}
		v := g.View(engine.RolePlayer, p.ID)
		s.Mine[p.ID] = PlayerBits{You: v.You, FlickBand: v.FlickBand}
		if v.LastHop != nil && v.LastHop.Frog == p.ID {
			s.HopPower = v.LastHop.Power
		}
	}
	return s
}

func frameOf(s *Snapshot, st engine.State, now time.Time) Frame {
	f := Frame{State: st, Game: s.GameID.String()}
	f.ServerNow = now.UnixMilli()
	if f.Phase == engine.PhaseRacing {
		f.DeadlineMs = f.Deadline
	}
	return f
}

// ProjectTV is the wall's frame: everything public.
func ProjectTV(s *Snapshot, now time.Time) Frame { return frameOf(s, s.TV, now) }

// ProjectHost is the TV's frame plus the host's controls.
func ProjectHost(s *Snapshot, now time.Time) Frame {
	st := s.TV
	st.Role = engine.RoleHost
	st.Host = s.Host
	return frameOf(s, st, now)
}

// ProjectPlayer is one phone's frame. playerID "" (or one this game does not
// know) is a spectator: the full read-only view with no You.
func ProjectPlayer(s *Snapshot, playerID string, now time.Time) Frame {
	st := s.Phone
	if bits, ok := s.Mine[playerID]; ok {
		st.You = bits.You
		st.FlickBand = bits.FlickBand
		if st.LastHop != nil && st.LastHop.Frog == playerID {
			hop := *st.LastHop // the snapshot is shared by every stream: copy, never mutate
			hop.Power = s.HopPower
			st.LastHop = &hop
		}
	}
	return frameOf(s, st, now)
}
