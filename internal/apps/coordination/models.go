// Package coordination implements the multi-party coordination engine.
//
// Phase 1 ships meeting scheduling on Slack only. The architecture is a
// deterministic Go state machine driven by a cron sweeper, with stateless
// LLM calls at the message edges (drafting outbound, parsing replies).
// Outbound + inbound messaging goes through internal/services/messenger;
// the existing session_events table is the audit log.
package coordination

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Kind values stored in app_coordinations.kind.
const KindMeeting = "meeting"

// Status values for app_coordinations.status.
const (
	StatusActive    = "active"
	StatusConverged = "converged"
	StatusConfirmed = "confirmed"
	StatusAbandoned = "abandoned"
	StatusCancelled = "cancelled"
)

// Participant status values.
const (
	ParticipantPending   = "pending"
	ParticipantContacted = "contacted"
	ParticipantResponded = "responded"
	ParticipantTimedOut  = "timed_out"
	ParticipantDeclined  = "declined"
)

// Slot is a candidate meeting time. Keys generated from Start+End are
// used as identifiers across the engine, decision cards, and parser
// output.
type Slot struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Key returns a stable string identifier for this slot, used as the
// constraint key in participant.constraints and as the option_id on
// decision cards.
func (s Slot) Key() string {
	return s.Start.UTC().Format(time.RFC3339) + "|" + s.End.UTC().Format(time.RFC3339)
}

// TimeWindow is a free-time window collected from the organizer when
// they don't have an iCal configured ("Tuesday 9-12, Wednesday all
// morning"). The slot generator intersects these with each candidate
// slot to determine availability.
type TimeWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// ProposalState captures the LLM solver's most recent proposal so the
// next outbound to participants can include the running summary +
// proposed times.
type ProposalState struct {
	Summary       string   `json:"summary,omitempty"`
	ProposedTimes []string `json:"proposed_times,omitempty"`
}

// CoordinationConfig is the kind-specific config jsonb on app_coordinations.
// Phase 1 only has meeting fields; later kinds will discriminate via Kind.
type CoordinationConfig struct {
	Title            string        `json:"title"`
	DurationMinutes  int           `json:"duration_minutes"`
	StartDate        time.Time     `json:"start_date"`
	EndDate          time.Time     `json:"end_date"`
	OrganizerWindows []TimeWindow  `json:"organizer_windows,omitempty"`
	CandidateSlots   []Slot        `json:"candidate_slots"`
	AutoApprove      bool          `json:"auto_approve"`
	Notes            string        `json:"notes,omitempty"`
	OrganizerTZ      string        `json:"organizer_tz,omitempty"`
	LatestProposal   ProposalState `json:"latest_proposal,omitzero"`
}

// CoordinationResult is the kind-specific result jsonb populated on
// status transition to confirmed.
type CoordinationResult struct {
	ChosenSlot *Slot `json:"chosen_slot,omitempty"`
}

// Coordination is one workflow instance.
type Coordination struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	OrganizerID   uuid.UUID
	Kind          string
	Status        string
	Config        CoordinationConfig
	Result        *CoordinationResult
	DeadlineAt    *time.Time
	ShepherdJobID *uuid.UUID
	RoundCount    int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// MaxRounds is the negotiation cap. After this many rounds without
// convergence, the engine surfaces an abandonment card to the organizer.
const MaxRounds = 5

// Round is one ask/answer cycle for a participant. asked_slots
// snapshots the candidate set at the time of the ask so re-engagement
// drafts can compare old vs new.
type Round struct {
	Round       int        `json:"round"`
	AskedAt     time.Time  `json:"asked_at"`
	AskedSlots  []Slot     `json:"asked_slots"`
	RespondedAt *time.Time `json:"responded_at,omitempty"`
}

// SlotVerdict is what the parser records about a participant's stance
// on a specific slot.
type SlotVerdict string

const (
	VerdictAccept      SlotVerdict = "accept"
	VerdictReject      SlotVerdict = "reject"
	VerdictUnspecified SlotVerdict = "unspecified"
)

// Constraints is the parser's current understanding of what a
// participant can/can't do. Replaced wholesale on each parse — the
// "current truth" from reading the full message log.
type Constraints struct {
	SlotVerdicts map[string]SlotVerdict `json:"slot_verdicts,omitempty"`
	Notes        string                 `json:"notes,omitempty"`
}

// Participant is per-counterparty state.
type Participant struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	CoordinationID uuid.UUID
	Identifier     string // slack user id in Phase 1
	UserID         *uuid.UUID
	SessionID      *uuid.UUID
	Channel        string
	Status         string
	Rounds         []Round
	Constraints    Constraints
	// Availability is free-form natural-language availability text
	// accumulated across the participant's replies. The LLM solver
	// reads this to propose viable times. Replaces the discrete
	// constraint-voting model (which couldn't handle "anytime after
	// Tuesday" or "Fri or Sat morning").
	Availability string
	// AcceptedTime is the RFC3339 string (or any unique key) of the
	// proposed time the participant explicitly accepted in the most
	// recent round. Convergence = all active participants have the same
	// non-empty AcceptedTime.
	AcceptedTime string
	NudgeCount   int
	NextNudgeAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateCoordination inserts a new coordination. Result is nil at
// creation time.
func CreateCoordination(ctx context.Context, pool *pgxpool.Pool, c *Coordination) error {
	configJSON, err := json.Marshal(c.Config)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	row := pool.QueryRow(ctx, `
		INSERT INTO app_coordinations
		    (tenant_id, organizer_id, kind, status, config, deadline_at, shepherd_job_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at
	`, c.TenantID, c.OrganizerID, c.Kind, c.Status, configJSON, c.DeadlineAt, c.ShepherdJobID)
	return row.Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
}

// GetCoordination loads a coordination by id within the tenant.
func GetCoordination(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Coordination, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, tenant_id, organizer_id, kind, status, config, result,
		       deadline_at, shepherd_job_id, round_count, created_at, updated_at
		FROM app_coordinations
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, id)
	return scanCoordination(row)
}

// ListActiveCoordinations returns all active coordinations for a tenant.
// Used by the cron sweeper.
func ListActiveCoordinations(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]Coordination, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, organizer_id, kind, status, config, result,
		       deadline_at, shepherd_job_id, round_count, created_at, updated_at
		FROM app_coordinations
		WHERE tenant_id = $1 AND status = 'active'
		ORDER BY created_at
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Coordination{}
	for rows.Next() {
		c, err := scanCoordination(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// ListAllActiveCoordinations returns active coordinations across every
// tenant. Used by the cron sweeper which runs tenant-agnostic.
func ListAllActiveCoordinations(ctx context.Context, pool *pgxpool.Pool) ([]Coordination, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, organizer_id, kind, status, config, result,
		       deadline_at, shepherd_job_id, round_count, created_at, updated_at
		FROM app_coordinations
		WHERE status = 'active'
		ORDER BY tenant_id, created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Coordination{}
	for rows.Next() {
		c, err := scanCoordination(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// UpdateCoordinationStatus transitions a coordination's status and
// optionally writes its result jsonb. result may be nil.
func UpdateCoordinationStatus(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID, status string, result *CoordinationResult) error {
	var resultJSON any
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshaling result: %w", err)
		}
		resultJSON = b
	}
	_, err := pool.Exec(ctx, `
		UPDATE app_coordinations
		SET status = $3, result = $4, updated_at = now()
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, id, status, resultJSON)
	return err
}

// UpdateCoordinationConfig overwrites the jsonb config (used when slots
// are recomputed or auto_approve flips on).
func UpdateCoordinationConfig(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID, config CoordinationConfig) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	_, err = pool.Exec(ctx, `
		UPDATE app_coordinations SET config = $3, updated_at = now()
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, id, configJSON)
	return err
}

// CreateParticipant inserts a participant row.
func CreateParticipant(ctx context.Context, pool *pgxpool.Pool, p *Participant) error {
	rounds, _ := json.Marshal(p.Rounds)
	constraints, _ := json.Marshal(p.Constraints)
	row := pool.QueryRow(ctx, `
		INSERT INTO app_coordination_participants
		    (tenant_id, coordination_id, identifier, user_id, session_id, channel, status,
		     rounds, constraints, availability, accepted_time, nudge_count, next_nudge_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, created_at, updated_at
	`, p.TenantID, p.CoordinationID, p.Identifier, p.UserID, p.SessionID, p.Channel,
		p.Status, rounds, constraints, p.Availability, p.AcceptedTime, p.NudgeCount, p.NextNudgeAt)
	return row.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

// ListParticipants returns all participants for a coordination.
func ListParticipants(ctx context.Context, pool *pgxpool.Pool, tenantID, coordID uuid.UUID) ([]Participant, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, coordination_id, identifier, user_id, session_id, channel,
		       status, rounds, constraints, availability, accepted_time, nudge_count, next_nudge_at, created_at, updated_at
		FROM app_coordination_participants
		WHERE tenant_id = $1 AND coordination_id = $2
		ORDER BY created_at
	`, tenantID, coordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Participant{}
	for rows.Next() {
		p, err := scanParticipant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// GetParticipant loads a participant by id.
func GetParticipant(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Participant, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, tenant_id, coordination_id, identifier, user_id, session_id, channel,
		       status, rounds, constraints, availability, accepted_time, nudge_count, next_nudge_at, created_at, updated_at
		FROM app_coordination_participants
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, id)
	return scanParticipant(row)
}

// ListReadyParticipants finds participants whose next_nudge_at has
// elapsed, ordered for the cron sweep. Includes pending (initial
// outreach), contacted (nudges), and responded (re-engagements after
// recompute set next_nudge_at = now()).
func ListReadyParticipants(ctx context.Context, pool *pgxpool.Pool, now time.Time) ([]Participant, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, coordination_id, identifier, user_id, session_id, channel,
		       status, rounds, constraints, availability, accepted_time, nudge_count, next_nudge_at, created_at, updated_at
		FROM app_coordination_participants
		WHERE status IN ('pending','contacted','responded')
		  AND next_nudge_at IS NOT NULL
		  AND next_nudge_at <= $1
		ORDER BY tenant_id, coordination_id, next_nudge_at
	`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Participant{}
	for rows.Next() {
		p, err := scanParticipant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// UpdateParticipant writes back the mutable fields after a sweep tick
// or inbound dispatch.
func UpdateParticipant(ctx context.Context, pool *pgxpool.Pool, p *Participant) error {
	rounds, _ := json.Marshal(p.Rounds)
	constraints, _ := json.Marshal(p.Constraints)
	_, err := pool.Exec(ctx, `
		UPDATE app_coordination_participants
		SET status = $3, session_id = $4, rounds = $5, constraints = $6,
		    availability = $7, accepted_time = $8,
		    nudge_count = $9, next_nudge_at = $10, updated_at = now()
		WHERE tenant_id = $1 AND id = $2
	`, p.TenantID, p.ID, p.Status, p.SessionID, rounds, constraints,
		p.Availability, p.AcceptedTime, p.NudgeCount, p.NextNudgeAt)
	return err
}

// FindParticipantBySession is used by the reply handler: given a
// session id (resolved by Messenger), find the active participant row
// it belongs to. Returns nil if no active participant claims this session.
func FindParticipantBySession(ctx context.Context, pool *pgxpool.Pool, tenantID, sessionID uuid.UUID) (*Participant, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, tenant_id, coordination_id, identifier, user_id, session_id, channel,
		       status, rounds, constraints, availability, accepted_time, nudge_count, next_nudge_at, created_at, updated_at
		FROM app_coordination_participants
		WHERE tenant_id = $1 AND session_id = $2
		  AND status IN ('contacted','responded')
		ORDER BY updated_at DESC
		LIMIT 1
	`, tenantID, sessionID)
	p, err := scanParticipant(row)
	if err != nil {
		if isNoRows(err) {
			//nolint:nilnil // (nil, nil) is the intentional "no match" signal
			return nil, nil
		}
		return nil, err
	}
	return p, nil
}

// scannable lets scan helpers accept either pgx.Row or pgx.Rows.
type scannable interface {
	Scan(dest ...any) error
}

func scanCoordination(s scannable) (*Coordination, error) {
	var c Coordination
	var configJSON []byte
	var resultJSON sql.NullString
	if err := s.Scan(
		&c.ID, &c.TenantID, &c.OrganizerID, &c.Kind, &c.Status, &configJSON,
		&resultJSON, &c.DeadlineAt, &c.ShepherdJobID, &c.RoundCount, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(configJSON, &c.Config); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}
	if resultJSON.Valid && resultJSON.String != "" {
		var r CoordinationResult
		if err := json.Unmarshal([]byte(resultJSON.String), &r); err != nil {
			return nil, fmt.Errorf("unmarshaling result: %w", err)
		}
		c.Result = &r
	}
	return &c, nil
}

func scanParticipant(s scannable) (*Participant, error) {
	var p Participant
	var roundsJSON, constraintsJSON []byte
	if err := s.Scan(
		&p.ID, &p.TenantID, &p.CoordinationID, &p.Identifier, &p.UserID, &p.SessionID,
		&p.Channel, &p.Status, &roundsJSON, &constraintsJSON,
		&p.Availability, &p.AcceptedTime, &p.NudgeCount, &p.NextNudgeAt, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(roundsJSON, &p.Rounds); err != nil {
		return nil, fmt.Errorf("unmarshaling rounds: %w", err)
	}
	if err := json.Unmarshal(constraintsJSON, &p.Constraints); err != nil {
		return nil, fmt.Errorf("unmarshaling constraints: %w", err)
	}
	return &p, nil
}

func isNoRows(err error) bool {
	return err != nil && err.Error() == pgx.ErrNoRows.Error()
}
