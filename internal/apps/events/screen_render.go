package events

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
	xdraw "golang.org/x/image/draw"

	"github.com/mrdon/kit/internal/attachment"
)

// Rendering the screen.
//
// Like the menu board, the page ships as ONE self-contained document: reveal.js,
// the stylesheet, the fonts and every poster are inlined. It hangs on a wall
// with nobody watching it, and a page that needs a second request at paint time
// is a page that shows a broken image on the night the Wi-Fi hiccups.
//
// reveal.js is vendored rather than loaded from a CDN for the same reason. It
// owns the stage (a fixed 1920x1080 canvas scaled to the screen), the loop and
// the slide transitions; screen.js owns the pacing and the in-slide
// choreography, which is the part a presentation framework leaves to a
// presenter's clicker.

//go:embed screen/screen.html.tmpl screen/screen.css screen/screen.js screen/vendor/*
var screenFS embed.FS

var screenTmpl = template.Must(template.New("screen.html.tmpl").Funcs(template.FuncMap{
	// plus offsets a stagger index past the slide's own heading lines.
	"plus": func(a, b int) int { return a + b },
	"odd":  func(i int) bool { return i%2 == 1 },
	// seq is 0..n-1, for the featured pager's dots.
	"seq": func(n int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	},
}).ParseFS(screenFS, "screen/screen.html.tmpl"))

// screenPosterPx bounds a poster's long side. The featured slide shows one
// about 760px tall; anything bigger is bytes a TV stick has to decode for
// nothing, and a 4000px upload stutters the transition it arrives in on.
const screenPosterPx = 960

const screenPosterQuality = 80

// screenGround is what a transparent poster is flattened onto: the slide's
// own navy, so a logo on transparency sits on the page rather than in a white
// box.
var screenGround = color.RGBA{R: 0x14, G: 0x22, B: 0x2d, A: 0xff}

// screenStamp fingerprints the template, stylesheet, script and vendored
// library, so a deploy that changes only the look still reloads every screen
// already on the wall. See menu.RenderStamp for the day that was learned.
var screenStamp = sync.OnceValue(func() string {
	h := sha256.New()
	err := fs.WalkDir(screenFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(screenFS, path)
		if err != nil {
			return err
		}
		h.Write([]byte(path))
		h.Write(raw)
		return nil
	})
	if err != nil {
		panic(fmt.Errorf("stamping events screen assets: %w", err))
	}
	return hex.EncodeToString(h.Sum(nil))[:8]
})

// screenVersion is what the page polls. It hashes what the slides say --
// including today's date, which is how the screen rolls over to the next
// events every night without a job to tell it to -- plus the render stamp.
func screenVersion(s *Screen, logo []byte) string {
	raw, err := json.Marshal(s)
	if err != nil {
		// Only a type that cannot marshal gets here, which is a build-time
		// mistake; a stamp that never changes would pin screens silently.
		panic(fmt.Errorf("stamping events screen: %w", err))
	}
	h := sha256.New()
	h.Write(raw)
	h.Write(logo)
	return hex.EncodeToString(h.Sum(nil))[:12] + "." + screenStamp()
}

// screenView is what the template sees.
type screenView struct {
	*Screen
	Version string
	Posters map[string]template.URL
	Logo    template.URL
	CSS     template.CSS
	Reveal  template.JS
	Script  template.JS
}

// renderScreen produces the whole page. posters maps attachment id to a data
// URI; a card whose poster is missing renders its date block instead.
func renderScreen(s *Screen, version string, posters map[string]template.URL, logo []byte) ([]byte, error) {
	css, err := screenCSS()
	if err != nil {
		return nil, err
	}
	revealJS, err := screenFS.ReadFile("screen/vendor/reveal-6.0.2.js")
	if err != nil {
		return nil, fmt.Errorf("reading reveal.js: %w", err)
	}
	script, err := screenFS.ReadFile("screen/screen.js")
	if err != nil {
		return nil, fmt.Errorf("reading screen.js: %w", err)
	}
	view := screenView{
		Screen:  s,
		Version: version,
		Posters: posters,
		CSS:     template.CSS(css),      //nolint:gosec // embedded at build time, not user input
		Reveal:  template.JS(revealJS),  //nolint:gosec // vendored library, embedded at build time
		Script:  template.JS(script),    //nolint:gosec // embedded at build time, not user input
		Logo:    imageDataURI(logo, ""), //nolint:gosec // the tenant's own icon, sniffed to an image type
	}
	var buf bytes.Buffer
	if err := screenTmpl.ExecuteTemplate(&buf, "screen.html.tmpl", view); err != nil {
		return nil, fmt.Errorf("rendering events screen: %w", err)
	}
	return buf.Bytes(), nil
}

// screenCSS is reveal's base stylesheet, the two faces the topper already
// prints with, and the screen's own rules, in that order so ours win.
func screenCSS() (string, error) {
	var b strings.Builder
	for _, f := range []struct{ family, file string }{
		{"anton", "fonts/Anton-Regular.ttf"},
		{"barlow", "fonts/BarlowCondensed-SemiBold.ttf"},
	} {
		raw, err := topperFonts.ReadFile(f.file)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", f.file, err)
		}
		fmt.Fprintf(&b, "@font-face{font-family:%s;src:url(data:font/ttf;base64,%s) format(\"truetype\");font-display:block}\n",
			f.family, base64.StdEncoding.EncodeToString(raw))
	}
	for _, name := range []string{"screen/vendor/reveal-6.0.2.css", "screen/screen.css"} {
		raw, err := screenFS.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", name, err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// screenPosters loads every poster the slides use, once each.
//
// Failures cost that poster, never the page: a card without artwork falls back
// to its date block, which is a design rather than a hole.
func (a *App) screenPosters(ctx context.Context, tenantID uuid.UUID, s *Screen) map[string]template.URL {
	out := map[string]template.URL{}
	store := a.attachments()
	if store == nil {
		return out
	}
	for _, id := range s.posterIDs() {
		if _, seen := out[id.String()]; seen {
			continue
		}
		if uri := loadScreenPoster(ctx, store, tenantID, id); uri != "" {
			out[id.String()] = uri
		}
	}
	return out
}

func (s *Screen) posterIDs() []uuid.UUID {
	var ids []uuid.UUID
	add := func(id *uuid.UUID) {
		if id != nil {
			ids = append(ids, *id)
		}
	}
	for _, list := range [][]screenCard{s.Overview, s.Featured, s.Soon} {
		for _, c := range list {
			add(c.posterID)
		}
	}
	for _, d := range s.Week {
		add(d.posterID)
	}
	return ids
}

func loadScreenPoster(ctx context.Context, store *attachment.Service, tenantID, id uuid.UUID) template.URL {
	meta, raw, err := store.Load(ctx, tenantID, id)
	if err != nil {
		slog.Warn("events screen: loading poster", "attachment_id", id, "error", err)
		return ""
	}
	if _, ok := allowedPosterMIME[meta.Mime]; !ok {
		return ""
	}
	small, err := screenPoster(raw)
	if err != nil {
		slog.Warn("events screen: decoding poster", "attachment_id", id, "mime", meta.Mime, "error", err)
		return ""
	}
	return imageDataURI(small, "image/jpeg")
}

// screenPoster fits a poster inside screenPosterPx, keeping its shape -- the
// featured slide frames the whole poster, unlike the topper's round crop --
// and flattens it onto the slide's navy.
func screenPoster(raw []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decoding poster: %w", err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, errors.New("poster has no pixels")
	}
	if long := max(w, h); long > screenPosterPx {
		w, h = max(1, w*screenPosterPx/long), max(1, h*screenPosterPx/long)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(screenGround), image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: screenPosterQuality}); err != nil {
		return nil, fmt.Errorf("encoding screen poster: %w", err)
	}
	return buf.Bytes(), nil
}

// imageDataURI wraps image bytes for an <img src>. The type is sniffed when
// not given, and anything that does not sniff as a raster image is refused:
// the tenant icon arrives from Slack, and an SVG in a data URI can carry
// script.
func imageDataURI(raw []byte, mime string) template.URL {
	if len(raw) == 0 {
		return ""
	}
	if mime == "" {
		mime = http.DetectContentType(raw)
	}
	if _, ok := allowedPosterMIME[mime]; !ok {
		return ""
	}
	return template.URL("data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)) //nolint:gosec // raster type checked above
}
