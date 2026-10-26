package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Instance is one running Kit process as recorded in kit_instances. The
// table is global, not tenant-scoped: a process serves every tenant.
type Instance struct {
	ID          uuid.UUID
	Build       string
	BuiltAt     *time.Time
	Hostname    string
	HeartbeatAt time.Time
}

// instanceRetention is how long a dead process's row is kept. Long enough to
// answer "what was running yesterday" while debugging, short enough that the
// table never grows past a few rows.
const instanceRetention = 7 * 24 * time.Hour

// HeartbeatInstance records that this process is alive, and prunes rows of
// processes long gone.
func HeartbeatInstance(ctx context.Context, pool *pgxpool.Pool, inst Instance) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO kit_instances (id, build, built_at, hostname)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET heartbeat_at = now()
	`, inst.ID, inst.Build, inst.BuiltAt, inst.Hostname)
	if err != nil {
		return fmt.Errorf("recording instance heartbeat: %w", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM kit_instances WHERE heartbeat_at < now() - $1::interval`,
		instanceRetention.String()); err != nil {
		return fmt.Errorf("pruning dead instances: %w", err)
	}
	return nil
}

// NewerLiveInstance returns a process other than self that has heartbeated
// within window and was built strictly later than builtAt, or nil if there
// is none. A nil builtAt (a build with no stamp, i.e. a local `go run`)
// loses to any stamped build. Equal stamps never supersede each other: two
// processes of one build run the same code, and retiring either could take
// down the one the router is sending traffic to.
func NewerLiveInstance(ctx context.Context, pool *pgxpool.Pool, self uuid.UUID, builtAt *time.Time, window time.Duration) (*Instance, error) {
	var inst Instance
	err := pool.QueryRow(ctx, `
		SELECT id, build, built_at, hostname, heartbeat_at
		FROM kit_instances
		WHERE id <> $1
		  AND heartbeat_at > now() - $2::interval
		  AND built_at > COALESCE($3::timestamptz, '-infinity')
		ORDER BY built_at DESC
		LIMIT 1
	`, self, window.String(), builtAt).Scan(&inst.ID, &inst.Build, &inst.BuiltAt, &inst.Hostname, &inst.HeartbeatAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // no newer instance is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("looking for a newer instance: %w", err)
	}
	return &inst, nil
}
