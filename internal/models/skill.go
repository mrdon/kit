package models

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Skill struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	Name          string // spec-compliant slug (lowercase, hyphens)
	Description   string
	Content       string
	UserInvocable bool
	Source        *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type SkillFile struct {
	ID       uuid.UUID
	SkillID  uuid.UUID
	TenantID uuid.UUID
	Filename string
	Content  string
}

// --- Name validation per Agent Skills spec ---

var validNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidateSkillName checks if a name meets the Agent Skills spec.
func ValidateSkillName(name string) error {
	if len(name) == 0 || len(name) > 64 {
		return errors.New("name must be 1-64 characters")
	}
	if !validNameRe.MatchString(name) {
		return fmt.Errorf("name must be lowercase letters, numbers, and single hyphens (got %q)", name)
	}
	return nil
}

// SlugifyName converts a display name to a spec-compliant slug.
func SlugifyName(display string) string {
	var b strings.Builder
	prevHyphen := true // prevent leading hyphen
	for _, r := range strings.ToLower(display) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevHyphen = false
		case !prevHyphen:
			b.WriteRune('-')
			prevHyphen = true
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > 64 {
		s = s[:64]
		s = strings.TrimRight(s, "-")
	}
	return s
}

// --- SKILL.md serialization ---

// ToSKILLMD serializes a Skill into Agent Skills spec SKILL.md format.
func (s *Skill) ToSKILLMD() string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("name: %s\n", s.Name))
	b.WriteString(fmt.Sprintf("description: %s\n", s.Description))
	b.WriteString("---\n\n")
	b.WriteString(s.Content)
	return b.String()
}

// ParseSKILLMD parses a SKILL.md file into name, description, and content.
func ParseSKILLMD(raw string) (name, description, content string, err error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "---") {
		return "", "", raw, nil // no frontmatter, treat entire thing as content
	}

	// Find closing ---
	rest := raw[3:]
	if before, after, found := strings.Cut(rest, "\n---"); !found {
		return "", "", raw, nil
	} else {
		frontmatter := before
		content = strings.TrimSpace(after)

		for line := range strings.SplitSeq(frontmatter, "\n") {
			line = strings.TrimSpace(line)
			if k, v, ok := strings.Cut(line, ":"); ok {
				k = strings.TrimSpace(k)
				v = strings.TrimSpace(v)
				switch k {
				case "name":
					name = v
				case "description":
					description = v
				}
			}
		}
	}
	return name, description, content, nil
}

// --- CRUD ---

func ListSkills(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]Skill, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, name, description, content, user_invocable, source, created_at, updated_at
		FROM skills WHERE tenant_id = $1 ORDER BY name
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing skills: %w", err)
	}
	defer rows.Close()

	var skills []Skill
	for rows.Next() {
		var s Skill
		if err := rows.Scan(&s.ID, &s.TenantID, &s.Name, &s.Description, &s.Content,
			&s.UserInvocable, &s.Source, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		skills = append(skills, s)
	}
	return skills, rows.Err()
}

func GetSkillCatalog(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, roleIDs []uuid.UUID) ([]Skill, error) {
	scopeSQL, scopeArgs := ScopeFilterIDs("sc", 2, userID, roleIDs)
	args := append([]any{tenantID}, scopeArgs...)
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT s.id, s.name, s.description
		FROM skills s
		JOIN skill_scopes ss ON ss.skill_id = s.id AND ss.tenant_id = s.tenant_id
		JOIN scopes sc ON sc.id = ss.scope_id
		WHERE s.tenant_id = $1
		AND (`+scopeSQL+`)
		ORDER BY s.name
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("getting skill catalog: %w", err)
	}
	defer rows.Close()

	var skills []Skill
	for rows.Next() {
		var s Skill
		if err := rows.Scan(&s.ID, &s.Name, &s.Description); err != nil {
			return nil, err
		}
		skills = append(skills, s)
	}
	return skills, rows.Err()
}

// SkillSummary is a skill with its scope information, used for listing.
type SkillSummary struct {
	ID          uuid.UUID
	Name        string
	Description string
	Scopes      []SkillScope
}

// ListSkillsFiltered returns skills visible to the caller with optional search.
// If admin is true, all skills in the tenant are returned.
// Otherwise, only skills matching the caller's roles/identity are returned.
func ListSkillsFiltered(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, admin bool, userID uuid.UUID, roleIDs []uuid.UUID, search string) ([]SkillSummary, error) {
	var args []any
	args = append(args, tenantID) // $1

	var where strings.Builder
	where.WriteString("WHERE s.tenant_id = $1")

	if !admin {
		scopeSQL, scopeArgs := ScopeFilterIDs("sc", len(args)+1, userID, roleIDs)
		args = append(args, scopeArgs...)
		where.WriteString("\n\t\t\tAND (" + scopeSQL + ")")
	}

	if search != "" {
		args = append(args, "%"+strings.ToLower(search)+"%")
		where.WriteString(fmt.Sprintf("\n\t\t\tAND (LOWER(s.name) LIKE $%d OR LOWER(s.description) LIKE $%d)", len(args), len(args)))
	}

	join := ""
	if !admin {
		join = `JOIN skill_scopes ss ON ss.skill_id = s.id AND ss.tenant_id = s.tenant_id
		JOIN scopes sc ON sc.id = ss.scope_id`
	}

	q := fmt.Sprintf(`
		SELECT DISTINCT s.id, s.name, s.description
		FROM skills s
		%s
		%s
		ORDER BY s.name
	`, join, where.String())

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing skills: %w", err)
	}
	defer rows.Close()

	var skills []SkillSummary
	for rows.Next() {
		var s SkillSummary
		if err := rows.Scan(&s.ID, &s.Name, &s.Description); err != nil {
			return nil, err
		}
		skills = append(skills, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Batch-load scopes for all returned skills.
	if len(skills) > 0 {
		ids := make([]uuid.UUID, len(skills))
		for i, s := range skills {
			ids[i] = s.ID
		}
		scopeMap, err := getSkillScopesBatch(ctx, pool, tenantID, ids)
		if err != nil {
			return nil, err
		}
		for i := range skills {
			skills[i].Scopes = scopeMap[skills[i].ID]
		}
	}

	return skills, nil
}

// getSkillScopesBatch returns scopes for multiple skills in one query.
// Joins the canonical scopes table back to roles/users to recover the
// human-readable scope_value (role name or slack_user_id) for display.
func getSkillScopesBatch(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, skillIDs []uuid.UUID) (map[uuid.UUID][]SkillScope, error) {
	rows, err := pool.Query(ctx, `
		SELECT
			ss.skill_id,
			CASE
				WHEN sc.role_id IS NULL AND sc.user_id IS NULL THEN 'tenant'
				WHEN sc.role_id IS NOT NULL THEN 'role'
				ELSE 'user'
			END AS scope_type,
			COALESCE(r.name, u.slack_user_id, '*') AS scope_value
		FROM skill_scopes ss
		JOIN scopes sc ON sc.id = ss.scope_id
		LEFT JOIN roles r ON r.id = sc.role_id
		LEFT JOIN users u ON u.id = sc.user_id
		WHERE ss.tenant_id = $1 AND ss.skill_id = ANY($2)
	`, tenantID, skillIDs)
	if err != nil {
		return nil, fmt.Errorf("batch loading skill scopes: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID][]SkillScope)
	for rows.Next() {
		var skillID uuid.UUID
		var sc SkillScope
		if err := rows.Scan(&skillID, &sc.ScopeType, &sc.ScopeValue); err != nil {
			return nil, err
		}
		result[skillID] = append(result[skillID], sc)
	}
	return result, rows.Err()
}

func CreateSkill(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name, description, content, source, scope string) (*Skill, error) {
	// Slugify and validate name
	name = SlugifyName(name)
	if err := ValidateSkillName(name); err != nil {
		return nil, fmt.Errorf("invalid skill name: %w", err)
	}
	if len(description) > 1024 {
		description = description[:1024]
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	skill := &Skill{}
	skillID := uuid.New()
	err = tx.QueryRow(ctx, `
		INSERT INTO skills (id, tenant_id, name, description, content, source)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, name)
		DO UPDATE SET description = EXCLUDED.description, content = EXCLUDED.content, source = EXCLUDED.source, updated_at = now()
		RETURNING id, tenant_id, name, description, content, user_invocable, source, created_at, updated_at
	`, skillID, tenantID, name, description, content, source).Scan(
		&skill.ID, &skill.TenantID, &skill.Name, &skill.Description, &skill.Content,
		&skill.UserInvocable, &skill.Source, &skill.CreatedAt, &skill.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating skill: %w", err)
	}

	var roleID *uuid.UUID
	if scope != string(ScopeTypeTenant) && scope != "" {
		var rid uuid.UUID
		err := tx.QueryRow(ctx,
			`SELECT id FROM roles WHERE tenant_id = $1 AND name = $2`,
			tenantID, scope).Scan(&rid)
		if err != nil {
			return nil, fmt.Errorf("looking up role %q for skill scope: %w", scope, err)
		}
		roleID = &rid
	}
	scopeID, err := getOrCreateScopeTx(ctx, tx, tenantID, roleID, nil)
	if err != nil {
		return nil, fmt.Errorf("get-or-create scope: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO skill_scopes (tenant_id, skill_id, scope_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, tenantID, skill.ID, scopeID)
	if err != nil {
		return nil, fmt.Errorf("creating skill scope: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}
	return skill, nil
}

func UpdateSkill(ctx context.Context, pool *pgxpool.Pool, tenantID, skillID uuid.UUID, name, description, content *string) error {
	if name != nil {
		slugged := SlugifyName(*name)
		if err := ValidateSkillName(slugged); err != nil {
			return fmt.Errorf("invalid skill name: %w", err)
		}
		name = &slugged
	}
	if description != nil && len(*description) > 1024 {
		trimmed := (*description)[:1024]
		description = &trimmed
	}
	_, err := pool.Exec(ctx, `
		UPDATE skills SET
			name = COALESCE($3, name),
			description = COALESCE($4, description),
			content = COALESCE($5, content),
			updated_at = now()
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, skillID, name, description, content)
	if err != nil {
		return fmt.Errorf("updating skill: %w", err)
	}
	return nil
}

func DeleteSkill(ctx context.Context, pool *pgxpool.Pool, tenantID, skillID uuid.UUID) error {
	_, err := pool.Exec(ctx, `DELETE FROM skills WHERE tenant_id = $1 AND id = $2`, tenantID, skillID)
	if err != nil {
		return fmt.Errorf("deleting skill: %w", err)
	}
	return nil
}

func GetSkill(ctx context.Context, pool *pgxpool.Pool, tenantID, skillID uuid.UUID) (*Skill, error) {
	s := &Skill{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, name, description, content, user_invocable, source, created_at, updated_at
		FROM skills WHERE tenant_id = $1 AND id = $2
	`, tenantID, skillID).Scan(
		&s.ID, &s.TenantID, &s.Name, &s.Description, &s.Content,
		&s.UserInvocable, &s.Source, &s.CreatedAt, &s.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting skill: %w", err)
	}
	return s, nil
}

// --- Skill files ---

func AddSkillFile(ctx context.Context, pool *pgxpool.Pool, tenantID, skillID uuid.UUID, filename, content string) (*SkillFile, error) {
	f := &SkillFile{}
	err := pool.QueryRow(ctx, `
		INSERT INTO skill_references (id, skill_id, tenant_id, filename, content)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, skill_id, tenant_id, filename, content
	`, uuid.New(), skillID, tenantID, filename, content).Scan(
		&f.ID, &f.SkillID, &f.TenantID, &f.Filename, &f.Content,
	)
	if err != nil {
		return nil, fmt.Errorf("adding skill file: %w", err)
	}
	return f, nil
}

func ListSkillFiles(ctx context.Context, pool *pgxpool.Pool, tenantID, skillID uuid.UUID) ([]SkillFile, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, skill_id, tenant_id, filename, content
		FROM skill_references WHERE tenant_id = $1 AND skill_id = $2
		ORDER BY filename
	`, tenantID, skillID)
	if err != nil {
		return nil, fmt.Errorf("listing skill files: %w", err)
	}
	defer rows.Close()

	var files []SkillFile
	for rows.Next() {
		var f SkillFile
		if err := rows.Scan(&f.ID, &f.SkillID, &f.TenantID, &f.Filename, &f.Content); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

func DeleteSkillFile(ctx context.Context, pool *pgxpool.Pool, tenantID, fileID uuid.UUID) error {
	_, err := pool.Exec(ctx, `DELETE FROM skill_references WHERE tenant_id = $1 AND id = $2`, tenantID, fileID)
	if err != nil {
		return fmt.Errorf("deleting skill file: %w", err)
	}
	return nil
}

func GetSkillReference(ctx context.Context, pool *pgxpool.Pool, tenantID, refID uuid.UUID) (*SkillFile, error) {
	ref := &SkillFile{}
	err := pool.QueryRow(ctx, `
		SELECT id, skill_id, tenant_id, filename, content
		FROM skill_references WHERE tenant_id = $1 AND id = $2
	`, tenantID, refID).Scan(&ref.ID, &ref.SkillID, &ref.TenantID, &ref.Filename, &ref.Content)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting skill file: %w", err)
	}
	return ref, nil
}

// SkillScope represents a single scope row for a skill.
type SkillScope struct {
	ScopeType  ScopeType
	ScopeValue string
}

// GetSkillScopeRefs returns the scopes table rows referenced by a skill,
// for use with services.Caller.CanSee. Unlike GetSkillScopes (which returns
// human-readable display values), this returns the underlying scope row IDs
// and role/user FKs.
func GetSkillScopeRefs(ctx context.Context, pool *pgxpool.Pool, tenantID, skillID uuid.UUID) ([]ScopeRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT sc.id, sc.role_id, sc.user_id
		FROM skill_scopes ss
		JOIN scopes sc ON sc.id = ss.scope_id
		WHERE ss.tenant_id = $1 AND ss.skill_id = $2
	`, tenantID, skillID)
	if err != nil {
		return nil, fmt.Errorf("getting skill scope refs: %w", err)
	}
	defer rows.Close()

	var refs []ScopeRow
	for rows.Next() {
		var r ScopeRow
		if err := rows.Scan(&r.ID, &r.RoleID, &r.UserID); err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}

// GetSkillScopes returns the scope rows for a skill.
func GetSkillScopes(ctx context.Context, pool *pgxpool.Pool, tenantID, skillID uuid.UUID) ([]SkillScope, error) {
	rows, err := pool.Query(ctx, `
		SELECT
			CASE
				WHEN sc.role_id IS NULL AND sc.user_id IS NULL THEN 'tenant'
				WHEN sc.role_id IS NOT NULL THEN 'role'
				ELSE 'user'
			END AS scope_type,
			COALESCE(r.name, u.slack_user_id, '*') AS scope_value
		FROM skill_scopes ss
		JOIN scopes sc ON sc.id = ss.scope_id
		LEFT JOIN roles r ON r.id = sc.role_id
		LEFT JOIN users u ON u.id = sc.user_id
		WHERE ss.tenant_id = $1 AND ss.skill_id = $2
	`, tenantID, skillID)
	if err != nil {
		return nil, fmt.Errorf("getting skill scopes: %w", err)
	}
	defer rows.Close()

	var scopes []SkillScope
	for rows.Next() {
		var s SkillScope
		if err := rows.Scan(&s.ScopeType, &s.ScopeValue); err != nil {
			return nil, err
		}
		scopes = append(scopes, s)
	}
	return scopes, rows.Err()
}

// SearchSkills performs FTS on skills visible to the user's roles.
func SearchSkills(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, roleIDs []uuid.UUID, query string) ([]Skill, error) {
	scopeSQL, scopeArgs := ScopeFilterIDs("sc", 2, userID, roleIDs)
	ftsParam := 2 + len(scopeArgs)
	args := append([]any{tenantID}, scopeArgs...)
	args = append(args, query)
	rows, err := pool.Query(ctx, fmt.Sprintf(`
		SELECT DISTINCT s.id, s.name, s.description
		FROM skills s
		JOIN skill_scopes ss ON ss.skill_id = s.id AND ss.tenant_id = s.tenant_id
		JOIN scopes sc ON sc.id = ss.scope_id
		WHERE s.tenant_id = $1
		AND (%s)
		AND (
			to_tsvector('english', s.content) @@ plainto_tsquery('english', $%d)
			OR to_tsvector('english', s.description) @@ plainto_tsquery('english', $%d)
		)
		ORDER BY s.name
		LIMIT 10
	`, scopeSQL, ftsParam, ftsParam), args...)
	if err != nil {
		return nil, fmt.Errorf("searching skills: %w", err)
	}
	defer rows.Close()

	var skills []Skill
	for rows.Next() {
		var s Skill
		if err := rows.Scan(&s.ID, &s.Name, &s.Description); err != nil {
			return nil, err
		}
		skills = append(skills, s)
	}
	return skills, rows.Err()
}
