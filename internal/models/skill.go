package models

import (
	"context"
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
		return fmt.Errorf("name must be 1-64 characters")
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
	if i := strings.Index(rest, "\n---"); i < 0 {
		return "", "", raw, nil
	} else {
		frontmatter := rest[:i]
		content = strings.TrimSpace(rest[i+4:])

		for _, line := range strings.Split(frontmatter, "\n") {
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

func GetSkillCatalog(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, userRoles []string) ([]Skill, error) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT s.id, s.name, s.description
		FROM skills s
		JOIN skill_scopes ss ON ss.skill_id = s.id AND ss.tenant_id = s.tenant_id
		WHERE s.tenant_id = $1
		AND (
			(ss.scope_type = 'tenant' AND ss.scope_value = '*')
			OR (ss.scope_type = 'role' AND ss.scope_value = ANY($2))
		)
		ORDER BY s.name
	`, tenantID, userRoles)
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
		RETURNING id, tenant_id, name, description, content, user_invocable, source, created_at, updated_at
	`, skillID, tenantID, name, description, content, source).Scan(
		&skill.ID, &skill.TenantID, &skill.Name, &skill.Description, &skill.Content,
		&skill.UserInvocable, &skill.Source, &skill.CreatedAt, &skill.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating skill: %w", err)
	}

	scopeType := "tenant"
	scopeValue := "*"
	if scope != "tenant" && scope != "" {
		scopeType = "role"
		scopeValue = scope
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO skill_scopes (tenant_id, skill_id, scope_type, scope_value)
		VALUES ($1, $2, $3, $4)
	`, tenantID, skillID, scopeType, scopeValue)
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
	if err == pgx.ErrNoRows {
		return nil, nil
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
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting skill file: %w", err)
	}
	return ref, nil
}

// SearchSkills performs FTS on skills visible to the user's roles.
func SearchSkills(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, userRoles []string, query string) ([]Skill, error) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT s.id, s.name, s.description
		FROM skills s
		JOIN skill_scopes ss ON ss.skill_id = s.id AND ss.tenant_id = s.tenant_id
		WHERE s.tenant_id = $1
		AND (
			(ss.scope_type = 'tenant' AND ss.scope_value = '*')
			OR (ss.scope_type = 'role' AND ss.scope_value = ANY($2))
		)
		AND (
			to_tsvector('english', s.content) @@ plainto_tsquery('english', $3)
			OR to_tsvector('english', s.description) @@ plainto_tsquery('english', $3)
		)
		ORDER BY s.name
		LIMIT 10
	`, tenantID, userRoles, query)
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
