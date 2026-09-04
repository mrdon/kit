package menu

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

var testTenant = uuid.New()

// A board with no upstream is never refreshed: there is nothing to ask.
func TestEnsureFreshIgnoresUnsourcedBoards(t *testing.T) {
	a := &App{}
	row := &BoardRow{}
	if got := a.EnsureFresh(t.Context(), testTenant, row); got != row {
		t.Error("a hand-set board should be returned untouched")
	}
}

// Inside the TTL nothing goes upstream — this is what makes a wall of screens
// polling every 30s cost one fetch a minute rather than one per screen.
func TestEnsureFreshHonoursTTL(t *testing.T) {
	a := &App{} // nil pool: any real pull would panic, which is the assertion
	just := time.Now().Add(-FreshFor / 2)
	row := &BoardRow{SourceKind: SourceUntappd, SourceID: "1", SyncedAt: &just}
	if got := a.EnsureFresh(t.Context(), testTenant, row); got != row {
		t.Error("a board synced within the TTL should be served as-is")
	}
}

// The pour rules the board depends on, stated as a table so a change to them
// is a deliberate edit rather than a surprise.
//
// The rules moved into the shared source model when the printed menu and the
// till started reading the same tap list, so this now exercises them through
// Headline and boardPourSize. The expectations are unchanged on purpose: the
// board must quote exactly what it quoted before.
func TestHeadlinePourRules(t *testing.T) {
	container := func(price, name string) string {
		return `<div class="container"><div class="price">` + price +
			`</div><div class="name">` + name + `</div></div>`
	}
	for _, c := range []struct {
		name, item, price, size string
	}{
		{"largest draft wins",
			container("6.50", "16oz Draft") + container("4", "9oz Draft"), "6.50", "16oz"},
		{"growler loses to draft",
			container("26", "64oz Growler") + container("8", "12oz Draft"), "8", "12oz"},
		{"packaged-only falls back with its own label",
			container("6", "12oz Can"), "6", "12oz can"},
		{"no containers, no price",
			"", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			beer := Beer{Pours: parsePours(c.item)}
			var price, size string
			if pour, ok := beer.Headline(); ok {
				price, size = pour.Price, boardPourSize(pour)
			}
			if price != c.price || size != c.size {
				t.Errorf("got %q/%q, want %q/%q", price, size, c.price, c.size)
			}
		})
	}
}
