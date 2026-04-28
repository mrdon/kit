package coordination

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
	kitslack "github.com/mrdon/kit/internal/slack"
)

// ensureParticipantUser resolves a Slack user ID to a Kit user record.
// If the user isn't in our DB yet (common for participants who've never
// DM'd Kit themselves), fetch their profile from Slack and upsert.
// Returns nil on hard failure (don't block coordination start; the
// participant just won't have a display name).
func ensureParticipantUser(ctx context.Context, pool *pgxpool.Pool, app *CoordinationApp, tenantID uuid.UUID, slackID string) *models.User {
	if u, err := models.GetUserBySlackID(ctx, pool, tenantID, slackID); err == nil && u != nil {
		return u
	}
	// Need the tenant's bot token to call Slack. Messenger holds the
	// encryptor; reuse its tenant→client resolution path.
	if app == nil || app.msg == nil || app.msg.Encryptor == nil {
		slog.Warn("ensureParticipantUser: messenger/encryptor not configured", "slack_id", slackID)
		return nil
	}
	tenant, err := models.GetTenantByID(ctx, pool, tenantID)
	if err != nil || tenant == nil {
		return nil
	}
	botToken, err := app.msg.Encryptor.Decrypt(tenant.BotToken)
	if err != nil {
		slog.Warn("decrypting bot token for profile fetch", "error", err)
		return nil
	}
	slack := kitslack.NewClient(botToken)
	displayName := ""
	if info, err := slack.GetUserInfo(ctx, slackID); err == nil {
		displayName = info.DisplayName
	} else {
		slog.Warn("slack GetUserInfo failed for participant", "slack_id", slackID, "error", err)
	}
	u, err := models.GetOrCreateUser(ctx, pool, tenantID, slackID, displayName)
	if err != nil {
		slog.Warn("upserting kit user for participant", "slack_id", slackID, "error", err)
		return nil
	}
	return u
}

// Service is the public entry point for coordination. Tools (agent and
// MCP) call into Service; the cron sweeper drives Engine.
type Service struct {
	pool *pgxpool.Pool
	app  *CoordinationApp
}

func newService(pool *pgxpool.Pool, app *CoordinationApp) *Service {
	return &Service{pool: pool, app: app}
}

// StartInput is the API for creating a meeting coordination.
type StartInput struct {
	Title           string
	DurationMinutes int
	StartDate       time.Time // earliest acceptable meeting day
	EndDate         time.Time // latest
	CandidateSlots  []Slot    // pre-computed by the agent (from organizer iCal or organizer-typed windows)
	Participants    []string  // slack user ids
	Notes           string    // hints like "mornings preferred"
	AutoApprove     bool
	DeadlineDays    int // nil/0 → defaults to 7
	OrganizerTZ     string
}

// Start creates a coordination, its participants, and arms the first
// sweep tick (sets next_nudge_at = now() on each participant so the
// engine picks them up on the next cron run).
func (s *Service) Start(ctx context.Context, c *services.Caller, in StartInput) (*Coordination, error) {
	if c == nil {
		return nil, errors.New("caller required")
	}
	if in.Title == "" {
		return nil, errors.New("title required")
	}
	if in.DurationMinutes <= 0 {
		return nil, errors.New("duration_minutes required")
	}
	if len(in.CandidateSlots) == 0 {
		return nil, errors.New("candidate_slots required (the agent must pre-compute these from the organizer's calendar or stated availability)")
	}
	if len(in.Participants) < 1 {
		return nil, errors.New("at least one participant required")
	}

	deadlineDays := in.DeadlineDays
	if deadlineDays <= 0 {
		deadlineDays = 7
	}
	deadline := time.Now().Add(time.Duration(deadlineDays) * 24 * time.Hour)

	// AutoApprove defaults to false — outbound DMs to participants are
	// the identity-sensitive operation and the organizer should approve
	// at least the first wave. They can flip auto_approve via the
	// "Send + auto-approve future" option on the approval card.
	coord := &Coordination{
		TenantID:    c.TenantID,
		OrganizerID: c.UserID,
		Kind:        KindMeeting,
		Status:      StatusActive,
		DeadlineAt:  &deadline,
		Config: CoordinationConfig{
			Title:           in.Title,
			DurationMinutes: in.DurationMinutes,
			StartDate:       in.StartDate,
			EndDate:         in.EndDate,
			CandidateSlots:  in.CandidateSlots,
			AutoApprove:     in.AutoApprove,
			Notes:           in.Notes,
			OrganizerTZ:     in.OrganizerTZ,
		},
	}

	if err := CreateCoordination(ctx, s.pool, coord); err != nil {
		return nil, fmt.Errorf("creating coordination: %w", err)
	}

	// Resolve organizer's Slack ID once so we can drop them from the
	// participants list (the organizer doesn't need a DM asking when
	// they're free — they initiated the coordination).
	organizerSlackID := ""
	if u, err := models.GetUserByID(ctx, s.pool, c.TenantID, c.UserID); err == nil && u != nil {
		organizerSlackID = u.SlackUserID
	}

	now := time.Now()
	created := 0
	for _, slackID := range in.Participants {
		if slackID == organizerSlackID {
			// Don't DM the organizer about their own meeting.
			continue
		}
		p := &Participant{
			TenantID:       c.TenantID,
			CoordinationID: coord.ID,
			Identifier:     slackID,
			Channel:        "slack",
			Status:         ParticipantPending,
			NextNudgeAt:    &now,
			Constraints:    Constraints{SlotVerdicts: map[string]SlotVerdict{}},
			Rounds:         []Round{},
		}
		// Ensure the Kit user record exists (fetch from Slack and upsert
		// if we've never seen them). Without this, downstream session
		// creation fails the user_id FK, and we can't render display
		// names on cards either.
		if u := ensureParticipantUser(ctx, s.pool, s.app, c.TenantID, slackID); u != nil {
			p.UserID = &u.ID
		}
		if err := CreateParticipant(ctx, s.pool, p); err != nil {
			return nil, fmt.Errorf("creating participant %s: %w", slackID, err)
		}
		created++
	}
	if created < 1 {
		// All passed-in participants matched the organizer. We need at
		// least one external person to DM. Roll back to keep state clean.
		_, _ = s.pool.Exec(ctx, "DELETE FROM app_coordinations WHERE id = $1", coord.ID)
		return nil, errors.New("at least one participant besides the organizer is required")
	}

	return coord, nil
}

// Cancel marks the coordination cancelled and the engine sends a
// brief cancellation note to contacted participants (next sweep).
func (s *Service) Cancel(ctx context.Context, c *services.Caller, coordID uuid.UUID) error {
	coord, err := GetCoordination(ctx, s.pool, c.TenantID, coordID)
	if err != nil {
		return fmt.Errorf("loading coordination: %w", err)
	}
	if coord == nil {
		return errors.New("coordination not found")
	}
	if coord.OrganizerID != c.UserID {
		return errors.New("only the organizer can cancel this coordination")
	}
	if coord.Status != StatusActive && coord.Status != StatusConverged {
		return fmt.Errorf("cannot cancel coordination in status %q", coord.Status)
	}

	if err := UpdateCoordinationStatus(ctx, s.pool, c.TenantID, coord.ID, StatusCancelled, nil); err != nil {
		return fmt.Errorf("updating status: %w", err)
	}

	// Best-effort: send a cancellation note to each contacted/responded
	// participant. The engine's send pipeline handles this via the
	// outbound message log; for Phase 1 we send synchronously here.
	if s.app != nil && s.app.engine != nil {
		_ = s.app.engine.NotifyCancel(ctx, coord)
	}
	return nil
}

// GetStatus returns the current state of a coordination, including
// per-participant status, current candidate set, and next scheduled
// nudges.
type Status struct {
	Coordination *Coordination
	Participants []Participant
}

func (s *Service) GetStatus(ctx context.Context, c *services.Caller, coordID uuid.UUID) (*Status, error) {
	coord, err := GetCoordination(ctx, s.pool, c.TenantID, coordID)
	if err != nil {
		return nil, fmt.Errorf("loading coordination: %w", err)
	}
	if coord == nil {
		return nil, errors.New("coordination not found")
	}
	parts, err := ListParticipants(ctx, s.pool, c.TenantID, coord.ID)
	if err != nil {
		return nil, fmt.Errorf("listing participants: %w", err)
	}
	return &Status{Coordination: coord, Participants: parts}, nil
}

// ListForCaller returns recent coordinations the caller organized.
// Used by list_coordinations.
func (s *Service) ListForCaller(ctx context.Context, c *services.Caller, limit int) ([]Coordination, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, organizer_id, kind, status, config, result,
		       deadline_at, shepherd_task_id, created_at, updated_at
		FROM app_coordinations
		WHERE tenant_id = $1 AND organizer_id = $2
		ORDER BY created_at DESC
		LIMIT $3
	`, c.TenantID, c.UserID, limit)
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
