package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// Ingester processes uploaded files and creates skills.
type Ingester struct {
	pool  *pgxpool.Pool
	llm   *anthropic.Client
	slack *kitslack.Client
}

// NewIngester creates a new file ingester.
func NewIngester(pool *pgxpool.Pool, llm *anthropic.Client, slack *kitslack.Client) *Ingester {
	return &Ingester{pool: pool, llm: llm, slack: slack}
}

// ProcessFile downloads a file from Slack, extracts text, converts to skill(s).
func (ing *Ingester) ProcessFile(ctx context.Context, tenantID uuid.UUID, file kitslack.File) ([]string, error) {
	// Download file content
	data, err := ing.slack.GetFileContent(ctx, file.URL)
	if err != nil {
		return nil, fmt.Errorf("downloading file %s: %w", file.Name, err)
	}

	ext := strings.ToLower(filepath.Ext(file.Name))

	// Handle zip files — process each file inside
	if ext == ".zip" || file.MimeType == "application/zip" {
		return ing.processZip(ctx, tenantID, data)
	}

	// If it's a SKILL.md file, parse directly without LLM
	if strings.ToLower(filepath.Base(file.Name)) == "skill.md" || ext == ".skill.md" {
		name, description, content, err := models.ParseSKILLMD(string(data))
		if err != nil {
			return nil, fmt.Errorf("parsing SKILL.md %s: %w", file.Name, err)
		}
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(file.Name), filepath.Ext(file.Name))
		}
		skill, err := models.CreateSkill(ctx, ing.pool, tenantID, name, description, content, "upload", "tenant")
		if err != nil {
			return nil, fmt.Errorf("creating skill from SKILL.md %s: %w", file.Name, err)
		}
		slog.Info("imported SKILL.md", "skill_id", skill.ID, "name", name)
		return []string{name}, nil
	}

	// Extract text
	rawText, err := ExtractText(data, file.Name, file.MimeType)
	if err != nil {
		return nil, fmt.Errorf("extracting text from %s: %w", file.Name, err)
	}

	if strings.TrimSpace(rawText) == "" {
		return nil, fmt.Errorf("no text extracted from %s", file.Name)
	}

	// Convert to structured skill via LLM
	name, description, content, err := ConvertToSkill(ctx, ing.llm, file.Name, rawText)
	if err != nil {
		return nil, fmt.Errorf("converting %s to skill: %w", file.Name, err)
	}

	// Create skill (tenant-wide scope)
	skill, err := models.CreateSkill(ctx, ing.pool, tenantID, name, description, content, "upload", "tenant")
	if err != nil {
		return nil, fmt.Errorf("creating skill from %s: %w", file.Name, err)
	}

	slog.Info("created skill from file", "skill_id", skill.ID, "name", name, "filename", file.Name)
	return []string{name}, nil
}

func (ing *Ingester) processZip(ctx context.Context, tenantID uuid.UUID, data []byte) ([]string, error) {
	files, err := ExtractZip(data)
	if err != nil {
		return nil, fmt.Errorf("extracting zip: %w", err)
	}

	var created []string
	for filename, content := range files {
		rawText, err := ExtractText(content, filename, "")
		if err != nil {
			slog.Warn("skipping file in zip", "filename", filename, "error", err)
			continue
		}
		if strings.TrimSpace(rawText) == "" {
			continue
		}

		name, description, skillContent, err := ConvertToSkill(ctx, ing.llm, filename, rawText)
		if err != nil {
			slog.Warn("conversion failed for zip entry", "filename", filename, "error", err)
			continue
		}

		_, err = models.CreateSkill(ctx, ing.pool, tenantID, name, description, skillContent, "upload", "tenant")
		if err != nil {
			slog.Warn("skill creation failed for zip entry", "filename", filename, "error", err)
			continue
		}

		created = append(created, name)
	}

	return created, nil
}
