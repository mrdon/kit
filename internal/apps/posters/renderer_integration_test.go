package posters

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrdon/kit/internal/posterrender"
)

// The real renderer, supervised the way kit runs it, rendering a built-in
// template with the brand derived from the Gravity guide. This is the one
// place the Go and Node sides of the wire contract meet, so field names,
// base64 bytes and the problems/warnings shapes are all exercised. Skipped
// when the renderer is not built (make renderer-build) or node is absent.
func TestRendererEndToEnd(t *testing.T) {
	dir, err := filepath.Abs("../../../renderer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dist", "server.js")); err != nil {
		t.Skip("renderer not built; run make renderer-build")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}
	// Offline fonts, a scratch cache, and a port unlikely to collide.
	t.Setenv("POSTER_FONT_DIR", filepath.Join(dir, "test", "fonts"))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r, stop, err := posterrender.Start(ctx, posterrender.Config{Dir: dir, Node: os.Getenv("POSTER_RENDERER_NODE"), Port: 8497, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if !r.Healthy(ctx) {
		t.Fatal("renderer did not become healthy")
	}

	brand, problems := deriveBrand(loadGravityTokens(t), "e2e")
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	brand.Logos = map[string]posterrender.ImageRef{}
	bs, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	var captionT BuiltinTemplate
	for _, b := range bs {
		if b.Key == "caption" {
			captionT = b
		}
	}
	// Every layout takes a photo; three inline ones in one folder stand in
	// for the library.
	var photos []posterrender.ImageRef
	for _, id := range []string{"p1", "p2", "p3"} {
		photos = append(photos, posterrender.ImageRef{ID: id, Source: posterrender.SourceInline, Data: base64Encode(posterrender.PNG1x1), Modified: "1", Folder: "set"})
	}
	hero := posterrender.PhotoUse{ID: "p1", FocusX: 0.5, FocusY: 0.5, Zoom: 1}
	content := posterrender.Content{Eyebrow: "Every Wednesday", Title: "Trivia night", Summary: "Free to play, no sign-up.", Details: []posterrender.Detail{{Label: "When", Value: "6:30 to 8:30pm"}}}
	res, err := r.Render(ctx, posterrender.RenderRequest{
		Kind: "template", Source: captionT.Source, Format: brand.PortraitFormat, Brand: *brand, Photos: photos,
		Props: &posterrender.TemplateProps{Content: content, Photos: []posterrender.PhotoUse{hero}, Ground: "paper", Accent: "amber"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(res.Problems) != 0 {
		t.Fatalf("problems: %v", res.Problems)
	}
	if len(res.PNG) < 1000 || string(res.PNG[1:4]) != "PNG" {
		t.Fatalf("png = %d bytes", len(res.PNG))
	}
	if res.Meta == nil || res.Meta.Name != "Caption" {
		t.Errorf("meta = %+v", res.Meta)
	}

	// The copy rules, including the brand's own banned words.
	brand.BannedWords = []string{"hoppy hour"}
	check, err := r.CopyCheck(ctx, posterrender.Content{Eyebrow: "Fri", Title: "Hoppy hour — don't miss it!", Details: []posterrender.Detail{}}, brand)
	if err != nil {
		t.Fatal(err)
	}
	if len(check.Problems) < 3 {
		t.Errorf("copy check problems = %v", check.Problems)
	}

	// A refusal comes back as a RequestError the app turns into problems.
	_, err = r.Render(ctx, posterrender.RenderRequest{Kind: "poster", Source: `import fs from "node:fs"; export default () => null;`, Format: brand.PortraitFormat, Brand: *brand, Photos: []posterrender.ImageRef{}})
	var rerr *posterrender.RequestError
	if !errorsAs(err, &rerr) {
		t.Fatalf("expected a RequestError, got %v", err)
	}

	// Options over the real generator: seven type-only candidates exist
	// (type and when, two grounds each) so we ask for four.
	var templates []posterrender.PlannedTemplate
	for _, b := range bs {
		templates = append(templates, posterrender.PlannedTemplate{ID: b.Key, Source: b.Source, Weight: 1, Meta: b.Meta})
	}
	// Nil lists are what Kit sends for a type-only event (no hero, no
	// details); the client must not hand the renderer JSON null for them.
	bare := content
	bare.Details = nil
	if _, err := r.Options(ctx, posterrender.OptionsRequest{Content: bare, Brand: *brand, Format: brand.PortraitFormat, Templates: templates, Photos: photos, Hero: &hero, Count: 1}); err != nil {
		t.Fatalf("options with nil lists: %v", err)
	}
	opts, err := r.Options(ctx, posterrender.OptionsRequest{Content: content, Brand: *brand, Format: brand.PortraitFormat, Photos: photos, Hero: &hero, Templates: templates, Count: 4})
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if len(opts.Options) != 4 {
		t.Fatalf("options = %d (%v)", len(opts.Options), opts.Skipped)
	}
	if _, _, _, ok := readInlined(opts.Options[0].Source); !ok {
		t.Error("option source does not carry the inlined block set_poster_fields relies on")
	}
}
