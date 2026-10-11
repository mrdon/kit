package posters

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

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
	var band *Template // the badge built-in
	for i := range ts {
		if ts[i].BuiltinKey == "badge" {
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

// The hourly Drive sync is opt-in: a photo folder alone must not create
// the scheduled row, only the folder plus the auto-sync switch.
func TestAutoSyncIsOptIn(t *testing.T) {
	f := newFixture(t)
	if f.app.autoSyncOn(f.ctx, f.tenant.ID) {
		t.Fatal("auto sync on with no settings row")
	}
	s := Settings{TenantID: f.tenant.ID, PhotoFolderID: "folder123456", LogoMap: map[string]LogoFile{}}
	if _, err := upsertSettings(f.ctx, f.pool, s); err != nil {
		t.Fatal(err)
	}
	if f.app.autoSyncOn(f.ctx, f.tenant.ID) {
		t.Fatal("auto sync on with a folder but the switch off")
	}
	s.AutoSync = true
	saved, err := upsertSettings(f.ctx, f.pool, s)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.AutoSync {
		t.Fatal("auto_sync did not round-trip")
	}
	if !f.app.autoSyncOn(f.ctx, f.tenant.ID) {
		t.Fatal("auto sync off with a folder and the switch on")
	}
	s.PhotoFolderID = ""
	if _, err := upsertSettings(f.ctx, f.pool, s); err != nil {
		t.Fatal(err)
	}
	if f.app.autoSyncOn(f.ctx, f.tenant.ID) {
		t.Fatal("auto sync on with the switch on but no folder")
	}
}

// The renderer fetches Drive files by their Drive id, and no reference that
// can reach a browser carries a download URL (which would hold the server's
// Drive key).
func TestImageRefsCarryDriveFileIDNotURL(t *testing.T) {
	p := Photo{ID: uuid.New(), DriveFileID: "1abcDEF", DriveModifiedAt: "2026-01-01T00:00:00Z", Folder: "taproom"}
	ref := p.Ref()
	if ref.FileID != "1abcDEF" || ref.ID != p.ID.String() || ref.URL != "" {
		t.Fatalf("photo ref = %+v", ref)
	}
	a := &App{driveKey: "SECRET-KEY"}
	var brand posterrender.Brand
	a.attachLogos(&brand, Settings{LogoMap: map[string]LogoFile{"white": {FileID: "logoFile1"}}})
	logo := brand.Logos["white"]
	if logo.FileID != "logoFile1" || logo.URL != "" {
		t.Fatalf("logo ref = %+v", logo)
	}
	if raw, _ := json.Marshal(brand); strings.Contains(string(raw), "SECRET-KEY") {
		t.Fatal("the Drive key is in the brand JSON")
	}
}

// One listing records every file in a batch: new files start unmeasured
// unless Drive already reported their size, a changed file is measured
// again, and the page helpers count and slice without loading the library.
func TestRecordListedPhotosBatch(t *testing.T) {
	f := newFixture(t)
	files := []DriveFile{
		{ID: "a", Name: "a.jpg", Folder: "patio", Modified: "m1", Width: 4000, Height: 3000},
		{ID: "b", Name: "b.jpg", Folder: "patio", Modified: "m1"},
		{ID: "c", Name: "c.jpg", Folder: "", Modified: "m1"},
	}
	n, err := recordListedPhotos(f.ctx, f.pool, f.tenant.ID, files)
	if err != nil || n != 3 {
		t.Fatalf("record: %v new=%d", err, n)
	}
	a, _ := getPhotoByFileID(f.ctx, f.pool, f.tenant.ID, "a")
	if a.Width != 4000 || a.Orientation != "landscape" || a.C2PA != posterrender.C2PAUnchecked {
		t.Fatalf("drive dimensions not stored: %+v", a)
	}
	un, _ := listUnmeasured(f.ctx, f.pool, f.tenant.ID, 10)
	if len(un) != 2 {
		t.Fatalf("unmeasured = %d, want b and c", len(un))
	}
	// Same listing again: nothing new, nothing reset.
	if n, _ = recordListedPhotos(f.ctx, f.pool, f.tenant.ID, files); n != 0 {
		t.Fatalf("relisting counted %d new", n)
	}
	// a changed on Drive without dimensions this time: measured again.
	files[0] = DriveFile{ID: "a", Name: "a.jpg", Folder: "patio", Modified: "m2"}
	if _, err := recordListedPhotos(f.ctx, f.pool, f.tenant.ID, files); err != nil {
		t.Fatal(err)
	}
	if c, _ := countUnmeasured(f.ctx, f.pool, f.tenant.ID); c != 3 {
		t.Fatalf("unmeasured after change = %d, want 3", c)
	}
	total, _ := countPhotos(f.ctx, f.pool, f.tenant.ID, PhotoFilter{Folder: "patio"})
	page, _ := listPhotos(f.ctx, f.pool, f.tenant.ID, PhotoFilter{Folder: "patio", Limit: 1, Offset: 1})
	folders, _ := listFolders(f.ctx, f.pool, f.tenant.ID)
	if total != 2 || len(page) != 1 || page[0].Filename != "b.jpg" || len(folders) != 2 {
		t.Fatalf("paging: total=%d page=%+v folders=%v", total, page, folders)
	}
}

// An uploaded file is offered to the renderer as a logo variant too, under
// its attachment id, without touching the brand's own logos.
func TestWithAttachmentLogos(t *testing.T) {
	brand := posterrender.Brand{Logos: map[string]posterrender.ImageRef{"white": {ID: "logo:w"}}}
	refs := []posterrender.ImageRef{{ID: "attachment:abc", Source: posterrender.SourceInline}, {ID: "11111111-1111-1111-1111-111111111111", Source: posterrender.SourceDrive}}
	out := withAttachmentLogos(brand, refs)
	if len(out.Logos) != 2 || out.Logos["attachment:abc"].ID != "attachment:abc" || out.Logos["white"].ID != "logo:w" {
		t.Fatalf("logos = %+v", out.Logos)
	}
	if len(brand.Logos) != 1 {
		t.Fatal("the brand's own logo map was changed")
	}
}
