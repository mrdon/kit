package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Persistence for promotion channels and the sparse state that hangs off them.
// See migration 087 for the shape and why state is sparse.

const channelColumns = `
	id, tenant_id, name, mode, connector, submit_url, feed_tier, verified_at,
	lead_time_days, steps, min_prominence, include_offsite, active, created_at, updated_at`

func scanChannel(row pgx.Row) (*Channel, error) {
	var c Channel
	var steps []byte
	err := row.Scan(
		&c.ID, &c.TenantID, &c.Name, &c.Mode, &c.Connector, &c.SubmitURL,
		&c.FeedTier, &c.VerifiedAt, &c.LeadTimeDays, &steps, &c.MinProminence,
		&c.IncludeOffsite, &c.Active, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	// A malformed steps blob yields no steps rather than an error: the column
	// is written by presets, and a read path should not fail the whole page
	// because one hand-edited row is bad JSON.
	if len(steps) > 0 {
		_ = json.Unmarshal(steps, &c.Steps)
	}
	return &c, nil
}

func listChannels(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]Channel, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+channelColumns+`
		FROM app_event_channels
		WHERE tenant_id = $1
		ORDER BY lower(name)`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing event channels: %w", err)
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning event channel: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func getChannel(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Channel, error) {
	c, err := scanChannel(pool.QueryRow(ctx, `
		SELECT `+channelColumns+`
		FROM app_event_channels
		WHERE tenant_id = $1 AND id = $2`, tenantID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading event channel: %w", err)
	}
	return c, nil
}

func insertChannel(ctx context.Context, pool *pgxpool.Pool, c *Channel) (*Channel, error) {
	steps, err := json.Marshal(c.Steps)
	if err != nil {
		return nil, fmt.Errorf("encoding channel steps: %w", err)
	}
	out, err := scanChannel(pool.QueryRow(ctx, `
		INSERT INTO app_event_channels (
			tenant_id, name, mode, connector, submit_url, feed_tier,
			verified_at, lead_time_days, steps, min_prominence, include_offsite, active)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING `+channelColumns,
		c.TenantID, c.Name, c.Mode, c.Connector, c.SubmitURL, c.FeedTier,
		c.VerifiedAt, c.LeadTimeDays, steps, c.MinProminence, c.IncludeOffsite, c.Active))
	if err != nil {
		return nil, fmt.Errorf("creating event channel: %w", err)
	}
	return out, nil
}

func updateChannel(ctx context.Context, pool *pgxpool.Pool, c *Channel) (*Channel, error) {
	steps, err := json.Marshal(c.Steps)
	if err != nil {
		return nil, fmt.Errorf("encoding channel steps: %w", err)
	}
	out, err := scanChannel(pool.QueryRow(ctx, `
		UPDATE app_event_channels SET
			name = $3, mode = $4, connector = $5, submit_url = $6,
			feed_tier = $7, verified_at = $8, lead_time_days = $9,
			steps = $10, min_prominence = $11, include_offsite = $12, active = $13,
			updated_at = NOW()
		WHERE tenant_id = $1 AND id = $2
		RETURNING `+channelColumns,
		c.TenantID, c.ID, c.Name, c.Mode, c.Connector, c.SubmitURL,
		c.FeedTier, c.VerifiedAt, c.LeadTimeDays, steps, c.MinProminence, c.IncludeOffsite, c.Active))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("updating event channel: %w", err)
	}
	return out, nil
}

func deleteChannel(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) error {
	tag, err := pool.Exec(ctx, `
		DELETE FROM app_event_channels WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("deleting event channel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// loadPromoState reads the sparse state for a tenant's events.
//
// Two things per key: the CURRENT row, and the most recent completion. They
// differ for a cadence, where a `done` row is the anchor for the next cycle
// rather than a final state -- so the list needs to know both "what does the
// row say" and "when was this last actually posted".
func loadPromoState(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, eventIDs []uuid.UUID) (map[promoKey]promoRecord, error) {
	out := map[promoKey]promoRecord{}
	if len(eventIDs) == 0 {
		return out, nil
	}
	rows, err := pool.Query(ctx, `
		SELECT event_id, channel_id, step_key, status, url, note, updated_at
		FROM app_event_promos
		WHERE tenant_id = $1 AND event_id = ANY($2)
		ORDER BY updated_at ASC`, tenantID, eventIDs)
	if err != nil {
		return nil, fmt.Errorf("loading promo state: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			k         promoKey
			status    PromoState
			url, note string
			updated   time.Time
		)
		if err := rows.Scan(&k.eventID, &k.channelID, &k.stepKey, &status, &url, &note, &updated); err != nil {
			return nil, fmt.Errorf("scanning promo state: %w", err)
		}
		rec := out[k]
		// Ascending order means the last write wins as the current state.
		rec.State, rec.URL, rec.Note, rec.UpdatedAt = status, url, note, updated
		if status == PromoDone || status == PromoAutoDone {
			stamp := updated
			rec.LastDoneAt = &stamp
			rec.LastURL = url
		}
		out[k] = rec
	}
	return out, rows.Err()
}

// upsertPromo records what happened to one item.
//
// There is deliberately no way to write a 'todo': a to-do is the ABSENCE of a
// row. Un-doing something therefore deletes rather than writing a state, which
// is what keeps the computed list the single source of truth about whether
// work still applies.
func upsertPromo(ctx context.Context, pool *pgxpool.Pool, tenantID, eventID, channelID uuid.UUID, stepKey string, status PromoState, url, note string, by *uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO app_event_promos (
			tenant_id, event_id, channel_id, step_key, status, url, note, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (tenant_id, event_id, channel_id, step_key) DO UPDATE SET
			status = EXCLUDED.status,
			url = EXCLUDED.url,
			note = EXCLUDED.note,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()`,
		tenantID, eventID, channelID, stepKey, status, url, note, by)
	if err != nil {
		return fmt.Errorf("recording promo state: %w", err)
	}
	return nil
}

func clearPromo(ctx context.Context, pool *pgxpool.Pool, tenantID, eventID, channelID uuid.UUID, stepKey string) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM app_event_promos
		WHERE tenant_id = $1 AND event_id = $2 AND channel_id = $3 AND step_key = $4`,
		tenantID, eventID, channelID, stepKey)
	if err != nil {
		return fmt.Errorf("clearing promo state: %w", err)
	}
	return nil
}
