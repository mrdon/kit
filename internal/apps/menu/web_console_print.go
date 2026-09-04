package menu

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// The printed menu's settings, for the console.
//
// Admin-only, unlike the board's read endpoint. That one answers "what URL do
// I paste into the screen?", which anyone setting up a kiosk needs; this one
// changes the prices and wording a room full of customers reads off a table,
// which is not a note-taking action.
//
// These write the same PrintConfig the set_menu_print tool does, through the
// same validation, so the console and the agent cannot disagree about what a
// valid configuration is.

func registerPrintConsoleRoutes(mux apps.Mux, a *App) {
	adminRoute := func(h http.HandlerFunc) http.Handler {
		return console.AdminJSON(a.pool, a.signer, h)
	}
	mux.Handle("GET /{slug}/api/menu/print", adminRoute(a.handleGetPrintConfig))
	mux.Handle("PUT /{slug}/api/menu/print", adminRoute(a.handleSavePrintConfig))
}

// printConfigPayload is the wire shape.
//
// It carries the sections currently on the board alongside the saved config,
// so the colour editor can offer the headings that actually exist rather than
// asking an admin to type them from memory and silently doing nothing when
// they typo one.
type printConfigPayload struct {
	Config PrintConfig `json:"config"`

	// Sections are the headings on the tap list right now, in board order.
	Sections []string `json:"sections"`
	// Palette is the house colour cycle, offered as swatches.
	Palette []string `json:"palette"`
	// NotesCached is how many descriptions have been fetched and stored.
	NotesCached int `json:"notes_cached"`
	// PrintURL is where the sheet lives, so the page can link to it.
	PrintURL string `json:"print_url"`
}

// MarshalJSON guarantees the slice and map fields serialise as `[]` and `{}`
// rather than `null`. A nil Go slice marshals to `null`, and a client then
// doing `.map(...)` on it dies on a type error and takes the page down -- the
// first-run state, with nothing configured, is exactly where that would bite.
func (p printConfigPayload) MarshalJSON() ([]byte, error) {
	type alias printConfigPayload // shed the method, or this recurses
	if p.Sections == nil {
		p.Sections = []string{}
	}
	if p.Palette == nil {
		p.Palette = []string{}
	}
	if p.Config.Extras == nil {
		p.Config.Extras = []Beer{}
	}
	if p.Config.Colors == nil {
		p.Config.Colors = map[string]string{}
	}
	if p.Config.Notes == nil {
		p.Config.Notes = map[string]string{}
	}
	if p.Config.Blurbs == nil {
		p.Config.Blurbs = map[string]string{}
	}
	return json.Marshal(alias(p))
}

func (a *App) handleGetPrintConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)

	state, err := LoadPrintState(ctx, a.pool, tenant.ID)
	if err != nil {
		slog.Error("loading print config", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	out := printConfigPayload{
		Config:      state.Config,
		Palette:     printPalette,
		NotesCached: len(state.Notes),
		PrintURL:    a.baseURL + "/" + tenant.Slug + "/menu/print.pdf",
		Sections:    a.boardSections(ctx, tenant.ID, state.Config),
	}
	writeJSON(w, http.StatusOK, out)
}

// boardSections lists the headings on the stored tap list, in board order.
//
// It reads the saved board rather than pulling Untappd: this is a hint for a
// colour picker, and making an admin wait on a third party to see their own
// settings would be a poor trade. A workspace with no board yet simply gets no
// suggestions, and any heading can still be typed by hand.
//
// The configured sections are folded in too, so a colour set for a beer that
// has since gone off tap stays visible and editable rather than vanishing from
// the page while remaining in the stored config. That includes headings that
// exist only as a blurb -- snacks have no tap and still want a bar colour.
func (a *App) boardSections(ctx context.Context, tenantID uuid.UUID, cfg PrintConfig) []string {
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, name)
	}

	if row, err := GetBoard(ctx, a.pool, tenantID); err == nil && row != nil {
		if board, perr := ParseBoard(row.Payload); perr == nil {
			for _, t := range board.Taps {
				add(t.Section)
			}
		}
	}
	for _, e := range cfg.Extras {
		add(e.Section)
	}
	// The config maps have no order of their own. Sorted, or the list
	// reshuffles between page loads for no reason the reader can see.
	named := make([]string, 0, len(cfg.Colors)+len(cfg.Blurbs))
	for name := range cfg.Colors {
		named = append(named, name)
	}
	for name := range cfg.Blurbs {
		named = append(named, name)
	}
	sort.Strings(named)
	for _, name := range named {
		add(name)
	}
	return out
}

func (a *App) handleSavePrintConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)

	// Unknown fields are rejected for the same reason the tool rejects them: a
	// stale client sending a field this build does not know about should fail
	// loudly rather than have its setting silently dropped.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	var cfg PrintConfig
	if err := dec.Decode(&cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := SavePrintConfig(ctx, a.pool, tenant.ID, cfg); err != nil {
		slog.Error("saving print config", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	a.handleGetPrintConfig(w, r)
}
