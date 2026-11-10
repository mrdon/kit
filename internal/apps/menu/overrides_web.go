package menu

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// The Menu page's item list: what Untappd sends, what Kit adds, and what is
// hidden or 86'd, in one place. Reading it is for any member, as the 86 list
// is, because the list is where the 86 ticks live. Changing items or hiding
// is admin, and takes the same arguments as set_menu_items.

func registerItemsRoutes(mux apps.Mux, a *App) {
	mux.Handle("GET /{slug}/api/menu/items",
		console.RequireCap(a.pool, a.signer, auth.CapMenuEightySix, a.handleGetItems))
	mux.Handle("PUT /{slug}/api/menu/items",
		console.AdminJSON(a.pool, a.signer, a.handleSaveItems))
}

// menuItemRow is one line of the list.
type menuItemRow struct {
	Name   string `json:"name"`
	Source string `json:"source"` // "untappd" or "kit"
	Style  string `json:"style"`
	Price  string `json:"price"`
	Size   string `json:"size"`
	// Show is a Kit item's menus: print, board or both. Empty for Untappd.
	Show   string `json:"show"`
	Hidden bool   `json:"hidden"`
	Out    bool   `json:"out"`
	// Index is a Kit item's place in the stored list, so an edit can name it.
	Index int `json:"index"`
}

type menuSection struct {
	Name   string        `json:"name"`
	Hidden bool          `json:"hidden"`
	Items  []menuItemRow `json:"items"`
}

type menuItemsPayload struct {
	Sections []menuSection `json:"sections"`
	Edits    Edits         `json:"edits"`
}

// itemsPayload lists every row either menu could show, including the hidden
// and 86'd ones, so they can be brought back from the same list.
func (a *App) itemsPayload(ctx context.Context, tenantID uuid.UUID) (menuItemsPayload, error) {
	ov, err := a.loadOverrides(ctx, tenantID)
	if err != nil {
		return menuItemsPayload{}, err
	}
	var taps []Tap
	if row, err := GetBoard(ctx, a.pool, tenantID); err == nil && row != nil {
		if board, perr := ParseBoard(row.Payload); perr == nil {
			taps = board.Taps
		}
	}
	kit := make([]Tap, 0, len(ov.Items))
	index := map[string]int{}
	for i, e := range ov.Items {
		t := Tap{Section: e.Section, Name: e.Name, Style: e.Style}
		if pour, ok := e.Headline(); ok {
			t.Price, t.Size = pour.Price, boardPourSize(pour)
		}
		kit = append(kit, t)
		index[nameKey(e.Name)] = i
	}

	out := menuItemsPayload{Sections: []menuSection{}, Edits: ov.normalized()}
	for _, t := range mergeBoardItems(taps, kit) {
		if n := len(out.Sections); n == 0 || sectionKey(out.Sections[n-1].Name) != sectionKey(t.Section) {
			out.Sections = append(out.Sections, menuSection{
				Name:   t.Section,
				Hidden: ov.hidden(t.Section, ""),
				Items:  []menuItemRow{},
			})
		}
		row := menuItemRow{
			Name: t.Name, Source: "untappd", Style: t.Style, Price: t.Price, Size: t.SizeLabel(),
			Hidden: containsName(ov.Hidden.Items, t.Name), Out: containsName(ov.EightySix, t.Name),
			Index: -1,
		}
		if i, ok := index[nameKey(t.Name)]; ok && i < len(ov.Items) && ov.Items[i].Section == t.Section {
			row.Source, row.Show, row.Index = "kit", firstNonEmpty(ov.Items[i].Show, ShowBoth), i
		}
		s := &out.Sections[len(out.Sections)-1]
		s.Items = append(s.Items, row)
	}
	return out, nil
}

func (a *App) handleGetItems(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	out, err := a.itemsPayload(r.Context(), tenant.ID)
	if err != nil {
		slog.Error("loading menu items", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) handleSaveItems(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read the change: " + err.Error()})
		return
	}
	args, err := parseItemsArgs(raw)
	if err == nil {
		_, err = editItems(ctx, a.pool, a, tenant.ID, args)
	}
	if err != nil {
		if errors.Is(err, ErrPayloadInvalid) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		slog.Error("saving menu items", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	a.handleGetItems(w, r)
}
