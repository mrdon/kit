package devices

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pairing limits. Ten minutes is long enough to walk to the office for the
// phone and back; twenty pending per workspace bounds what an approver has
// to scan, and what a joker on the wifi can add to it.
const (
	pairingTTL          = 10 * time.Minute
	maxPendingPerTenant = 20
)

// Pairing statuses.
const (
	statusPending   = "pending"
	statusApproved  = "approved"
	statusCancelled = "cancelled"
)

// Pairing is one row of app_device_pairings.
type Pairing struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	UserCode  string
	Picture   string
	Status    string
	ActorID   *uuid.UUID
	ClientIP  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// errGone is any reason a pairing can no longer be acted on: expired,
// cancelled, already approved, or never existed.
var errGone = errors.New("pairing is no longer pending")

// pictures is the fixed set a device may show. Distinct at a glance on a
// phone, so an approver tapping one of three is never guessing.
var pictures = []string{
	"🦊", "🐸", "🐙", "🦉", "🐝", "🦀", "🐢", "🦋", "🐠", "🦜",
	"🍎", "🍋", "🍇", "🍉", "🍓", "🥑", "🌽", "🥕", "🍄", "🌶️",
	"⚽", "🎸", "🎲", "🎯", "🎈", "🪁", "🎺", "🥁", "🎷", "🏀",
	"🚲", "🚀", "⛵", "🚂", "🛸", "🎪", "🏰", "⛺", "🗼", "🌋",
	"☂️", "⚓", "🔑", "🧲", "💎", "🧭", "🔔", "🪴", "🕯️", "🧩",
}

// userCodeAlphabet avoids the characters that are read wrong across a bar:
// no 0/O, 1/I/L, or 8/B.
const userCodeAlphabet = "ACDEFGHJKMNPQRTUVWXYZ2345679"

const pairingColumns = `id, tenant_id, user_code, picture, status, actor_id, client_ip, created_at, expires_at`

func scanPairing(row pgx.Row) (*Pairing, error) {
	p := &Pairing{}
	err := row.Scan(&p.ID, &p.TenantID, &p.UserCode, &p.Picture, &p.Status, &p.ActorID, &p.ClientIP, &p.CreatedAt, &p.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("scanning pairing: %w", err)
	}
	return p, nil
}

func randomPicture() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(pictures))))
	if err != nil {
		return "", fmt.Errorf("picking picture: %w", err)
	}
	return pictures[n.Int64()], nil
}

func randomUserCode() (string, error) { return randomCode(4) }

// randomCode draws n characters from the unambiguous alphabet.
func randomCode(n int) (string, error) {
	out := make([]byte, n)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(userCodeAlphabet))))
		if err != nil {
			return "", fmt.Errorf("picking code: %w", err)
		}
		out[i] = userCodeAlphabet[n.Int64()]
	}
	return string(out), nil
}

// createPairing inserts a pending pairing. The user code is unique among
// the tenant's pending rows (retried on collision) so typing it is
// unambiguous.
func createPairing(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, deviceCodeHash, clientIP string) (*Pairing, error) {
	picture, err := randomPicture()
	if err != nil {
		return nil, err
	}
	for range 5 {
		code, err := randomUserCode()
		if err != nil {
			return nil, err
		}
		var taken bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM app_device_pairings
			  WHERE tenant_id = $1 AND user_code = $2 AND status = 'pending' AND expires_at > now())
		`, tenantID, code).Scan(&taken); err != nil {
			return nil, fmt.Errorf("checking pairing code: %w", err)
		}
		if taken {
			continue
		}
		p, err := scanPairing(pool.QueryRow(ctx, `
			INSERT INTO app_device_pairings (tenant_id, device_code_hash, user_code, picture, client_ip, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING `+pairingColumns,
			tenantID, deviceCodeHash, code, picture, clientIP, time.Now().Add(pairingTTL)))
		if err != nil {
			return nil, fmt.Errorf("creating pairing: %w", err)
		}
		return p, nil
	}
	return nil, errors.New("could not pick a free pairing code")
}

// pairingByDeviceCode finds the row behind a device's cookie, whatever its
// status; nil when missing.
func pairingByDeviceCode(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, hash string) (*Pairing, error) {
	return scanPairing(pool.QueryRow(ctx, `
		SELECT `+pairingColumns+` FROM app_device_pairings
		WHERE tenant_id = $1 AND device_code_hash = $2
	`, tenantID, hash))
}

// pendingPairings lists what an approver can act on, oldest first.
func pendingPairings(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]Pairing, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+pairingColumns+` FROM app_device_pairings
		WHERE tenant_id = $1 AND status = 'pending' AND expires_at > now()
		ORDER BY created_at
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing pairings: %w", err)
	}
	defer rows.Close()
	var out []Pairing
	for rows.Next() {
		p, err := scanPairing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// pendingPairing fetches one actionable row by id or by user code.
func pendingPairing(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, id *uuid.UUID, code string) (*Pairing, error) {
	return scanPairing(pool.QueryRow(ctx, `
		SELECT `+pairingColumns+` FROM app_device_pairings
		WHERE tenant_id = $1 AND status = 'pending' AND expires_at > now()
		  AND (($2::uuid IS NOT NULL AND id = $2) OR ($2::uuid IS NULL AND user_code = $3))
	`, tenantID, id, code))
}

// setPairingStatus moves a pending row to approved (with its actor) or
// cancelled. errGone when the row was no longer pending.
func setPairingStatus(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID, status string, actorID *uuid.UUID) error {
	tag, err := pool.Exec(ctx, `
		UPDATE app_device_pairings SET status = $3, actor_id = $4
		WHERE tenant_id = $1 AND id = $2 AND status = 'pending' AND expires_at > now()
	`, tenantID, id, status, actorID)
	if err != nil {
		return fmt.Errorf("updating pairing: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errGone
	}
	return nil
}

// claimPairing is the device's half of an approval: the row is deleted and
// its actor returned, exactly once, to the holder of the device code.
func claimPairing(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, hash string) (uuid.UUID, error) {
	var actorID uuid.UUID
	err := pool.QueryRow(ctx, `
		DELETE FROM app_device_pairings
		WHERE tenant_id = $1 AND device_code_hash = $2 AND status = 'approved' AND actor_id IS NOT NULL
		RETURNING actor_id
	`, tenantID, hash).Scan(&actorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errGone
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("claiming pairing: %w", err)
	}
	return actorID, nil
}

// countPending bounds the per-tenant list.
func countPending(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM app_device_pairings
		WHERE tenant_id = $1 AND status = 'pending' AND expires_at > now()
	`, tenantID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting pairings: %w", err)
	}
	return n, nil
}

// sweepPairings drops rows nothing can act on any more: expired, or
// settled long enough ago that the device has had every chance to poll.
func sweepPairings(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM app_device_pairings WHERE tenant_id = $1 AND expires_at < now()
	`, tenantID)
	if err != nil {
		return fmt.Errorf("sweeping pairings: %w", err)
	}
	return nil
}

// choices returns the three pictures an approver sees for a pairing: the
// real one and two decoys, in an order that is stable across polls (seeded
// by the pairing id) so the buttons don't shuffle under a thumb.
func (p *Pairing) choices() []string {
	seed := sha256.Sum256(p.ID[:])
	pick := func(i int) uint64 { return binary.LittleEndian.Uint64(seed[i*8:]) }
	others := make([]string, 0, len(pictures)-1)
	for _, pic := range pictures {
		if pic != p.Picture {
			others = append(others, pic)
		}
	}
	a := others[pick(0)%uint64(len(others))]
	others = remove(others, a)
	b := others[pick(1)%uint64(len(others))]
	out := []string{p.Picture, a, b}
	// Fisher-Yates with the remaining seed words.
	for i := len(out) - 1; i > 0; i-- {
		j := int(pick(i+1) % uint64(i+1))
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func remove(in []string, s string) []string {
	out := in[:0:0]
	for _, v := range in {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}
