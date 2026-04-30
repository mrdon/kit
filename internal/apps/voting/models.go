// Package voting implements standalone proposal votes. The organizer
// poses a yes/no/abstain question to a named participant list; each
// participant gets a decision card in their swipe feed. On all-resolved
// or deadline, the engine surfaces a digest decision card to the
// organizer who picks accept / reject (and optionally broadcasts a
// briefing card to participants).
//
// Unlike coordination, voting has no Slack DM outreach, no nudge
// schedule, and no LLM message drafting/parsing — the participant
// surface is the card itself.
package voting

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Status values for app_votes.status.
const (
	StatusActive    = "active"
	StatusConfirmed = "confirmed"
	StatusAbandoned = "abandoned"
	StatusCancelled = "cancelled"
)

// VoteVerdict is the participant's choice on their decision card.
type VoteVerdict string

const (
	VerdictApprove VoteVerdict = "approve"
	VerdictObject  VoteVerdict = "object"
	VerdictAbstain VoteVerdict = "abstain"
)

// Vote is one workflow instance.
type Vote struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	OrganizerID  uuid.UUID
	Title        string
	ProposalText string
	ContextNotes string
	Status       string
	DeadlineAt   time.Time
	Outcome      *Outcome
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Outcome is populated when the engine surfaces the digest card and
// extended when the organizer resolves it.
type Outcome struct {
	Tally  Tally  `json:"tally"`
	Action string `json:"action,omitempty"` // accept | accept_and_share | reject | reject_and_announce
}

// Tally counts each verdict bucket plus the "no response" bucket for
// participants who never resolved their card. Flat struct (rather
// than map[VoteVerdict]int) so the JSON shape is stable and the
// templates can dereference fields directly.
type Tally struct {
	Approve    int `json:"approve"`
	Object     int `json:"object"`
	Abstain    int `json:"abstain"`
	NoResponse int `json:"no_response"`
}

// Participant is per-counterparty state.
type Participant struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	VoteID      uuid.UUID
	Identifier  string // slack user id
	UserID      *uuid.UUID
	VoteCardID  *uuid.UUID
	Verdict     VoteVerdict // empty until resolved
	Reason      string
	RespondedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateVote inserts a new vote row.
func CreateVote(ctx context.Context, pool *pgxpool.Pool, v *Vote) error {
	row := pool.QueryRow(ctx, `
		INSERT INTO app_votes
		    (tenant_id, organizer_id, title, proposal_text, context_notes, status, deadline_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at
	`, v.TenantID, v.OrganizerID, v.Title, v.ProposalText, v.ContextNotes, v.Status, v.DeadlineAt)
	return row.Scan(&v.ID, &v.CreatedAt, &v.UpdatedAt)
}

// GetVote loads a vote by id within the tenant.
func GetVote(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Vote, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, tenant_id, organizer_id, title, proposal_text, context_notes,
		       status, deadline_at, outcome, created_at, updated_at
		FROM app_votes
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, id)
	return scanVote(row)
}

// ListActiveVotes returns all active votes across all tenants. Used by
// the cron sweeper.
func ListActiveVotes(ctx context.Context, pool *pgxpool.Pool) ([]Vote, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, organizer_id, title, proposal_text, context_notes,
		       status, deadline_at, outcome, created_at, updated_at
		FROM app_votes
		WHERE status = 'active'
		ORDER BY tenant_id, created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Vote{}
	for rows.Next() {
		v, err := scanVote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// listVotesForCaller returns recent votes the caller organized.
func listVotesForCaller(ctx context.Context, pool *pgxpool.Pool, tenantID, organizerID uuid.UUID, limit int) ([]Vote, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, organizer_id, title, proposal_text, context_notes,
		       status, deadline_at, outcome, created_at, updated_at
		FROM app_votes
		WHERE tenant_id = $1 AND organizer_id = $2
		ORDER BY created_at DESC
		LIMIT $3
	`, tenantID, organizerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Vote{}
	for rows.Next() {
		v, err := scanVote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// UpdateVoteStatus transitions a vote's status and optionally writes
// its outcome jsonb. outcome may be nil.
func UpdateVoteStatus(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID, status string, outcome *Outcome) error {
	var outcomeJSON any
	if outcome != nil {
		b, err := json.Marshal(outcome)
		if err != nil {
			return fmt.Errorf("marshaling outcome: %w", err)
		}
		outcomeJSON = b
	}
	_, err := pool.Exec(ctx, `
		UPDATE app_votes
		SET status = $3, outcome = $4, updated_at = now()
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, id, status, outcomeJSON)
	return err
}

// CreateParticipant inserts a participant row.
func CreateParticipant(ctx context.Context, pool *pgxpool.Pool, p *Participant) error {
	row := pool.QueryRow(ctx, `
		INSERT INTO app_vote_participants
		    (tenant_id, vote_id, identifier, user_id, vote_card_id, verdict, reason, responded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at
	`, p.TenantID, p.VoteID, p.Identifier, p.UserID, p.VoteCardID,
		nilStringIfEmpty(string(p.Verdict)), p.Reason, p.RespondedAt)
	return row.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

// GetParticipant loads a participant by id.
func GetParticipant(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Participant, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, tenant_id, vote_id, identifier, user_id, vote_card_id,
		       verdict, reason, responded_at, created_at, updated_at
		FROM app_vote_participants
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, id)
	return scanParticipant(row)
}

// ListParticipants returns all participants for a vote.
func ListParticipants(ctx context.Context, pool *pgxpool.Pool, tenantID, voteID uuid.UUID) ([]Participant, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, vote_id, identifier, user_id, vote_card_id,
		       verdict, reason, responded_at, created_at, updated_at
		FROM app_vote_participants
		WHERE tenant_id = $1 AND vote_id = $2
		ORDER BY created_at
	`, tenantID, voteID)
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

// UpdateParticipantCardID stores the per-participant decision card id
// after surfacing it.
func UpdateParticipantCardID(ctx context.Context, pool *pgxpool.Pool, tenantID, participantID uuid.UUID, cardID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE app_vote_participants
		SET vote_card_id = $3, updated_at = now()
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, participantID, cardID)
	return err
}

// UpdateParticipantVerdict records a participant's resolution. Returns
// (true, nil) on a successful first record, (false, nil) if the
// participant had already resolved (the WHERE verdict IS NULL guard
// is the atomic idempotency point — closes the TOCTOU window where
// two concurrent resolve paths both read empty verdict and both
// update). Errors only on real DB failures.
func UpdateParticipantVerdict(ctx context.Context, pool *pgxpool.Pool, tenantID, participantID uuid.UUID, verdict VoteVerdict, reason string, respondedAt time.Time) (bool, error) {
	res, err := pool.Exec(ctx, `
		UPDATE app_vote_participants
		SET verdict = $3, reason = $4, responded_at = $5, updated_at = now()
		WHERE tenant_id = $1 AND id = $2 AND verdict IS NULL
	`, tenantID, participantID, string(verdict), reason, respondedAt)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() > 0, nil
}

func scanVote(s scannable) (*Vote, error) {
	var v Vote
	var outcome sql.NullString
	if err := s.Scan(
		&v.ID, &v.TenantID, &v.OrganizerID, &v.Title, &v.ProposalText, &v.ContextNotes,
		&v.Status, &v.DeadlineAt, &outcome, &v.CreatedAt, &v.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if outcome.Valid && outcome.String != "" {
		var o Outcome
		if err := json.Unmarshal([]byte(outcome.String), &o); err != nil {
			return nil, fmt.Errorf("unmarshaling outcome: %w", err)
		}
		v.Outcome = &o
	}
	return &v, nil
}

func scanParticipant(s scannable) (*Participant, error) {
	var p Participant
	var verdict sql.NullString
	if err := s.Scan(
		&p.ID, &p.TenantID, &p.VoteID, &p.Identifier, &p.UserID, &p.VoteCardID,
		&verdict, &p.Reason, &p.RespondedAt, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if verdict.Valid {
		p.Verdict = VoteVerdict(verdict.String)
	}
	return &p, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func nilStringIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
