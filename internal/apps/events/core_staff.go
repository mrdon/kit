package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// Staff-mapping and shift-notice tool cores, shared by the agent and MCP
// surfaces. The web page is the primary way to set a mapping (two name
// dropdowns beat typing ids), so these exist mainly to inspect the mapping and
// to preview a send from chat or a harness.

func coreStaffMap(ctx context.Context, caller *services.Caller) (string, error) {
	app := Instance()
	if app == nil {
		return "", errors.New("events app not available")
	}
	mappings, err := listStaffMappings(ctx, app, caller.TenantID)
	if err != nil {
		return "", err
	}
	roster, rosterErr := staffRoster(ctx, caller.TenantID)
	return FormatStaffMap(mappings, roster, rosterErr), nil
}

// staffMapInput is the shared input shape for events_map_staff.
type staffMapInput struct {
	SquareTeamMember string `json:"square_team_member"`
	SlackUser        string `json:"slack_user"`
}

func coreMapStaff(ctx context.Context, caller *services.Caller, raw json.RawMessage) (string, error) {
	app := Instance()
	if app == nil {
		return "", errors.New("events app not available")
	}
	var in staffMapInput
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
	}
	if strings.TrimSpace(in.SquareTeamMember) == "" {
		return "", errors.New("square_team_member is required")
	}

	roster, err := staffRoster(ctx, caller.TenantID)
	if err != nil {
		return "", fmt.Errorf("reading the Square schedule: %w", err)
	}
	member, err := resolveTeamMember(roster, in.SquareTeamMember)
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(in.SlackUser) == "" {
		if err := clearStaffMapping(ctx, app, caller.TenantID, member.TeamMemberID); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s is no longer mapped, so they will stop receiving shift notices.", member.Name), nil
	}

	slackID, err := app.resolveSlackUser(ctx, caller.TenantID, in.SlackUser)
	if err != nil {
		return "", err
	}
	m, err := setStaffMapping(ctx, app, caller.TenantID, member.TeamMemberID, slackID)
	if err != nil {
		return "", err
	}
	name := m.DisplayName
	if name == "" {
		name = m.SlackUserID
	}
	return fmt.Sprintf("%s (Square) now maps to %s (Slack). They will get a DM on the mornings they work when something is on.", member.Name, name), nil
}

// noticeInput is the shared input shape for events_shift_notices.
type noticeInput struct {
	Send bool `json:"send"`
}

func coreShiftNotices(ctx context.Context, caller *services.Caller, raw json.RawMessage) (string, error) {
	app := Instance()
	if app == nil {
		return "", errors.New("events app not available")
	}
	var in noticeInput
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
	}
	if !in.Send {
		plans, err := app.PreviewShiftNotices(ctx, caller.TenantID)
		if err != nil {
			return "", err
		}
		return FormatNoticePreview(plans), nil
	}
	sum, err := app.RunShiftNotices(ctx, caller.TenantID, "manual")
	if err != nil {
		return "", err
	}
	return "Shift notices: " + sum.String() + ".", nil
}

// resolveTeamMember accepts a Square team member id or a name, so a caller who
// read a name out of the schedule does not have to go hunting for the id.
//
// An ambiguous name is an error rather than a guess: picking wrong here sends
// one person's shift brief -- private bookings included -- to another.
func resolveTeamMember(roster []StaffMember, ref string) (StaffMember, error) {
	ref = strings.TrimSpace(ref)
	lower := strings.ToLower(ref)
	var partial []StaffMember
	for _, m := range roster {
		if m.TeamMemberID == ref || strings.EqualFold(m.Name, ref) {
			return m, nil
		}
		if strings.Contains(strings.ToLower(m.Name), lower) {
			partial = append(partial, m)
		}
	}
	switch len(partial) {
	case 1:
		return partial[0], nil
	case 0:
		return StaffMember{}, fmt.Errorf("nobody named %q is on the published Square schedule for the next two months", ref)
	default:
		names := make([]string, 0, len(partial))
		for _, m := range partial {
			names = append(names, m.Name)
		}
		return StaffMember{}, fmt.Errorf("%q matches several people (%s) — be more specific", ref, strings.Join(names, ", "))
	}
}

// resolveSlackUser accepts a Slack user id or a display name. Names are
// resolved against the Slack workspace rather than Kit's user table, because
// staff who have never messaged Kit have no row there yet.
func (a *App) resolveSlackUser(ctx context.Context, tenantID uuid.UUID, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if isSlackUserID(ref) {
		return ref, nil
	}
	options, listErr := a.listSlackOptions(ctx, tenantID)
	if listErr != "" {
		return "", errors.New(strings.ToLower(listErr[:1]) + listErr[1:])
	}
	lower := strings.ToLower(ref)
	var partial []slackOption
	for _, o := range options {
		if strings.EqualFold(o.Name, ref) {
			return o.SlackUserID, nil
		}
		if strings.Contains(strings.ToLower(o.Name), lower) {
			partial = append(partial, o)
		}
	}
	switch len(partial) {
	case 1:
		return partial[0].SlackUserID, nil
	case 0:
		return "", fmt.Errorf("no Slack member matches %q", ref)
	default:
		names := make([]string, 0, len(partial))
		for _, o := range partial {
			names = append(names, o.Name)
		}
		return "", fmt.Errorf("%q matches several Slack members (%s) — be more specific, or pass the U… id", ref, strings.Join(names, ", "))
	}
}

// isSlackUserID reports whether ref already looks like a Slack id, so it can
// be used as-is rather than matched against display names.
func isSlackUserID(ref string) bool {
	if len(ref) < 3 {
		return false
	}
	if ref[0] != 'U' && ref[0] != 'W' {
		return false
	}
	return strings.ToUpper(ref) == ref
}

// FormatStaffMap renders the mapping for the agent and MCP surfaces. Unmapped
// people are listed explicitly and last: they are the actionable part, and a
// list that only shows what IS configured hides the person who is silently
// getting nothing.
func FormatStaffMap(mappings []StaffMapping, roster []StaffMember, rosterErr error) string {
	byMember := make(map[string]StaffMapping, len(mappings))
	for _, m := range mappings {
		byMember[m.SquareTeamMemberID] = m
	}

	var b strings.Builder
	if rosterErr != nil {
		b.WriteString("Could not read the Square schedule, so only the stored mappings are shown.\n\n")
	}

	mapped, unmapped := []string{}, []string{}
	for _, m := range roster {
		if pair, ok := byMember[m.TeamMemberID]; ok {
			name := pair.DisplayName
			if name == "" {
				name = pair.SlackUserID
			}
			mapped = append(mapped, fmt.Sprintf("  %s → %s", m.Name, name))
			delete(byMember, m.TeamMemberID)
			continue
		}
		unmapped = append(unmapped, fmt.Sprintf("  %s (%s)", m.Name, plural(m.Shifts, "shift", "shifts")))
	}
	// Anything left over is mapped but no longer on the schedule — someone who
	// has left, or a rota that has not been published yet. Worth showing so a
	// stale pairing can be cleaned up.
	stale := []string{}
	for _, m := range byMember {
		name := m.DisplayName
		if name == "" {
			name = m.SlackUserID
		}
		stale = append(stale, fmt.Sprintf("  %s (not on the upcoming schedule)", name))
	}
	sort.Strings(stale)

	if len(mapped) > 0 {
		b.WriteString("Mapped:\n" + strings.Join(mapped, "\n") + "\n")
	}
	if len(unmapped) > 0 {
		b.WriteString("\nNOT mapped — these people work but get no shift notices:\n" +
			strings.Join(unmapped, "\n") + "\n")
	}
	if len(stale) > 0 {
		b.WriteString("\nMapped but not scheduled:\n" + strings.Join(stale, "\n") + "\n")
	}
	if b.Len() == 0 {
		return "Nobody is on the published Square schedule, so there is nobody to map yet."
	}
	return b.String()
}
