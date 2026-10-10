package posters

import (
	"testing"

	"github.com/mrdon/kit/internal/posterrender"
)

func TestPhotoIndexLifecycle(t *testing.T) {
	f := newFixture(t)
	p, changed, err := upsertListedPhoto(f.ctx, f.pool, f.tenant.ID, "file-1", "m1", "vinyl", "DSC01.jpg")
	if err != nil || !changed || p.Status != PhotoPending {
		t.Fatalf("first listing: %v %v %+v", err, changed, p)
	}
	if err := setPhotoFacts(f.ctx, f.pool, f.tenant.ID, p.ID, 1600, 1200, "landscape", "none"); err != nil {
		t.Fatal(err)
	}
	// Same file again: nothing changed, no re-inspect.
	if _, changed, _ = upsertListedPhoto(f.ctx, f.pool, f.tenant.ID, "file-1", "m1", "vinyl", "DSC01.jpg"); changed {
		t.Error("an unchanged listing asked for re-inspection")
	}
	// Pending photos are not searchable.
	if hits, _ := searchPhotos(f.ctx, f.pool, f.tenant.ID, "vinyl", 10); len(hits) != 0 {
		t.Errorf("pending photo was offered: %d hits", len(hits))
	}
	if _, err := indexPhoto(f.ctx, f.pool, f.tenant.ID, IndexEntry{ID: p.ID, Description: "A turntable on the bar", Tags: []string{"vinyl", "bar"}, FocusX: 0.7, FocusY: 0.3}, "tester"); err != nil {
		t.Fatal(err)
	}
	hits, err := searchPhotos(f.ctx, f.pool, f.tenant.ID, "turntable", 10)
	if err != nil || len(hits) != 1 || hits[0].FocusX < 0.69 || hits[0].IndexedBy != "tester" {
		t.Fatalf("search after index: %v %+v", err, hits)
	}
	// The folder name is searchable too (it is the set).
	if hits, _ := searchPhotos(f.ctx, f.pool, f.tenant.ID, "vinyl", 10); len(hits) != 1 {
		t.Errorf("folder word not searchable: %d hits", len(hits))
	}
	// A photo flagged as generated is indexed but never offered.
	if err := setPhotoFacts(f.ctx, f.pool, f.tenant.ID, p.ID, 1600, 1200, "landscape", "ai"); err != nil {
		t.Fatal(err)
	}
	if hits, _ := searchPhotos(f.ctx, f.pool, f.tenant.ID, "turntable", 10); len(hits) != 0 {
		t.Error("an ai-flagged photo was offered")
	}
	// Gone from Drive: removed, but kept.
	n, err := markRemovedExcept(f.ctx, f.pool, f.tenant.ID, []string{"other"})
	if err != nil || n != 1 {
		t.Fatalf("mark removed: %v %d", err, n)
	}
	got, _ := getPhoto(f.ctx, f.pool, f.tenant.ID, p.ID)
	if got.Status != PhotoRemoved {
		t.Errorf("status = %s", got.Status)
	}
	// Back in Drive: pending again (facts may have changed), description kept.
	p2, _, _ := upsertListedPhoto(f.ctx, f.pool, f.tenant.ID, "file-1", "m2", "vinyl", "DSC01.jpg")
	if p2.Status != PhotoPending || p2.Description == "" {
		t.Errorf("relisted = %+v", p2)
	}
	counts, _ := photoCounts(f.ctx, f.pool, f.tenant.ID)
	if counts[PhotoPending] != 1 {
		t.Errorf("counts = %v", counts)
	}
}

func TestTemplatesBuiltinsAndTenantCopies(t *testing.T) {
	f := newFixture(t)
	ts, err := listTemplates(f.ctx, f.pool, f.tenant.ID, TemplateActive)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) < 7 {
		t.Fatalf("active templates = %d, want the seven built-ins", len(ts))
	}
	var band *Template
	for i := range ts {
		if ts[i].BuiltinKey == "band" {
			band = &ts[i]
		}
	}
	if band == nil || !band.Builtin() || band.CurrentVersionID == nil {
		t.Fatalf("band built-in = %+v", band)
	}
	// Installing again adds no version when the source is unchanged.
	f.app.installBuiltins(f.ctx)
	vs, _ := listTemplateVersions(f.ctx, f.pool, f.tenant.ID, band.ID)
	if len(vs) != 1 {
		t.Errorf("built-in versions after reinstall = %d", len(vs))
	}
	// Built-ins cannot be versioned by a tenant.
	if _, err := addTemplateVersion(f.ctx, f.pool, f.tenant.ID, band.ID, "x", "s", "agent", band.Meta, nil); err == nil {
		t.Error("tenant edit of a built-in was accepted")
	}
	// Hiding a built-in is per tenant.
	if err := f.app.SetTemplateStatus(f.ctx, f.tenant.ID, band.ID, "hidden"); err != nil {
		t.Fatal(err)
	}
	got, _ := getTemplate(f.ctx, f.pool, f.tenant.ID, band.ID)
	if !got.Hidden {
		t.Error("built-in not hidden")
	}
	planned, _, err := f.app.activeTemplates(f.ctx, f.tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range planned {
		if p.ID == band.ID.String() {
			t.Error("hidden built-in still offered to the generator")
		}
	}
	// A tenant copy starts as a draft with its own version chain.
	meta := posterrender.TemplateMeta{Name: "Band copy", Description: "d", Needs: []string{"title"}}
	meta.Photos.Min, meta.Photos.Max = 1, 1
	copyT, err := createTemplate(f.ctx, f.pool, f.tenant.ID, TemplateInput{Name: "Band copy", Origin: "chat", Meta: meta, Source: "v1", Summary: "Duplicated", Author: "user", ParentTemplateID: &band.ID})
	if err != nil {
		t.Fatal(err)
	}
	if copyT.Status != TemplateDraft || copyT.Builtin() {
		t.Errorf("copy = %+v", copyT)
	}
	v2, err := addTemplateVersion(f.ctx, f.pool, f.tenant.ID, copyT.ID, "v2", "Bigger title", "agent", meta, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = getTemplate(f.ctx, f.pool, f.tenant.ID, copyT.ID)
	if *got.CurrentVersionID != v2.ID || v2.ParentID == nil {
		t.Errorf("after version: %+v %+v", got, v2)
	}
	if err := f.app.SetTemplateStatus(f.ctx, f.tenant.ID, copyT.ID, "active"); err != nil {
		t.Fatal(err)
	}
	if err := f.app.SetTemplateStatus(f.ctx, f.tenant.ID, copyT.ID, "bogus"); err == nil {
		t.Error("bogus status accepted")
	}
}
