package posters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/posterrender"
	"github.com/mrdon/kit/internal/services"
)

// Skill names the app depends on. The branding guide is the only source of
// the visual system; the copy guide is the voice.
const (
	brandingSkill = "branding-guide"
	copySkill     = "writing-copy"
)

// ErrNoBrand is returned when the tenant has no usable brand yet. The
// message names the fix.
var ErrNoBrand = errors.New("no brand")

// BrandStatus is what the admin page shows: the brand, where it came from,
// and what stopped it.
type BrandStatus struct {
	Brand     *posterrender.Brand `json:"brand,omitempty"`
	Hash      string              `json:"hash"`
	Problems  []string            `json:"problems"`
	Source    string              `json:"source"` // "json", "model", or "" when no skill
	SkillID   string              `json:"skill_id,omitempty"`
	UpdatedAt string              `json:"updated_at,omitempty"`
}

// systemCaller reads skills as an admin of the tenant: brand derivation
// runs on behalf of the whole workspace (a scheduled sync, an event page
// anyone can open), not one person's role set. The branding guide is a
// tenant-wide fact; this does not widen what any person can read.
func systemCaller(tenantID uuid.UUID) *services.Caller {
	return &services.Caller{Kind: services.CallerAgent, TenantID: tenantID, IsAdmin: true}
}

// brandFor returns the tenant's brand, deriving and caching it when the
// skill changed since last time. Problems come back as ErrNoBrand wrapping
// the first one.
func (a *App) brandFor(ctx context.Context, tenantID uuid.UUID) (*posterrender.Brand, error) {
	st, err := a.brandStatus(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if st.Brand == nil {
		if len(st.Problems) > 0 {
			return nil, fmt.Errorf("%w: %s", ErrNoBrand, st.Problems[0])
		}
		return nil, fmt.Errorf("%w: add a %s skill first", ErrNoBrand, brandingSkill)
	}
	return st.Brand, nil
}

// brandStatus is brandFor with the full report.
func (a *App) brandStatus(ctx context.Context, tenantID uuid.UUID) (*BrandStatus, error) {
	skill, _, err := a.svc.Skills.ResolveByName(ctx, systemCaller(tenantID), brandingSkill)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			return &BrandStatus{Problems: []string{fmt.Sprintf("no %s skill: create one and the brand derives from it", brandingSkill)}}, nil
		}
		return nil, fmt.Errorf("loading branding guide: %w", err)
	}
	hash := contentHash(skill.Content)
	settings, err := getSettings(ctx, a.pool, tenantID)
	if err != nil {
		return nil, err
	}
	status := &BrandStatus{Hash: hash, SkillID: skill.ID.String(), Problems: []string{}}
	if settings.BrandHash == hash && (settings.Brand != nil || len(settings.BrandProblems) > 0) {
		status.Brand, status.Problems = settings.Brand, settings.BrandProblems
	} else {
		status.Brand, status.Problems = a.deriveAndCache(ctx, tenantID, skill, hash)
	}
	if guideJSONBlock(skill.Content) != "" {
		status.Source = "json"
	} else {
		status.Source = "model"
	}
	if settings.BrandDerivedAt != nil {
		status.UpdatedAt = settings.BrandDerivedAt.Format("2006-01-02 15:04")
	}
	if status.Brand != nil {
		a.attachLogos(status.Brand, settings)
		status.Brand.BannedWords = a.bannedWords(ctx, tenantID)
	}
	return status, nil
}

// deriveAndCache runs the derivation for a skill version and stores the
// outcome, so the model call (when needed) happens once per version.
func (a *App) deriveAndCache(ctx context.Context, tenantID uuid.UUID, skill *models.Skill, hash string) (*posterrender.Brand, []string) {
	var g *guideTokens
	var err error
	if block := guideJSONBlock(skill.Content); block != "" {
		g, err = parseGuideTokens(block)
	} else {
		g, err = a.extractTokens(ctx, skill.Content)
	}
	var brand *posterrender.Brand
	var problems []string
	if err != nil {
		problems = []string{err.Error()}
	} else {
		brand, problems = deriveBrand(g, hash)
	}
	if cerr := setBrand(ctx, a.pool, tenantID, brand, hash, problems); cerr != nil {
		slog.Warn("posters: caching brand", "error", cerr, "tenant_id", tenantID)
	}
	return brand, problems
}

// extractTokens is the one model call in brand derivation: a guide with no
// machine-readable block is read by Sonnet into the same shape.
func (a *App) extractTokens(ctx context.Context, guide string) (*guideTokens, error) {
	var raw map[string]any
	system := mustRender("system_brand_extract.tmpl", nil)
	user := mustRender("user_brand_extract.tmpl", map[string]any{"Guide": guide})
	if err := askJSON(ctx, a.llm, system, user, nil, &raw); err != nil {
		return nil, fmt.Errorf("the guide has no json tokens block and the model could not read one from the prose: %w", err)
	}
	// Round-trip through the typed parser so both paths validate alike.
	b, _ := jsonMarshal(raw)
	return parseGuideTokens(string(b))
}

// attachLogos adds the mapped logo files as image references.
func (a *App) attachLogos(brand *posterrender.Brand, s Settings) {
	brand.Logos = map[string]posterrender.ImageRef{}
	for variant, f := range s.LogoMap {
		if f.FileID == "" {
			continue
		}
		brand.Logos[variant] = posterrender.ImageRef{ID: "logo:" + f.FileID, Source: posterrender.SourceDrive, FileID: f.FileID, Modified: f.Modified}
	}
}

// bannedWords reads an optional fenced json block in the writing-copy
// skill of the form {"banned": [...]} so a tenant's own tells (a brewery's
// "hoppy hour") join the generic list. Missing is fine.
func (a *App) bannedWords(ctx context.Context, tenantID uuid.UUID) []string {
	skill, _, err := a.svc.Skills.ResolveByName(ctx, systemCaller(tenantID), copySkill)
	if err != nil || skill == nil {
		return nil
	}
	for _, m := range jsonFence.FindAllStringSubmatch(skill.Content, -1) {
		var v struct {
			Banned []string `json:"banned"`
		}
		if jsonUnmarshal([]byte(m[1]), &v) == nil && len(v.Banned) > 0 {
			out := make([]string, 0, len(v.Banned))
			for _, w := range v.Banned {
				if w = strings.TrimSpace(w); w != "" {
					out = append(out, w)
				}
			}
			return out
		}
	}
	return nil
}

// skillText returns a skill's content for a prompt, or "" when the tenant
// has none. Both guides are optional context for the copy call; the brand
// itself has already been checked by then.
func (a *App) skillText(ctx context.Context, tenantID uuid.UUID, name string) string {
	skill, _, err := a.svc.Skills.ResolveByName(ctx, systemCaller(tenantID), name)
	if err != nil || skill == nil {
		return ""
	}
	return skill.Content
}
