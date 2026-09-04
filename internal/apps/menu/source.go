package menu

import (
	"context"
	"net/http"
	"sort"
	"strings"
)

// The tap list, normalised, once.
//
// Untappd is read in exactly one place and turned into []Beer. Everything that
// consumes the tap list works from that: the wall board collapses each beer to
// the one price a screen has room for, the printed menu spreads the pours
// across columns and adds a description, and a till needs the four-ounce price
// to sell a flight. Three surfaces, three different subsets, one parse.
//
// This exists because the alternative was tried and did not hold. Each surface
// scraping for itself meant two walks over the same markup that had already
// drifted -- one dropped a beer whose price was cleared upstream and the other
// kept it -- and a third consumer would have made three. A beer is a beer;
// which of its facts a surface shows is a rendering decision, not a parsing
// one.
//
// Beer carries everything Untappd gives, including the pours nobody currently
// prints. Throwing a field away at parse time is what forces the next surface
// to write its own parser.

// Beer is one row of the tap list, as Untappd has it.
//
// Prices are strings rather than cents because they are display text and never
// arithmetic -- "6.50" and "8" are both correct as written, and rounding
// either into the other would be wrong. Style is kept exactly as Untappd files
// it ("IPA - West Coast"); reordering it for a menu is a rendering decision.
type Beer struct {
	Section string `json:"section"`
	Name    string `json:"name"`
	Style   string `json:"style"`
	ABV     string `json:"abv"`

	// Pours is every container Untappd prices this beer in, in its order.
	Pours []Pour `json:"pours,omitempty"`

	// Notes is the description. It never comes off the board -- that template
	// carries no prose -- so it is filled in separately from the beer's page
	// on the consumer site, and is empty until then.
	Notes string `json:"notes,omitempty"`
}

// Pour is one container and its price.
type Pour struct {
	Size  string `json:"size"`  // "16oz" -- the leading token of Untappd's label
	Label string `json:"label"` // "16oz Draft" -- as Untappd writes it
	Price string `json:"price"`
}

// Headline returns the one pour a single-price surface should quote.
//
// Draft wins outright and biggest draft wins among those: a growler is
// takeaway, and showing its price beside a beer name would say a pint costs
// twenty-six dollars. A beer with no draft price at all -- packaged only, or
// simply not priced -- falls back to its first other container, so the row
// says what Untappd says rather than inventing a number.
//
// The caller decides what to do when this reports false. The board drops the
// row; the printed menu keeps it and prints a dash, because on paper a beer
// that is visibly pouring should still be listed.
func (b Beer) Headline() (Pour, bool) {
	best := 0
	var chosen, packaged Pour
	var havePackaged bool
	for _, p := range b.Pours {
		if p.Price == "" || p.Size == "" {
			continue
		}
		// Draft is decided by the word, not the size. Untappd labels are
		// "16oz Draft", "64oz Growler", "12oz Can" -- keying on the size alone
		// made a 12oz can outrank a 9oz pour and take the pint column.
		if isDraft(p.Label) {
			if rank, ok := draftRank[p.Size]; ok && rank > best {
				best, chosen = rank, p
			}
			continue
		}
		if !havePackaged {
			packaged, havePackaged = p, true
		}
	}
	if best == 0 {
		return packaged, havePackaged
	}
	return chosen, true
}

// PourBySize returns a specific pour, for a surface that wants one by name --
// a till selling a flight wants the 4oz price, whatever the beer's headline is.
func (b Beer) PourBySize(size string) (Pour, bool) {
	size = strings.ToLower(strings.TrimSpace(size))
	for _, p := range b.Pours {
		if p.Size == size && p.Price != "" {
			return p, true
		}
	}
	return Pour{}, false
}

// HasDraft reports whether the beer is poured at all, as opposed to being sold
// only in a can or a bottle.
func (b Beer) HasDraft() bool {
	for _, p := range b.Pours {
		if isDraft(p.Label) && p.Price != "" {
			return true
		}
	}
	return false
}

// tasterPour is the size every tap is priced in. It is the tell for whether a
// beer is actually on tap.
const tasterPour = "4oz"

// OnTap reports whether the beer is genuinely pouring.
//
// The test is a priced four-ounce draft, because every beer on the wall is
// priced for a flight and nothing else is. It is a sharper question than
// HasDraft: a beer whose prices have been cleared upstream has no pours at all
// and still sits in the board's markup, and a can listed alongside the taps
// has containers but is not a pour. Both are things a customer cannot order by
// the glass, and printing either on a menu invites an order that ends in an
// apology.
//
// This is also the set a flight is drawn from, which is why it lives here
// rather than in the printed menu: the till asking "what can go in a flight"
// and the menu asking "what is pouring" are the same question.
func (b Beer) OnTap() bool {
	p, ok := b.PourBySize(tasterPour)
	return ok && isDraft(p.Label)
}

// OnTapOnly narrows a tap list to what is actually pouring, keeping order.
func OnTapOnly(beers []Beer) []Beer {
	out := make([]Beer, 0, len(beers))
	for _, b := range beers {
		if b.OnTap() {
			out = append(out, b)
		}
	}
	return out
}

func isDraft(label string) bool {
	return strings.Contains(strings.ToLower(label), "draft")
}

// FetchBeers pulls the board and parses it, returning the tap list and a hash
// of the bytes it came from.
func FetchBeers(ctx context.Context, client *http.Client, boardID string) ([]Beer, string, error) {
	body, hash, err := FetchUntappdBody(ctx, client, boardID)
	if err != nil {
		return nil, "", err
	}
	return ParseBeers(body), hash, nil
}

// ParseBeers extracts the tap list from a board page's HTML.
//
// Nothing is filtered here. A beer with no price is still on the wall, and
// whether that is worth showing is a decision each surface makes for itself --
// a parser that drops rows leaves its callers unable to tell "not pouring"
// from "not parsed".
func ParseBeers(raw string) []Beer {
	doc := unescapePayload(raw)

	// Walk sections and items in document order so each beer keeps the
	// section heading it sits under.
	type mark struct {
		at      int
		section string
		item    string
	}
	sections := sectionRe.FindAllStringSubmatchIndex(doc, -1)
	items := itemStartRe.FindAllStringIndex(doc, -1)

	// Every place an item's markup must stop: the next item, the next section
	// heading, or the footer.
	bounds := make([]int, 0, len(items)+len(sections)+1)
	for _, m := range items {
		bounds = append(bounds, m[0])
	}
	for _, m := range sections {
		bounds = append(bounds, m[0])
	}
	if m := footerRe.FindStringIndex(doc); m != nil {
		bounds = append(bounds, m[0])
	}
	sort.Ints(bounds)

	var marks []mark
	for _, m := range sections {
		marks = append(marks, mark{at: m[0], section: cleanText(doc[m[2]:m[3]])})
	}
	for _, m := range items {
		end := len(doc)
		if i := sort.SearchInts(bounds, m[0]+1); i < len(bounds) {
			end = bounds[i]
		}
		marks = append(marks, mark{at: m[0], item: doc[m[0]:end]})
	}
	sort.Slice(marks, func(i, j int) bool { return marks[i].at < marks[j].at })

	var beers []Beer
	section := ""
	for _, mk := range marks {
		if mk.item == "" {
			section = mk.section
			continue
		}
		name := firstGroup(nameRe, mk.item)
		if name == "" {
			continue
		}
		beers = append(beers, Beer{
			Section: section,
			Name:    name,
			Style:   firstGroup(styleRe, mk.item),
			ABV:     firstGroup(abvRe, mk.item),
			Pours:   parsePours(mk.item),
		})
	}
	return beers
}

// parsePours reads every priced container off an item, in Untappd's order.
func parsePours(item string) []Pour {
	var pours []Pour
	for _, m := range containerRe.FindAllStringSubmatch(item, -1) {
		price, label := cleanText(m[1]), cleanText(m[2])
		fields := strings.Fields(label)
		if price == "" || len(fields) == 0 {
			continue
		}
		pours = append(pours, Pour{
			Size:  strings.ToLower(fields[0]),
			Label: label,
			Price: price,
		})
	}
	return pours
}
