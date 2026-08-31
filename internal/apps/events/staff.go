package events

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mrdon/kit/internal/apps/square"
	"github.com/mrdon/kit/internal/models"
)

// Staff mapping: which Kit user is which Square team member.
//
// Kit knows people by Slack id and Square knows them by payroll id, and
// nothing joins the two. Rather than guess, an admin sets the pairing once per
// person on the admin page. See migration 077 for why this is manual.

// rosterWindow is how far ahead the staff roster looks for people to offer in
// the mapping UI. Long enough that a new hire appears before their first
// shift, short enough that someone who left last season stops being offered.
const rosterWindow = 60 * 24 * time.Hour

// StaffMapping is one configured pairing.
type StaffMapping struct {
	SquareTeamMemberID string    `json:"square_team_member_id"`
	UserID             uuid.UUID `json:"user_id"`
	SlackUserID        string    `json:"slack_user_id"`
	DisplayName        string    `json:"display_name"`
}

// StaffMember is one person seen on the upcoming Square schedule.
//
// The roster is derived from published shifts rather than from Square's
// team-member list on purpose: it needs no API surface beyond the one the
// notifier already calls, and it offers only people actually being scheduled
// instead of every former employee Square still holds.
type StaffMember struct {
	TeamMemberID string `json:"team_member_id"`
	Name         string `json:"name"`
	// Shifts is how many published shifts this person has in the window,
	// which is what tells an admin who matters most to map first.
	Shifts int `json:"shifts"`
}

// listStaffMappings returns the configured pairings, resolved to the Kit
// user's Slack id and display name so callers can render them without a
// second lookup.
func listStaffMappings(ctx context.Context, a *App, tenantID uuid.UUID) ([]StaffMapping, error) {
	rows, err := a.pool.Query(ctx, `
		SELECT m.square_team_member_id, m.user_id, u.slack_user_id, COALESCE(u.display_name, '')
		FROM app_events_staff_map m
		JOIN users u ON u.id = m.user_id
		WHERE m.tenant_id = $1
		ORDER BY COALESCE(u.display_name, u.slack_user_id)`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing staff mappings: %w", err)
	}
	defer rows.Close()

	out := []StaffMapping{}
	for rows.Next() {
		var m StaffMapping
		if err := rows.Scan(&m.SquareTeamMemberID, &m.UserID, &m.SlackUserID, &m.DisplayName); err != nil {
			return nil, fmt.Errorf("scanning staff mapping: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// mappedUserIDs returns team member id -> Kit user id, the lookup the
// notifier needs.
func mappedUserIDs(ctx context.Context, a *App, tenantID uuid.UUID) (map[string]uuid.UUID, error) {
	mappings, err := listStaffMappings(ctx, a, tenantID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]uuid.UUID, len(mappings))
	for _, m := range mappings {
		out[m.SquareTeamMemberID] = m.UserID
	}
	return out, nil
}

// setStaffMapping pairs a Square team member with a Slack user, creating the
// Kit user row if this is someone who has never interacted with Kit — which is
// the normal case for taproom staff, who are in Slack but have no reason to
// have DM'd the bot.
//
// Re-pairing either side moves the mapping rather than failing: the unique
// constraints exist to stop double-delivery, not to make a correction require
// a delete first.
func setStaffMapping(ctx context.Context, a *App, tenantID uuid.UUID, teamMemberID, slackUserID string) (StaffMapping, error) {
	teamMemberID = strings.TrimSpace(teamMemberID)
	slackUserID = strings.TrimSpace(slackUserID)
	if teamMemberID == "" {
		return StaffMapping{}, errors.New("a Square team member is required")
	}
	if slackUserID == "" {
		return StaffMapping{}, errors.New("a Slack user is required")
	}

	user, err := models.EnsureUserBySlackID(ctx, a.pool, tenantID, slackUserID)
	if err != nil {
		return StaffMapping{}, fmt.Errorf("resolving slack user %s: %w", slackUserID, err)
	}

	// Clear any prior claim on this user before inserting, or the
	// (tenant_id, user_id) constraint rejects a re-pair that is really a
	// correction ("no, that shift belongs to Sean").
	if _, err := a.pool.Exec(ctx, `
		DELETE FROM app_events_staff_map
		WHERE tenant_id = $1 AND user_id = $2 AND square_team_member_id <> $3`,
		tenantID, user.ID, teamMemberID); err != nil {
		return StaffMapping{}, fmt.Errorf("clearing previous mapping: %w", err)
	}

	if _, err := a.pool.Exec(ctx, `
		INSERT INTO app_events_staff_map (tenant_id, square_team_member_id, user_id, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (tenant_id, square_team_member_id) DO UPDATE
			SET user_id = EXCLUDED.user_id, updated_at = now()`,
		tenantID, teamMemberID, user.ID); err != nil {
		return StaffMapping{}, fmt.Errorf("saving staff mapping: %w", err)
	}

	name := ""
	if user.DisplayName != nil {
		name = *user.DisplayName
	}
	return StaffMapping{
		SquareTeamMemberID: teamMemberID,
		UserID:             user.ID,
		SlackUserID:        user.SlackUserID,
		DisplayName:        name,
	}, nil
}

// clearStaffMapping removes a pairing. Unmapping is how you stop someone's
// notices without removing them from Square or Slack.
func clearStaffMapping(ctx context.Context, a *App, tenantID uuid.UUID, teamMemberID string) error {
	_, err := a.pool.Exec(ctx, `
		DELETE FROM app_events_staff_map
		WHERE tenant_id = $1 AND square_team_member_id = $2`,
		tenantID, strings.TrimSpace(teamMemberID))
	if err != nil {
		return fmt.Errorf("clearing staff mapping: %w", err)
	}
	return nil
}

// staffRoster returns the distinct people on the published schedule for the
// next rosterWindow, busiest first.
//
// Open shifts (no team member) are skipped: there is nobody to notify, and
// offering "(open shift)" in a dropdown of humans is noise.
func staffRoster(ctx context.Context, tenantID uuid.UUID) ([]StaffMember, error) {
	start := timeNow().UTC()
	shifts, err := square.Instance().ListPublishedShifts(ctx, tenantID, start, start.Add(rosterWindow))
	if err != nil {
		return nil, err
	}
	byID := map[string]*StaffMember{}
	for _, s := range shifts {
		if s.TeamMemberID == "" {
			continue
		}
		if m, ok := byID[s.TeamMemberID]; ok {
			m.Shifts++
			continue
		}
		byID[s.TeamMemberID] = &StaffMember{
			TeamMemberID: s.TeamMemberID,
			Name:         s.Member,
			Shifts:       1,
		}
	}
	out := make([]StaffMember, 0, len(byID))
	for _, m := range byID {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Shifts != out[j].Shifts {
			return out[i].Shifts > out[j].Shifts
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// recordNotice claims the day for a send, returning false when an identical
// notice has already been posted.
//
// The claim is the INSERT itself rather than a read-then-write, so two runs
// racing on the same day cannot both decide they are first. A changed hash
// updates the row and posts again: the day's plan genuinely differs from what
// the channel was told this morning.
func recordNotice(ctx context.Context, a *App, tenantID uuid.UUID, day time.Time, hash string) (bool, error) {
	var stored string
	err := a.pool.QueryRow(ctx, `
		INSERT INTO app_events_shift_notices (tenant_id, notice_date, content_hash)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, notice_date) DO UPDATE
			SET content_hash = EXCLUDED.content_hash, sent_at = now()
			WHERE app_events_shift_notices.content_hash <> EXCLUDED.content_hash
		RETURNING content_hash`,
		tenantID, day.Format("2006-01-02"), hash).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		// The WHERE on the DO UPDATE suppressed the write: same hash, already
		// posted, nothing new to say.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("recording shift notice: %w", err)
	}
	return true, nil
}

// stampNoticeMessage records the posted message's ts, which is also the thread
// anchor its detail reply hangs from.
func stampNoticeMessage(ctx context.Context, a *App, tenantID uuid.UUID, day time.Time, ts string) error {
	_, err := a.pool.Exec(ctx, `
		UPDATE app_events_shift_notices SET channel_message_id = $3
		WHERE tenant_id = $1 AND notice_date = $2`,
		tenantID, day.Format("2006-01-02"), ts)
	if err != nil {
		return fmt.Errorf("stamping shift notice: %w", err)
	}
	return nil
}
