package hoppers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
	"github.com/mrdon/kit/internal/livegame"
)

// Standing is one person's place in a finished night, names copied out so the
// history needs no engine to read.
type Standing struct {
	Name   string `json:"name"`
	Rank   int    `json:"rank"`
	Points int    `json:"points"`
	Wins   int    `json:"wins"`
}

// Result is one finished night.
type Result struct {
	ID         uuid.UUID  `json:"id"`
	GameID     *uuid.UUID `json:"game_id"`
	Title      string     `json:"title"`
	Standings  []Standing `json:"standings"`
	FinishedAt time.Time  `json:"finished_at"`
}

// standingsOf copies the engine's final standings out with names.
func standingsOf(s engine.State) []Standing {
	names := make(map[string]string, len(s.Players))
	for _, p := range s.Players {
		names[p.ID] = p.Name
	}
	out := make([]Standing, 0, len(s.Standings))
	for _, st := range s.Standings {
		out = append(out, Standing{Name: names[st.Player], Rank: st.Rank, Points: st.Points, Wins: st.Wins})
	}
	return out
}

// insertResult records a night that has just finished. tx is the change
// that finished it, so the history and the game can never disagree.
func insertResult(ctx context.Context, tx pgx.Tx, row *Game, standings []Standing) error {
	raw, err := json.Marshal(standings)
	if err != nil {
		return fmt.Errorf("encoding hoppers standings: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO app_hoppers_results (tenant_id, game_id, title, standings)
		VALUES ($1, $2, $3, $4)`, row.TenantID, row.ID, row.Title, raw)
	if err != nil {
		return fmt.Errorf("recording hoppers result: %w", err)
	}
	return nil
}

// ListResults returns the workspace's finished nights, newest first.
func ListResults(ctx context.Context, q Querier, tenantID uuid.UUID, limit int) ([]Result, error) {
	rows, err := q.Query(ctx, `
		SELECT id, game_id, title, standings, finished_at FROM app_hoppers_results
		 WHERE tenant_id = $1 ORDER BY finished_at DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing hoppers results: %w", err)
	}
	defer rows.Close()
	out := []Result{}
	for rows.Next() {
		var r Result
		var raw []byte
		if err := rows.Scan(&r.ID, &r.GameID, &r.Title, &raw, &r.FinishedAt); err != nil {
			return nil, fmt.Errorf("scanning hoppers result: %w", err)
		}
		if err := json.Unmarshal(raw, &r.Standings); err != nil {
			return nil, fmt.Errorf("decoding hoppers standings: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetFeedbackChannel reads where ratings go; nowhere until an admin picks.
func GetFeedbackChannel(ctx context.Context, q Querier, tenantID uuid.UUID) (livegame.FeedbackChannel, error) {
	var c livegame.FeedbackChannel
	err := q.QueryRow(ctx, `
		SELECT feedback_channel_id, feedback_channel_name
		  FROM app_hoppers_settings WHERE tenant_id = $1`, tenantID).Scan(&c.ID, &c.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return livegame.FeedbackChannel{}, nil
	}
	if err != nil {
		return c, fmt.Errorf("loading hoppers feedback channel: %w", err)
	}
	return c, nil
}

// SaveFeedbackChannel stores the workspace's choice.
func SaveFeedbackChannel(ctx context.Context, q Querier, tenantID uuid.UUID, c livegame.FeedbackChannel) error {
	_, err := q.Exec(ctx, `
		INSERT INTO app_hoppers_settings (tenant_id, feedback_channel_id, feedback_channel_name)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE
		   SET feedback_channel_id = EXCLUDED.feedback_channel_id,
		       feedback_channel_name = EXCLUDED.feedback_channel_name,
		       updated_at = now()`, tenantID, c.ID, c.Name)
	if err != nil {
		return fmt.Errorf("saving hoppers feedback channel: %w", err)
	}
	return nil
}
