package models

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// slackUserIDPattern matches a Slack user ID like U09AN7KJU3G or W12345.
var slackUserIDPattern = regexp.MustCompile(`^[UW][A-Z0-9]{2,}$`)

type User struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	SlackUserID string
	DisplayName *string
	Timezone    string
	CreatedAt   time.Time
}

// GetOrCreateUser finds a user by tenant + slack_user_id, creating if needed.
func GetOrCreateUser(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, slackUserID string, displayName string) (*User, error) {
	user := &User{}
	err := pool.QueryRow(ctx, `
		INSERT INTO users (id, tenant_id, slack_user_id, display_name)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, slack_user_id)
		DO UPDATE SET display_name = COALESCE(NULLIF(EXCLUDED.display_name, ''), users.display_name)
		RETURNING id, tenant_id, slack_user_id, display_name, timezone, created_at
	`, uuid.New(), tenantID, slackUserID, nilIfEmpty(displayName)).Scan(
		&user.ID, &user.TenantID, &user.SlackUserID, &user.DisplayName,
		&user.Timezone, &user.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get or create user: %w", err)
	}
	return user, nil
}

// GetUserBySlackID finds a user by tenant + slack_user_id.
func GetUserBySlackID(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, slackUserID string) (*User, error) {
	user := &User{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, slack_user_id, display_name, timezone, created_at
		FROM users WHERE tenant_id = $1 AND slack_user_id = $2
	`, tenantID, slackUserID).Scan(
		&user.ID, &user.TenantID, &user.SlackUserID, &user.DisplayName,
		&user.Timezone, &user.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting user: %w", err)
	}
	return user, nil
}

// GetUserByID finds a user by tenant + user ID.
func GetUserByID(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) (*User, error) {
	user := &User{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, slack_user_id, display_name, timezone, created_at
		FROM users WHERE tenant_id = $1 AND id = $2
	`, tenantID, userID).Scan(
		&user.ID, &user.TenantID, &user.SlackUserID, &user.DisplayName,
		&user.Timezone, &user.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting user by id: %w", err)
	}
	return user, nil
}

// SearchUsers finds users matching a query within a tenant.
// The query is matched case-insensitively against display_name and slack_user_id.
// An empty query returns all users.
func SearchUsers(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, query string) ([]User, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, slack_user_id, display_name, timezone, created_at
		FROM users
		WHERE tenant_id = $1
		  AND ($2 = ''
		       OR display_name ILIKE '%' || $2 || '%'
		       OR slack_user_id ILIKE '%' || $2 || '%')
		ORDER BY display_name NULLS LAST, slack_user_id
	`, tenantID, query)
	if err != nil {
		return nil, fmt.Errorf("searching users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.SlackUserID, &u.DisplayName,
			&u.Timezone, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// ErrAmbiguousUser is returned when a user reference matches multiple users.
type ErrAmbiguousUser struct {
	Ref     string
	Matches []User
}

func (e *ErrAmbiguousUser) Error() string {
	return fmt.Sprintf("user reference %q is ambiguous (%d matches)", e.Ref, len(e.Matches))
}

// ResolveUserRef accepts a user reference in any of the forms kit understands and
// returns the canonical User. Accepted forms:
//   - kit user UUID
//   - Slack user ID (e.g. U09AN7KJU3G)
//   - case-insensitive substring match against display_name (must be unique)
//
// Returns (nil, nil) when no user matches. Returns *ErrAmbiguousUser when a
// name fragment matches more than one user.
func ResolveUserRef(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, ref string) (*User, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil //nolint:nilnil // empty ref is "no user"
	}

	// UUID
	if id, err := uuid.Parse(ref); err == nil {
		return GetUserByID(ctx, pool, tenantID, id)
	}

	// Slack user ID: U or W followed by alphanumerics. Slack IDs are uppercase.
	if slackUserIDPattern.MatchString(ref) {
		return GetUserBySlackID(ctx, pool, tenantID, ref)
	}

	// Otherwise treat as a display-name search.
	matches, err := SearchUsers(ctx, pool, tenantID, ref)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, nil //nolint:nilnil // no match is not an error
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	return nil, &ErrAmbiguousUser{Ref: ref, Matches: matches}
}

// ListUsersByTenant returns all users for a tenant.
func ListUsersByTenant(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]User, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, slack_user_id, display_name, timezone, created_at
		FROM users WHERE tenant_id = $1
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.SlackUserID, &u.DisplayName,
			&u.Timezone, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning user: %w", err)
		}
		users = append(users, u)
	}
	return users, nil
}

// UpdateUserProfile updates a user's display name and timezone.
func UpdateUserProfile(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, displayName, timezone string) error {
	_, err := pool.Exec(ctx, `
		UPDATE users SET display_name = $3, timezone = $4 WHERE tenant_id = $1 AND id = $2
	`, tenantID, userID, nilIfEmpty(displayName), timezone)
	if err != nil {
		return fmt.Errorf("updating user profile: %w", err)
	}
	return nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
