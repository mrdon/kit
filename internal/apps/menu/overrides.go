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

// Where a configured extra row shows. One list feeds both menus because a can
// of soda is the same can on paper and on the wall, but the two have different
// room: the sheet lists every non-alcoholic can by name, while the wall gets
// one line for all of them and no juice boxes at all.
//
// Empty means print, which is what every row meant before the wall read this
// list, so a stored config keeps doing exactly what it did.
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

func (b Beer) onPrint() bool { return b.Show == "" || b.Show == ShowPrint || b.Show == ShowBoth }
func (b Beer) onBoard() bool { return b.Show == ShowBoard || b.Show == ShowBoth }

// validateExtras rejects a show value nobody will see the effect of. A typo
// here would otherwise quietly keep a row off both menus.
func validateExtras(extras []Beer) error {
	for _, e := range extras {
		if !validShow(e.Show) {
			return fmt.Errorf("%w: extra %q has show %q; use print, board or both",
				ErrPayloadInvalid, e.Name, e.Show)
		}
	}
	return nil
}

// printExtras is the slice of the list the printed menu draws.
func printExtras(extras []Beer) []Beer {
	var out []Beer
	for _, e := range extras {
		if e.onPrint() {
			out = append(out, e)
		}
	}
	return out
}

// boardExtras turns the wall's share of the list into taps. A row with no
// price is dropped for the reason ParseUntappdBoard drops one: an empty price
// column on a wall reads as a mistake.
func boardExtras(extras []Beer) []Tap {
	var out []Tap
	for _, e := range extras {
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

// mergeBoardExtras places each extra after the last tap in its section, or in
// a section of its own at the end, the same order mergeExtras gives paper.
func mergeBoardExtras(taps, extras []Tap) []Tap {
	if len(extras) == 0 {
		return taps
	}
	out := append([]Tap(nil), taps...)
	var fresh []Tap
	for _, e := range extras {
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

// Overrides are the venue's edits to the Untappd tap list: rows it adds that
// Untappd does not carry, and names it takes off for now. Both menus apply the
// same Overrides in the same order, extras on and then the 86 off, so a can
// that runs out leaves the wall and the paper together, and neither menu has
// its own idea of what Kit changed.
//
// What they deliberately do not share: an extra's Show says which menu has
// room for the row, permanently; the 86 list says what is unavailable today,
// on both. Hiding something for the day is always an 86, never a Show edit.
type Overrides struct {
	Extras    []Beer
	EightySix []string
}

// loadOverrides reads both halves. The extras live in the printed menu's
// stored config, where the console edits them.
func (a *App) loadOverrides(ctx context.Context, tenantID uuid.UUID) (Overrides, error) {
	state, err := LoadPrintState(ctx, a.pool, tenantID)
	if err != nil {
		return Overrides{}, err
	}
	out, err := LoadEightySix(ctx, a.pool, tenantID)
	if err != nil {
		return Overrides{}, err
	}
	return Overrides{Extras: state.Config.Extras, EightySix: out}, nil
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

// applyBoard edits the wall's tap list in place.
func (o Overrides) applyBoard(b *Board) {
	b.Taps = mergeBoardExtras(b.Taps, boardExtras(o.Extras))
	dropEightySix(b, o.EightySix)
}

// applyRows edits the printed menu's rows.
func (o Overrides) applyRows(rows []Beer) []Beer {
	return dropEightySixRows(mergeExtras(rows, printExtras(o.Extras)), o.EightySix)
}

// names is every row Kit itself lists, which the 86 list may name even
// though no Untappd sync ever sees them.
func (o Overrides) names() []string {
	out := make([]string, 0, len(o.Extras))
	for _, e := range o.Extras {
		out = append(out, e.Name)
	}
	return out
}

// version puts the overrides in front of the wall's version, so an 86 or a
// changed extra reaches the screen on its next poll. In front, like the other
// stamps, so the render stamp stays the suffix.
func (o Overrides) version(v string) string {
	v = eightySixVersion(o.EightySix, v)
	extras := boardExtras(o.Extras)
	if len(extras) == 0 {
		return v
	}
	lines := make([]string, 0, len(extras))
	for _, e := range extras {
		lines = append(lines, strings.Join([]string{e.Section, e.Name, e.Style, e.ABV, e.Price, e.Size}, "\x1f"))
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "x" + hex.EncodeToString(sum[:])[:6] + "." + v
}
