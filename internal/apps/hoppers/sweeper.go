package hoppers

import (
	"context"
	"log/slog"
	"time"

	"github.com/mrdon/kit/internal/livegame"
)

// StartSweeper runs the game clock: livegame.Sweeper, sleeping until the
// earliest next_due_at anywhere (a countdown, a bot's hop, a flick timeout,
// a phone timing out) and waking on every publish. See livegame.Sweeper for
// why it is a carve-out from the no-ticker rule, and why two processes both
// running it is safe: every sweep is the same row-locked engine transaction
// as a command, so the loser just finds nothing due.
func (s *Service) StartSweeper(ctx context.Context) {
	livegame.Sweeper{
		Name:  AppName,
		Wake:  s.broker.Changed(),
		Next:  func(ctx context.Context) (time.Duration, bool, error) { return NextDueIn(ctx, s.pool) },
		Sweep: s.sweepAll,
	}.Start(ctx)
}

func (s *Service) sweepAll(ctx context.Context) {
	due, err := DueGames(ctx, s.pool)
	if err != nil {
		slog.Warn("hoppers sweep query failed", "error", err)
		return
	}
	for _, ref := range due {
		if err := s.Sweep(ctx, ref.TenantID, ref.ID); err != nil {
			slog.Warn("hoppers sweep failed", "game_id", ref.ID, "error", err)
		}
	}
}
