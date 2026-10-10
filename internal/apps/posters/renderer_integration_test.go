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
	r, stop, err := posterrender.Start(ctx, posterrender.Config{Dir: dir, Port: 8497, CacheDir: t.TempDir()})
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
	var typeT BuiltinTemplate
	for _, b := range bs {
		if b.Key == "type" {
			typeT = b
		}
	}
	content := posterrender.Content{Eyebrow: "Every Wednesday", Title: "Trivia night", Summary: "Free to play, no sign-up.", Details: []posterrender.Detail{{Label: "When", Value: "6:30 to 8:30pm"}}}
	res, err := r.Render(ctx, posterrender.RenderRequest{
		Kind: "template", Source: typeT.Source, Format: brand.PortraitFormat, Brand: *brand, Photos: []posterrender.ImageRef{},
		Props: &posterrender.TemplateProps{Content: content, Photos: []posterrender.PhotoUse{}, Ground: "paper", Accent: "amber"},
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
	if res.Meta == nil || res.Meta.Name != "Type" {
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
		if b.Meta.Photos.Max == 0 {
			templates = append(templates, posterrender.PlannedTemplate{ID: b.Key, Source: b.Source, Weight: 1, Meta: b.Meta})
		}
	}
	opts, err := r.Options(ctx, posterrender.OptionsRequest{Content: content, Brand: *brand, Format: brand.PortraitFormat, Photos: []posterrender.ImageRef{}, Templates: templates, Count: 4})
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
