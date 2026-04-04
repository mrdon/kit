package models

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Skill struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	Name          string
	Description   string
	Content       string
	UserInvocable bool
	Source        *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type SkillReference struct {
	ID       uuid.UUID
	SkillID  uuid.UUID
	TenantID uuid.UUID
	Filename string
	Content  string
}

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

// GetSkillCatalog returns name + description for skills visible to the given roles.
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

	// Create scope
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

func GetSkillReference(ctx context.Context, pool *pgxpool.Pool, tenantID, refID uuid.UUID) (*SkillReference, error) {
	ref := &SkillReference{}
	err := pool.QueryRow(ctx, `
		SELECT id, skill_id, tenant_id, filename, content
		FROM skill_references WHERE tenant_id = $1 AND id = $2
	`, tenantID, refID).Scan(&ref.ID, &ref.SkillID, &ref.TenantID, &ref.Filename, &ref.Content)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting reference: %w", err)
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
