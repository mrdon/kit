package menu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

// Where a Kit item shows. There is one menu config, and both the wall and the
// paper read it; this is its only per-menu knob, because the wall has less
// room: paper can list every non-alcoholic can by name while the wall shows
// one line for all of them. Empty means both.
const (
	ShowPrint = "print"
	ShowBoard = "board"
	ShowBoth  = "both"
)

func validShow(s string) bool {
	switch s {
	case "", ShowPrint, ShowBoard, ShowBoth:
		return true
	}
	return false
}

func (b Beer) onPrint() bool { return b.Show != ShowBoard }
func (b Beer) onBoard() bool { return b.Show != ShowPrint }

// validateItems rejects what would otherwise fail quietly: a show nobody will
// see the effect of, or a row with no name or section to file it under.
func validateItems(items []Beer) error {
	for _, e := range items {
		if strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.Section) == "" {
			return fmt.Errorf("%w: every item needs a name and a section (got %q in %q)",
				ErrPayloadInvalid, e.Name, e.Section)
		}
		if !validShow(e.Show) {
			return fmt.Errorf("%w: item %q has show %q; use print, board or both",
				ErrPayloadInvalid, e.Name, e.Show)
		}
	}
	return nil
}

// boardItems turns the wall's share of the items into taps. A row with no
// price is dropped for the reason ParseUntappdBoard drops one: an empty price
// column on a wall reads as a mistake.
func boardItems(items []Beer) []Tap {
	var out []Tap
	for _, e := range items {
		if !e.onBoard() {
			continue
		}
		pour, ok := e.Headline()
		if !ok {
			continue
		}
		out = append(out, Tap{
			Section: e.Section,
			Name:    e.Name,
			Style:   e.Style,
			ABV:     e.ABV,
			Price:   pour.Price,
			Size:    boardPourSize(pour),
		})
	}
	return out
}

func printItems(items []Beer) []Beer {
	var out []Beer
	for _, e := range items {
		if e.onPrint() {
			out = append(out, e)
		}
	}
	return out
}

// mergeBoardItems places each item after the last tap in its section, or in a
// section of its own at the end, the same order mergeExtras gives paper.
func mergeBoardItems(taps, items []Tap) []Tap {
	if len(items) == 0 {
		return taps
	}
	out := append([]Tap(nil), taps...)
	var fresh []Tap
	for _, e := range items {
		at := -1
		for i, t := range out {
			if sectionKey(t.Section) == sectionKey(e.Section) {
				at = i
			}
		}
		if at < 0 {
			fresh = append(fresh, e)
			continue
		}
		out = append(out[:at+1], append([]Tap{e}, out[at+1:]...)...)
	}
	return append(out, fresh...)
}

// Overrides are everything Kit changes about the Untappd tap list before a
// menu draws it: the standing edits and today's 86 list. Both menus apply the
// same Overrides in the same order -- items on, then hidden and 86'd names
// off -- so the wall and the paper never disagree about what Kit changed.
type Overrides struct {
	Edits
	EightySix []string
}

func (a *App) loadOverrides(ctx context.Context, tenantID uuid.UUID) (Overrides, error) {
	edits, err := LoadEdits(ctx, a.pool, tenantID)
	if err != nil {
		return Overrides{}, err
	}
	out, err := LoadEightySix(ctx, a.pool, tenantID)
	if err != nil {
		return Overrides{}, err
	}
	return Overrides{Edits: edits, EightySix: out}, nil
}

// overridesFor is loadOverrides for the wall, where a failure costs the edits
// and never the tap list: a beer shown that should be hidden is recoverable, a
// blank board in a full taproom is not.
func (a *App) overridesFor(ctx context.Context, tenantID uuid.UUID) Overrides {
	ov, err := a.loadOverrides(ctx, tenantID)
	if err != nil {
		slog.Warn("loading menu overrides for the board", "tenant_id", tenantID, "error", err)
		return Overrides{}
	}
	return ov
}

// hidden reports whether a row is hidden, its own name or its whole section.
func (o Overrides) hidden(section, name string) bool {
	for _, s := range o.Hidden.Sections {
		if sectionKey(s) == sectionKey(section) {
			return true
		}
	}
	return containsName(o.Hidden.Items, name)
}

// gone reports whether a row stays off the menus: hidden or 86'd.
func (o Overrides) gone(section, name string) bool {
	return o.hidden(section, name) || containsName(o.EightySix, name)
}

func containsName(names []string, name string) bool {
	key := nameKey(name)
	for _, n := range names {
		if nameKey(n) == key {
			return true
		}
	}
	return false
}

// applyBoard edits the wall's tap list in place.
func (o Overrides) applyBoard(b *Board) {
	taps := mergeBoardItems(b.Taps, boardItems(o.Items))
	kept := make([]Tap, 0, len(taps))
	for _, t := range taps {
		if !o.gone(t.Section, t.Name) {
			kept = append(kept, t)
		}
	}
	b.Taps = kept
}

// applyRows edits the printed menu's rows.
func (o Overrides) applyRows(rows []Beer) []Beer {
	merged := mergeExtras(rows, printItems(o.Items))
	kept := make([]Beer, 0, len(merged))
	for _, r := range merged {
		if !o.gone(r.Section, r.Name) {
			kept = append(kept, r)
		}
	}
	return kept
}

// itemNames is every row Kit itself lists, which the 86 list may name even
// though no Untappd sync ever sees them.
func (o Overrides) itemNames() []string {
	out := make([]string, 0, len(o.Items))
	for _, e := range o.Items {
		out = append(out, e.Name)
	}
	return out
}

// version puts the overrides in front of the wall's version, so an edit or an
// 86 reaches the screen on its next poll. In front, like the other stamps, so
// the render stamp stays the suffix.
func (o Overrides) version(v string) string {
	v = eightySixVersion(o.EightySix, v)
	var lines []string
	for _, e := range boardItems(o.Items) {
		lines = append(lines, strings.Join([]string{e.Section, e.Name, e.Style, e.ABV, e.Price, e.Size}, "\x1f"))
	}
	if len(o.Hidden.Items)+len(o.Hidden.Sections) > 0 {
		lines = append(lines, "items:"+strings.Join(o.Hidden.Items, "\x1f"),
			"sections:"+strings.Join(o.Hidden.Sections, "\x1f"))
	}
	if len(lines) == 0 {
		return v
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "x" + hex.EncodeToString(sum[:])[:6] + "." + v
}
