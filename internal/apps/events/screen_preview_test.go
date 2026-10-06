package events

import (
	"encoding/json"
	"html/template"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWriteScreenPreview renders the wall screen to KIT_SCREEN_OUT from a JSON
// array of events (the Event json shape) at KIT_SCREEN_EVENTS, for eyeballing
// the slides in a browser. Posters are read from KIT_SCREEN_POSTERS/<attachment
// id> and the logo from KIT_SCREEN_LOGO, both optional. Scaffolding; skipped
// in CI.
func TestWriteScreenPreview(t *testing.T) {
	out, src := os.Getenv("KIT_SCREEN_OUT"), os.Getenv("KIT_SCREEN_EVENTS")
	if out == "" || src == "" {
		t.Skip("set KIT_SCREEN_OUT and KIT_SCREEN_EVENTS to render a preview")
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	if err := json.Unmarshal(raw, &events); err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation(DefaultTimezone)
	if err != nil {
		t.Fatal(err)
	}
	s := composeScreen(events, time.Now().In(loc), "Gravity Brewing", "thegravitybrewing.com")

	posters := map[string]template.URL{}
	if dir := os.Getenv("KIT_SCREEN_POSTERS"); dir != "" {
		for _, id := range s.posterIDs() {
			raw, err := os.ReadFile(filepath.Join(dir, id.String()))
			if err != nil {
				continue
			}
			if small, err := screenPoster(raw); err == nil {
				posters[id.String()] = imageDataURI(small, "image/jpeg")
			}
		}
	}
	var logo []byte
	if p := os.Getenv("KIT_SCREEN_LOGO"); p != "" {
		if logo, err = os.ReadFile(p); err != nil {
			t.Fatal(err)
		}
	}
	page, err := renderScreen(s, screenVersion(s, logo), posters, logo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, page, 0o600); err != nil {
		t.Fatal(err)
	}
}
