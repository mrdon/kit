package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// UserTools defines the shared tool metadata for user lookup operations.
var UserTools = []ToolMeta{
	{
		Name: "find_user",
		Description: "Look up a kit user by display name (e.g. 'matt'), Slack user ID (e.g. 'U09AN7KJU3G'), or kit UUID. " +
			"Use this to resolve a human-friendly reference to a kit user UUID before passing it to tools that take 'assigned_to' or similar fields. " +
			"Returns matching users with their kit UUID, Slack ID, display name, and admin flag. " +
			"A name fragment that matches multiple users returns all candidates so you can disambiguate.",
		Schema: propsReq(map[string]any{
			"query": field("string", "Display name (or fragment), Slack user ID, or kit UUID."),
		}, "query"),
	},
}

// UserService handles user lookups.
type UserService struct {
	pool *pgxpool.Pool
}

// Find searches users by name fragment, Slack ID, or UUID. Returns all matches.
func (s *UserService) Find(ctx context.Context, c *Caller, query string) ([]models.User, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query is required")
	}

	// Try direct resolution first (UUID or exact Slack ID)
	if id, err := uuid.Parse(query); err == nil {
		u, err := models.GetUserByID(ctx, s.pool, c.TenantID, id)
		if err != nil {
			return nil, err
		}
		if u != nil {
			return []models.User{*u}, nil
		}
		return nil, nil
	}

	return models.SearchUsers(ctx, s.pool, c.TenantID, query)
}

// Resolve takes a user reference (UUID, Slack ID, or display-name fragment) and
// returns the unique matching user. Returns ErrNotFound when nothing matches and
// surfaces models.ErrAmbiguousUser when a name fragment is not unique.
func (s *UserService) Resolve(ctx context.Context, c *Caller, ref string) (*models.User, error) {
	u, err := models.ResolveUserRef(ctx, s.pool, c.TenantID, ref)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrNotFound
	}
	return u, nil
}

// FormatUserRefError turns a resolver error into a user-facing message that
// names candidates when the reference is ambiguous.
func FormatUserRefError(ref string, err error) string {
	var ambig *models.ErrAmbiguousUser
	if errors.As(err, &ambig) {
		var b strings.Builder
		fmt.Fprintf(&b, "Multiple users match %q. Use a kit UUID or be more specific:\n", ref)
		for _, u := range ambig.Matches {
			b.WriteString("  - " + FormatUserLine(&u) + "\n")
		}
		return b.String()
	}
	if errors.Is(err, ErrNotFound) {
		return fmt.Sprintf("No user found matching %q. Try find_user to list candidates.", ref)
	}
	return err.Error()
}

// FormatUserLine renders a single user as "Name (slack_id, uuid)".
func FormatUserLine(u *models.User) string {
	name := "(no display name)"
	if u.DisplayName != nil && *u.DisplayName != "" {
		name = *u.DisplayName
	}
	return fmt.Sprintf("%s — slack:%s uuid:%s", name, u.SlackUserID, u.ID)
}
