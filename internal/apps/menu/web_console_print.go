package menu

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

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
	mux.Handle("POST /{slug}/api/menu/print/sync", adminRoute(a.handleSyncPrint))
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

	// Beers is the synced tap list, so the page can show what will print and
	// let somebody write the copy for it. Descriptions are resolved here --
	// hand-written over cached -- so the page shows what the paper will say
	// rather than making the client re-derive the precedence.
	Beers []printBeer `json:"beers"`
	// SyncedAt is null until the first sync, which is the state that makes the
	// page lead with the button rather than the form.
	SyncedAt *time.Time `json:"synced_at"`
	// SyncError is the last sync's warning, normally about descriptions.
	SyncError string `json:"sync_error"`
}

// printBeer is one row as the settings page needs it: enough to recognise the
// beer, plus the description and where it came from. The facts are read-only
// on this page -- they follow the board -- so they travel as display strings
// rather than as something to edit.
type printBeer struct {
	Section string `json:"section"`
	Name    string `json:"name"`
	Style   string `json:"style"`
	ABV     string `json:"abv"`
	Price   string `json:"price"`
	Note    string `json:"note"`
	// Written is true when the description was typed here rather than scraped,
	// so the page can show which copy is yours and which came from Untappd.
	Written bool `json:"written"`
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
	if p.Beers == nil {
		p.Beers = []printBeer{}
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

	writeJSON(w, http.StatusOK, a.printPayload(ctx, tenant.Slug, tenant.ID, state))
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

// printPayload assembles the settings page's whole state from one load, so a
// GET and the response to a save or a sync cannot disagree about what is
// stored.
func (a *App) printPayload(ctx context.Context, slug string, tenantID uuid.UUID, state *PrintState) printConfigPayload {
	return printConfigPayload{
		Config:      state.Config,
		Palette:     printPalette,
		NotesCached: len(state.Notes),
		PrintURL:    a.baseURL + "/" + slug + "/menu/print.pdf",
		Sections:    a.boardSections(ctx, tenantID, state.Config),
		Beers:       printBeers(state),
		SyncedAt:    state.SyncedAt,
		SyncError:   state.SyncError,
	}
}

// printBeers resolves each synced row to what the paper will actually say.
func printBeers(state *PrintState) []printBeer {
	out := make([]printBeer, 0, len(state.Rows))
	for _, r := range state.Rows {
		b := printBeer{
			Section: r.Section,
			Name:    r.Name,
			Style:   tidyStyle(r.Style),
			ABV:     tidyABV(r.ABV),
			Note:    state.Notes[normalizeBeerName(r.Name)],
		}
		if pour, ok := r.Headline(); ok {
			b.Price = money(pour.Price)
		}
		// Hand-written wins, and says so -- the page marks it, because "this
		// is your copy, Untappd will not overwrite it" is the whole reason the
		// layer exists.
		if written := lookupFold(state.Config.Notes, r.Name); written != "" {
			b.Note, b.Written = written, true
		}
		out = append(out, b)
	}
	return out
}

// syncResponse carries the new state alongside what the sync did.
//
// The state is a named field rather than an embedded one on purpose.
// printConfigPayload has its own MarshalJSON, and embedding promotes that
// method to the outer struct -- encoding/json would then call it and emit the
// payload alone, silently dropping the report and the summary. A field cannot
// do that.
type syncResponse struct {
	State   printConfigPayload `json:"state"`
	Report  SyncReport         `json:"report"`
	Summary string             `json:"summary"`
}

// handleSyncPrint refreshes the tap list from Untappd on request.
//
// A sync that cannot reach the description pages is a success with a warning,
// not a failure, so the report comes back on a 200 alongside the new state and
// the page shows both. Only losing the board itself is an error, because
// without it there is no menu to show.
func (a *App) handleSyncPrint(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)

	rep, err := SyncPrintMenu(ctx, a.pool, tenant.ID)
	if err != nil {
		slog.Warn("menu print: sync", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	state, err := LoadPrintState(ctx, a.pool, tenant.ID)
	if err != nil {
		slog.Error("loading print config after sync", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, syncResponse{
		State:   a.printPayload(ctx, tenant.Slug, tenant.ID, state),
		Report:  rep,
		Summary: rep.Summary(),
	})
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
