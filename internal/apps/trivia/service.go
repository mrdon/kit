package trivia

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Service is the game engine's entry point. It owns the pool, the live
// broker, and (when configured) the cross-process relay.
//
// Every method that mutates a game publishes AFTER its transaction commits,
// reading the committed snapshot -- never from inside, where a rollback would
// already have been fanned out to the room.
type Service struct {
	pool   *pgxpool.Pool
	broker *Broker
	relay  *relay
}

// NewService builds the engine.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, broker: NewBroker()}
}

// Broker exposes the fan-out for the SSE handlers.
func (s *Service) Broker() *Broker { return s.broker }

// ConfigureRelay attaches the Redis relay. Safe to call with a nil client, in
// which case fan-out stays per-process -- exactly correct at one web process,
// and at two the clients' staleness watchdog plus the poll fallback keep the
// game playable rather than frozen.
func (s *Service) ConfigureRelay(rdb *redis.Client) {
	if rdb == nil {
		return
	}
	s.relay = newRelay(rdb, s.broker)
}

// StartRelay begins consuming relayed snapshots. No-op without Redis.
func (s *Service) StartRelay(ctx context.Context) {
	if s.relay != nil {
		s.relay.start(ctx)
	}
}

// publish reads the committed snapshot and fans it out locally and, when a
// relay is configured, to the other web processes.
func (s *Service) publish(ctx context.Context, tenantID, gameID uuid.UUID) {
	snap, err := s.Snapshot(ctx, tenantID, gameID)
	if err != nil {
		return
	}
	s.broker.Publish(gameID, snap)
	if s.relay != nil {
		s.relay.publish(ctx, snap)
	}
}

// Snapshot assembles the complete state of one game.
//
// It is deliberately one function producing one struct, rather than each
// surface running its own queries: three surfaces that assemble state
// separately are three surfaces that can disagree about it, and the whole
// point of the design is that they cannot.
func (s *Service) Snapshot(ctx context.Context, tenantID, gameID uuid.UUID) (*Snapshot, error) {
	game, err := GetGame(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return nil, err
	}
	return s.snapshotOf(ctx, game)
}

func (s *Service) snapshotOf(ctx context.Context, game *Game) (*Snapshot, error) {
	tenantID, gameID := game.TenantID, game.ID

	teams, err := ListTeams(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return nil, err
	}
	cells, err := ListBoardCells(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return nil, err
	}
	standings, err := Leaderboard(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return nil, err
	}

	snap := &Snapshot{
		GameID: gameID, TenantID: tenantID,
		Name: game.Name, Title: game.Title, Phase: game.Phase,
		StateVersion: game.StateVersion,
		ServerNow:    time.Now().UTC(), Deadline: game.PhaseDeadline,
		FinalWager: game.FinalWager, TokenValues: game.TokenValues,
		CellValues: game.CellValues, BoardRows: game.BoardRows, BoardCols: game.BoardColumns,
		Standings:   map[uuid.UUID]int{},
		PublisherID: processID,
	}
	for _, st := range standings {
		snap.Standings[st.TeamID] = st.Total
	}
	for _, c := range cells {
		snap.Board = append(snap.Board, SnapCell{
			ID: c.ID, Col: c.ColIndex, Row: c.RowIndex,
			Topic: c.Topic, Points: c.Points, Played: c.PlayedAt != nil,
		})
	}
	for _, t := range teams {
		snap.Teams = append(snap.Teams, SnapTeam{
			ID: t.ID, Name: t.Name, Score: snap.Standings[t.ID], Eligible: true,
		})
	}

	if game.CurrentRoundID == nil {
		return snap, nil
	}
	if err := s.fillRound(ctx, snap, game, teams); err != nil {
		return nil, err
	}
	return snap, nil
}

// fillRound adds the in-play round, its cards, its chips and -- once the
// round is scored -- the answer.
func (s *Service) fillRound(ctx context.Context, snap *Snapshot, game *Game, teams []Team) error {
	tenantID := game.TenantID
	round, err := GetRound(ctx, s.pool, tenantID, *game.CurrentRoundID)
	if err != nil {
		return err
	}
	answers, err := ListAnswers(ctx, s.pool, tenantID, round.ID)
	if err != nil {
		return err
	}
	slots, err := ListSlots(ctx, s.pool, tenantID, round.ID)
	if err != nil {
		return err
	}
	bets, err := ListBets(ctx, s.pool, tenantID, round.ID)
	if err != nil {
		return err
	}

	sr := &SnapRound{
		ID: round.ID, IsFinal: round.IsFinal, Ordinal: round.Ordinal, Points: round.Points,
		Text: round.Prompt, CorrectValue: round.AnswerValue, CorrectText: round.AnswerText,
	}
	answered := map[uuid.UUID]Answer{}
	for _, a := range answers {
		answered[a.TeamID] = a
	}
	// A team that joined after this round opened is not in its denominator.
	// Without that, "12 of 20 answered" ticks BACKWARDS on the TV as
	// latecomers arrive, and the everyone's-in early close never fires.
	eligibleFrom := map[uuid.UUID]int{}
	for _, t := range teams {
		eligibleFrom[t.ID] = t.EligibleFromOrdinal
	}
	for i := range snap.Teams {
		t := &snap.Teams[i]
		t.Eligible = eligibleFrom[t.ID] <= round.Ordinal
		if a, ok := answered[t.ID]; ok {
			t.Answered = true
			t.StakeLocked = a.Stake != nil
		}
		if t.Eligible {
			sr.EligibleCount++
			if t.Answered {
				sr.AnsweredCount++
			}
		}
	}
	snap.Round = sr

	nameByTeam := map[uuid.UUID]string{}
	for _, t := range snap.Teams {
		nameByTeam[t.ID] = t.Name
	}
	potBySlot := map[uuid.UUID]int{}
	for _, b := range bets {
		potBySlot[b.SlotID] += b.Amount
		snap.Bets = append(snap.Bets, SnapBet(b))
	}
	for i := range snap.Teams {
		for _, b := range bets {
			if b.TeamID == snap.Teams[i].ID {
				snap.Teams[i].ChipsPlaced++
			}
		}
	}
	for _, sl := range slots {
		names := make([]string, 0, len(sl.TeamIDs))
		for _, id := range sl.TeamIDs {
			if n, ok := nameByTeam[id]; ok {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		snap.Slots = append(snap.Slots, SnapSlot{
			ID: sl.ID, Position: sl.Position, Value: sl.Value, Label: sl.Label,
			TeamIDs: sl.TeamIDs, TeamNames: names, Pot: potBySlot[sl.ID],
		})
	}

	if round.ScoredAt == nil {
		return nil
	}
	return s.fillScoring(ctx, snap, round)
}

// fillScoring populates the one field that carries the answer to the public
// surfaces, and does it only for a round that has actually been scored.
func (s *Service) fillScoring(ctx context.Context, snap *Snapshot, round *Round) error {
	deltas := map[uuid.UUID]ScoreDelta{}
	rows, err := ScoredRoundScores(ctx, s.pool, snap.TenantID, snap.GameID)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.RoundID == round.ID {
			deltas[r.TeamID] = ScoreDelta{BoardPoints: r.BoardPoints, BetDelta: r.BetDelta}
		}
	}
	snap.Scoring = &SnapScoring{
		CorrectValue: round.AnswerValue, CorrectText: round.AnswerText,
		WinningSlotID: round.WinningSlotID, Deltas: deltas,
	}
	return nil
}
