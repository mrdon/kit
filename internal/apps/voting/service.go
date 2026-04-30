package voting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// Service is the public entry point for voting. Tools (agent and MCP)
// call into Service; the cron sweeper drives Engine.
type Service struct {
	pool *pgxpool.Pool
	app  *VotingApp
}

func newService(pool *pgxpool.Pool, app *VotingApp) *Service {
	return &Service{pool: pool, app: app}
}

// StartVoteInput is the API for starting a proposal vote.
type StartVoteInput struct {
	Title         string
	ProposalText  string
	ContextNotes  string
	Participants  []string // slack user ids
	DeadlineHours int      // 0 → defaults to 48
}

// StartVote creates a vote, resolves participants, and surfaces a
// per-participant decision card immediately so they don't wait for the
// next cron tick.
func (s *Service) StartVote(ctx context.Context, c *services.Caller, in StartVoteInput) (*Vote, error) {
	if c == nil {
		return nil, errors.New("caller required")
	}
	if in.Title == "" {
		return nil, errors.New("title required")
	}
	if in.ProposalText == "" {
		return nil, errors.New("proposal_text required")
	}
	if len(in.Participants) < 1 {
		return nil, errors.New("at least one participant required")
	}

	deadlineHours := in.DeadlineHours
	if deadlineHours <= 0 {
		deadlineHours = 48
	}
	deadline := time.Now().Add(time.Duration(deadlineHours) * time.Hour)

	// Filter the organizer out of the participant list — they're the
	// asker, not a voter. No on-the-fly Slack profile fetching: voting
	// doesn't need display names at create time, and skeleton user rows
	// fill in next time the participant DMs Kit.
	organizerSlackID := ""
	if u, err := models.GetUserByID(ctx, s.pool, c.TenantID, c.UserID); err == nil && u != nil {
		organizerSlackID = u.SlackUserID
	}

	v := &Vote{
		TenantID:     c.TenantID,
		OrganizerID:  c.UserID,
		Title:        in.Title,
		ProposalText: in.ProposalText,
		ContextNotes: in.ContextNotes,
		Status:       StatusActive,
		DeadlineAt:   deadline,
	}
	if err := CreateVote(ctx, s.pool, v); err != nil {
		return nil, fmt.Errorf("creating vote: %w", err)
	}

	// Best-effort cleanup: if anything in the participant fan-out
	// fails, drop the orphan vote row. The CreateVote above is its
	// own implicit tx; wrapping the whole thing in a single tx would
	// be cleaner but ensureKitUser+CreateParticipant share `pool`
	// (not a tx) so this is the smaller-blast-radius fix.
	cleanup := func() { _, _ = s.pool.Exec(ctx, "DELETE FROM app_votes WHERE id = $1", v.ID) }

	created := 0
	for _, slackID := range in.Participants {
		if slackID == "" || slackID == organizerSlackID {
			continue
		}
		p := &Participant{
			TenantID:   c.TenantID,
			VoteID:     v.ID,
			Identifier: slackID,
		}
		if u, err := models.EnsureUserBySlackID(ctx, s.pool, c.TenantID, slackID); err == nil && u != nil {
			p.UserID = &u.ID
		}
		if err := CreateParticipant(ctx, s.pool, p); err != nil {
			cleanup()
			return nil, fmt.Errorf("creating participant %s: %w", slackID, err)
		}
		created++
	}
	if created < 1 {
		cleanup()
		return nil, errors.New("at least one participant besides the organizer is required")
	}

	if s.app != nil {
		if err := s.app.surfaceParticipantVoteCards(ctx, v); err != nil {
			slog.Error("surfacing initial vote cards", "error", err, "vote", v.ID)
		}
	}

	return v, nil
}

// Cancel marks an active vote cancelled. Outstanding participant
// cards are left in their feed — the organizer just stops getting
// the digest. The cards subsystem doesn't expose a "dismiss this
// card from someone else's feed" API today; if/when it does, this
// path should call it for each participant.
func (s *Service) Cancel(ctx context.Context, c *services.Caller, voteID uuid.UUID) error {
	v, err := GetVote(ctx, s.pool, c.TenantID, voteID)
	if err != nil {
		return fmt.Errorf("loading vote: %w", err)
	}
	if v == nil {
		return errors.New("vote not found")
	}
	if v.OrganizerID != c.UserID {
		return errors.New("only the organizer can cancel this vote")
	}
	if v.Status != StatusActive {
		return fmt.Errorf("cannot cancel vote in status %q", v.Status)
	}
	return UpdateVoteStatus(ctx, s.pool, c.TenantID, v.ID, StatusCancelled, nil)
}

// Status bundles a vote with its participants for the get_vote tool.
type Status struct {
	Vote         *Vote
	Participants []Participant
}

// GetStatus returns the current state of a vote.
func (s *Service) GetStatus(ctx context.Context, c *services.Caller, voteID uuid.UUID) (*Status, error) {
	v, err := GetVote(ctx, s.pool, c.TenantID, voteID)
	if err != nil {
		return nil, fmt.Errorf("loading vote: %w", err)
	}
	if v == nil {
		return nil, errors.New("vote not found")
	}
	parts, err := ListParticipants(ctx, s.pool, c.TenantID, v.ID)
	if err != nil {
		return nil, fmt.Errorf("listing participants: %w", err)
	}
	return &Status{Vote: v, Participants: parts}, nil
}

// ListForCaller returns recent votes the caller organized.
func (s *Service) ListForCaller(ctx context.Context, c *services.Caller, limit int) ([]Vote, error) {
	return listVotesForCaller(ctx, s.pool, c.TenantID, c.UserID, limit)
}
