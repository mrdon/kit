package posters

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps/events"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/posterrender"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// fakeLLM answers each prompt family with canned JSON, keyed on a phrase
// from the system prompt, so a test exercises the flow without a model.
type fakeLLM struct {
	calls int
	reply func(system, user string) string
}

func (f *fakeLLM) CreateMessage(_ context.Context, req *anthropic.Request) (*anthropic.Response, error) {
	f.calls++
	system := ""
	if len(req.System) > 0 {
		system = req.System[0].Text
	}
	var user strings.Builder
	for _, c := range req.Messages[0].Content {
		if c.Type == "text" {
			user.WriteString(c.Text)
		}
	}
	return &anthropic.Response{Content: []anthropic.Content{{Type: "text", Text: f.reply(system, user.String())}}}, nil
}

func cannedReply(system, _ string) string {
	switch {
	case strings.Contains(system, "write the words on an event poster"):
		return `{"eyebrow":"Thu Oct 15","title":"Vinyl night","summary":"Bring a record, we play a side.","details":[{"label":"When","value":"7 to 10pm"}],"action":"Records welcome"}`
	case strings.Contains(system, "choose the hero photo"):
		return `{"id":"__FIRST__","focus_x":0.6,"focus_y":0.4,"why":"it is the room"}`
	case strings.Contains(system, "update the words on a poster"):
		return `{"source":"// updated\n","changes":["time 7:30pm"]}`
	}
	return `{}`
}

type fixture struct {
	ctx    context.Context
	pool   *pgxpool.Pool
	app    *App
	tenant *models.Tenant
	user   *models.User
	events *events.Service
	fake   *posterrender.Fake
	llm    *fakeLLM
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()
	teamID := "T_po_" + uuid.NewString()
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "posters-test", "enc", models.SanitizeSlug("po-"+uuid.NewString(), teamID), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })
	user, err := models.EnsureUserBySlackID(ctx, pool, tenant.ID, "U_po_"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	guide, err := os.ReadFile("testdata/gravity-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.CreateSkill(ctx, pool, tenant.ID, brandingSkill, "brand", string(guide), "test", "tenant"); err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.NewEncryptor(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	evs := events.NewService(pool)
	fake := &posterrender.Fake{}
	llm := &fakeLLM{reply: cannedReply}
	app := &App{
		pool: pool, svc: services.New(pool, enc, "http://kit.test", nil), enc: enc, llm: llm,
		renderer: fake, cache: newRenderCache(nil), pixabay: newPixabay("", nil),
		eventsSvc: func() *events.Service { return evs },
	}
	app.installBuiltins(ctx)
	app.installEventListener()
	return &fixture{ctx: ctx, pool: pool, app: app, tenant: tenant, user: user, events: evs, fake: fake, llm: llm}
}

func (f *fixture) event(t *testing.T) *events.Event {
	t.Helper()
	price := int64(500)
	ev, err := f.events.Create(f.ctx, f.tenant.ID, events.CreateParams{
		Title: "Vinyl night", Summary: "Bring a record.", PrepNotes: "secret staff note",
		StartsAt: time.Now().AddDate(0, 0, 7).Format("2006-01-02") + " 19:00", PriceCents: &price,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func (f *fixture) indexedPhoto(t *testing.T, name, folder, description string) *Photo {
	t.Helper()
	p, _, err := upsertListedPhoto(f.ctx, f.pool, f.tenant.ID, "drive-"+uuid.NewString(), "2026-01-01", folder, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := setPhotoFacts(f.ctx, f.pool, f.tenant.ID, p.ID, 1600, 1200, "landscape", "none"); err != nil {
		t.Fatal(err)
	}
	p, err = indexPhoto(f.ctx, f.pool, f.tenant.ID, IndexEntry{ID: p.ID, Description: description, Tags: []string{"taproom"}, FocusX: 0.5, FocusY: 0.5}, "test")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGeneratePickAndStale(t *testing.T) {
	f := newFixture(t)
	ev := f.event(t)
	hero := f.indexedPhoto(t, "a.jpg", "vinyl", "A turntable on the bar with records stacked beside it, warm light")
	f.indexedPhoto(t, "b.jpg", "vinyl", "Shelves of records in the taproom corner")
	f.indexedPhoto(t, "c.jpg", "other", "The brewhouse tanks")
	f.llm.reply = func(system, user string) string {
		return strings.ReplaceAll(cannedReply(system, user), "__FIRST__", hero.ID.String())
	}

	var stages []GenerateStage
	res, err := f.app.Generate(f.ctx, f.tenant.ID, f.user.ID, ev.ID, false, func(stage GenerateStage, _ map[string]any) { stages = append(stages, stage) })
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if want := []GenerateStage{StageCopy, StagePhoto, StageRendering}; !slices.Equal(stages, want) {
		t.Errorf("stages = %v", stages)
	}
	if len(res.Options) != 7 {
		t.Fatalf("options = %d", len(res.Options))
	}
	if f.llm.calls != 2 {
		t.Errorf("model calls = %d, want copy + photo pick", f.llm.calls)
	}
	req := f.fake.OptionsCalls[0]
	if req.Hero == nil || req.Hero.ID != hero.ID.String() || req.Hero.FocusX != 0.6 {
		t.Errorf("hero = %+v", req.Hero)
	}
	// The photo set is the hero's folder, never the root or another set.
	if len(req.Photos) != 2 {
		t.Errorf("photo set = %d photos, want the two in the vinyl folder", len(req.Photos))
	}
	if strings.Contains(req.Content.Summary, "secret") {
		t.Error("prep notes reached the renderer")
	}
	if len(req.Templates) != 7 {
		t.Errorf("templates = %d", len(req.Templates))
	}

	// More options reuses copy and hero (no model calls) and avoids shown templates.
	calls := f.llm.calls
	if _, err := f.app.Generate(f.ctx, f.tenant.ID, f.user.ID, ev.ID, true, nil); err != nil {
		t.Fatalf("more: %v", err)
	}
	if f.llm.calls != calls {
		t.Errorf("more options made %d model calls", f.llm.calls-calls)
	}
	if len(f.fake.OptionsCalls[1].Exclude) != 7 {
		t.Errorf("exclude = %v", f.fake.OptionsCalls[1].Exclude)
	}

	// Pick: the version goes on the event and the facts are recorded.
	picked := res.Options[2]
	poster, err := f.app.Pick(f.ctx, f.tenant.ID, f.user.ID, res.Poster.ID, picked.ID)
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if poster.SetVersionID == nil || *poster.SetVersionID != picked.ID || poster.Stale || poster.FactsHash == "" {
		t.Fatalf("poster after pick = %+v", poster)
	}
	ev2, err := f.events.Get(f.ctx, f.tenant.ID, ev.ID)
	if err != nil || ev2.HeroAttachmentID == nil {
		t.Fatalf("event has no poster attachment: %v %+v", err, ev2)
	}
	ts, _ := listTemplates(f.ctx, f.pool, f.tenant.ID, "")
	picks := 0
	for _, tpl := range ts {
		picks += tpl.Picks
	}
	if picks != 1 {
		t.Errorf("template picks = %d, want 1", picks)
	}

	// A staff-notes edit does not flag the poster; a time change does.
	notes := "new staff note"
	if _, err := f.events.Update(f.ctx, f.tenant.ID, ev.ID, events.UpdateParams{PrepNotes: &notes}); err != nil {
		t.Fatal(err)
	}
	poster, _ = getPoster(f.ctx, f.pool, f.tenant.ID, poster.ID)
	if poster.Stale {
		t.Error("prep_notes edit flagged the poster")
	}
	start := time.Now().AddDate(0, 0, 8).Format("2006-01-02") + " 19:30"
	if _, err := f.events.Update(f.ctx, f.tenant.ID, ev.ID, events.UpdateParams{StartsAt: &start}); err != nil {
		t.Fatal(err)
	}
	poster, _ = getPoster(f.ctx, f.pool, f.tenant.ID, poster.ID)
	if !poster.Stale {
		t.Fatal("time change did not flag the poster")
	}

	// Update facts saves a new version next to the current; the event's
	// poster is untouched until someone sets it.
	edit, changes, err := f.app.UpdateFacts(f.ctx, f.tenant.ID, poster.ID, "agent")
	if err != nil {
		t.Fatalf("update facts: %v", err)
	}
	if edit.Version == nil || len(changes) != 1 || edit.Version.Source != "// updated\n" {
		t.Errorf("update facts = %+v %v", edit, changes)
	}
	poster, _ = getPoster(f.ctx, f.pool, f.tenant.ID, poster.ID)
	if !poster.Stale || *poster.SetVersionID != picked.ID || *poster.CurrentVersionID != edit.Version.ID {
		t.Errorf("poster after update facts = %+v", poster)
	}
}

func TestGenerateWithoutPhotosOffersTypeOnly(t *testing.T) {
	f := newFixture(t)
	ev := f.event(t)
	res, err := f.app.Generate(f.ctx, f.tenant.ID, f.user.ID, ev.ID, false, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !res.NoPhoto || f.fake.OptionsCalls[0].Hero != nil {
		t.Errorf("expected a photo-less batch, got %+v", f.fake.OptionsCalls[0].Hero)
	}
	if f.llm.calls != 1 {
		t.Errorf("model calls = %d, want copy only (no candidates, no pick)", f.llm.calls)
	}
}

func TestGenerateNeedsABrand(t *testing.T) {
	f := newFixture(t)
	if _, err := f.pool.Exec(f.ctx, "DELETE FROM skills WHERE tenant_id = $1", f.tenant.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.app.Generate(f.ctx, f.tenant.ID, f.user.ID, f.event(t).ID, false, nil)
	if err == nil || !strings.Contains(err.Error(), brandingSkill) {
		t.Fatalf("expected a missing-brand error naming the skill, got %v", err)
	}
}

func TestSaveEditRefusesProblems(t *testing.T) {
	f := newFixture(t)
	ev := f.event(t)
	res, err := f.app.Generate(f.ctx, f.tenant.ID, f.user.ID, ev.ID, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.fake.RenderFn = func(posterrender.RenderRequest) (*posterrender.RenderResponse, error) {
		return &posterrender.RenderResponse{Problems: []string{"<svg> is not allowed"}}, nil
	}
	poster, _ := getPoster(f.ctx, f.pool, f.tenant.ID, res.Poster.ID)
	before := *poster.CurrentVersionID
	edit, err := f.app.saveEdit(f.ctx, f.tenant.ID, poster, "bad source", "add a monkey", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if edit.Version != nil || len(edit.Problems) != 1 {
		t.Fatalf("edit = %+v", edit)
	}
	poster, _ = getPoster(f.ctx, f.pool, f.tenant.ID, poster.ID)
	if *poster.CurrentVersionID != before {
		t.Error("a failed render changed the current version")
	}
}
