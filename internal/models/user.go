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

// UserEnricher resolves missing profile fields (display_name, timezone)
// from an external source — typically Slack via the bot token. Returns
// ("", "", false) on any failure so callers fall back to the
// unenriched row without erroring.
//
// Wired once at startup via RegisterUserEnricher. The package-level
// hook keeps GetUserByID/GetUserBySlackID free of slack-client deps
// while still letting them lazily hydrate users that were created as
// skeletons (e.g. participants added by start_vote / start_coordination
// who haven't DM'd Kit themselves).
type UserEnricher func(ctx context.Context, tenantID uuid.UUID, slackUserID string) (displayName, timezone string, ok bool)

var userEnricher UserEnricher

// RegisterUserEnricher installs the user-profile enrichment hook.
// Call once at startup — subsequent calls overwrite. Pass nil to
// disable (useful for tests).
func RegisterUserEnricher(e UserEnricher) {
	userEnricher = e
}

// hydrateUser checks if u has empty DisplayName or Timezone and, if
// so, asks the registered enricher and persists what comes back.
// Best-effort: failures return u unchanged. After this runs once per
// missing field, subsequent reads see the populated values and skip
// enrichment entirely — so this is "fetch from Slack once ever per
// user", not "every request."
func hydrateUser(ctx context.Context, pool *pgxpool.Pool, u *User) *User {
	if u == nil || userEnricher == nil {
		return u
	}
	needName := u.DisplayName == nil || *u.DisplayName == ""
	needTZ := u.Timezone == ""
	if !needName && !needTZ {
		return u
	}
	name, tz, ok := userEnricher(ctx, u.TenantID, u.SlackUserID)
	if !ok {
		return u
	}
	if !needName {
		name = ""
	}
	if !needTZ {
		tz = ""
	}
	if name == "" && tz == "" {
		return u
	}
	// Only fill in the empty columns. COALESCE leaves a non-null
	// display_name alone; the CASE keeps a non-empty timezone.
	if _, err := pool.Exec(ctx, `
		UPDATE users SET
		    display_name = COALESCE(display_name, $3),
		    timezone = CASE WHEN timezone = '' THEN $4 ELSE timezone END
		WHERE tenant_id = $1 AND id = $2
	`, u.TenantID, u.ID, nilIfEmpty(name), tz); err != nil {
		return u
	}
	if name != "" {
		n := name
		u.DisplayName = &n
	}
	if tz != "" {
		u.Timezone = tz
	}
	return u
}

// GetOrCreateUser finds a user by tenant + slack_user_id, creating if needed.
//
// timezone is the IANA TZ from Slack (users.info.tz). It populates the
// row on insert and lazily fills in an existing row whose timezone is
// still empty — e.g. a user created before we started fetching the
// profile. Once a non-empty TZ is stored, subsequent calls won't
// overwrite it (avoids stomping on a user-set value with whatever Slack
// reports). Callers that don't know the TZ pass "".
func GetOrCreateUser(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, slackUserID string, displayName, timezone string) (*User, error) {
	user := &User{}
	err := pool.QueryRow(ctx, `
		INSERT INTO users (id, tenant_id, slack_user_id, display_name, timezone)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, slack_user_id)
		DO UPDATE SET
			display_name = COALESCE(NULLIF(EXCLUDED.display_name, ''), users.display_name),
			timezone = CASE WHEN users.timezone = '' THEN EXCLUDED.timezone ELSE users.timezone END
		RETURNING id, tenant_id, slack_user_id, display_name, timezone, created_at
	`, uuid.New(), tenantID, slackUserID, nilIfEmpty(displayName), timezone).Scan(
		&user.ID, &user.TenantID, &user.SlackUserID, &user.DisplayName,
		&user.Timezone, &user.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get or create user: %w", err)
	}
	return user, nil
}

// GetUserBySlackID finds a user by tenant + slack_user_id. Returns
// nil-nil when no row exists (callers who want auto-create should use
// EnsureUserBySlackID instead — auth/messenger paths legitimately
// distinguish "user not registered" from "user with empty fields"). If
// a row is found with empty display_name or timezone, hydrates them
// from Slack via the registered enricher (best-effort, persisted).
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
	return hydrateUser(ctx, pool, user), nil
}

// GetUserByID finds a user by tenant + user ID. Hydrates display_name
// + timezone from Slack via the registered enricher if either is
// empty (best-effort, persisted).
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
	return hydrateUser(ctx, pool, user), nil
}

// EnsureUserBySlackID is the canonical "give me a populated user for
// this Slack ID" entry point. It:
//   - Creates a skeleton row if none exists (idempotent via the unique
//     index on tenant_id + slack_user_id)
//   - Hydrates display_name and timezone from Slack via the registered
//     enricher if either is empty (one fetch per missing field, ever —
//     subsequent calls see populated columns and short-circuit)
//   - Returns a User you can use without nil-checks on display name
//     (still fall back gracefully if Slack was unreachable, but the
//     fields will fill in next call)
//
// Use this from any path that adds a user to Kit's world based on a
// Slack ID (vote participants, coordination invites, role assignments).
// Auth and messenger paths that want to detect "not registered" should
// keep using GetUserBySlackID (read-only, returns nil-nil if absent).
func EnsureUserBySlackID(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, slackUserID string) (*User, error) {
	u, err := GetOrCreateUser(ctx, pool, tenantID, slackUserID, "", "")
	if err != nil {
		return nil, err
	}
	return hydrateUser(ctx, pool, u), nil
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
