package menu

import (
	"sort"
	"strconv"
	"strings"
)

// The printed menu.
//
// This is the paper rendering of the tap list in source.go -- the same []Beer
// the wall board narrows and the till will read. Everything here is layout:
// which pour holds the price column, what a section is called and what colour
// its bar is, and where the rows Untappd does not carry get slotted in.
//
// Nothing here writes to the board. PrintMenu is assembled per request and
// thrown away; the only thing that persists is the config and the description
// cache.

// PrintSection is a run of rows under one coloured heading.
type PrintSection struct {
	Name  string
	Color string // #rrggbb
	Rows  []Beer
}

// PrintMenu is everything one print run needs.
type PrintMenu struct {
	Title     string
	Subtitle  string
	Flight    string
	Sizes     string
	FootLeft  string
	FootRight string
	Sections  []PrintSection
	Hero      []byte // optional masthead photo, JPEG or PNG
	HeroKind  string // fpdf's image-type string
	Logo      []byte
}

// Draft returns whether a section pours beer, and therefore whether it needs
// the ABV and two-pour columns or just a single price column.
//
// The test is deliberately asymmetric. A section is treated as packaged only
// when every row in it positively says so -- each has pours, and none of them
// is a draft. A beer whose price has been cleared in Untappd has no pours at
// all, and it must not drag its whole section into the single-column layout:
// that would relabel a list of beers as 12oz cans on the strength of one
// missing number.
func (s PrintSection) Draft() bool {
	for _, r := range s.Rows {
		if r.HasDraft() || len(r.Pours) == 0 {
			return true
		}
	}
	return false
}

// PackagedSize is the column heading for a section of cans and bottles: the
// size they mostly come in, so a list of twelve-ounce cans says so once at the
// top rather than on every row.
func (s PrintSection) PackagedSize() string {
	counts := map[string]int{}
	best, bestN := "", 0
	for _, r := range s.Rows {
		for _, p := range r.Pours {
			if p.Size == "" {
				continue
			}
			counts[p.Size]++
			if counts[p.Size] > bestN {
				best, bestN = p.Size, counts[p.Size]
			}
		}
	}
	if best == "" {
		return "Price"
	}
	return best
}

// tidyStyle turns Untappd's catalogue ordering into the way a menu says it.
//
// Untappd files styles genus-first so they sort together -- "IPA - West Coast",
// "Porter - Coffee", "Lager - American" -- which is right for a database and
// wrong for a customer. Swapping the halves gives "West Coast IPA", "Coffee
// Porter", "American Lager", which is what the printed menu has always said
// and what anyone would say out loud.
//
// Only the first separator is used. A style with none is already in menu
// order and is left alone.
func tidyStyle(s string) string {
	s = strings.TrimSpace(s)
	genus, species, ok := strings.Cut(s, " - ")
	if !ok {
		return s
	}
	genus, species = strings.TrimSpace(genus), strings.TrimSpace(species)
	if genus == "" || species == "" {
		return s
	}
	return species + " " + genus
}

// tidyABV gives every strength the same shape. Untappd stores "11%" beside
// "5.7%", and a column mixing the two reads as though somebody forgot a digit.
func tidyABV(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || !strings.HasSuffix(s, "%") {
		return s
	}
	num := strings.TrimSuffix(s, "%")
	if num == "" || strings.Contains(num, ".") {
		return s
	}
	if _, err := strconv.ParseFloat(num, 64); err != nil {
		return s
	}
	return num + ".0%"
}

// buildSections groups rows into their headings, in the order the headings
// first appear upstream, and colours each one.
//
// Order comes from the source rather than from sorting, because the order
// beers appear on the Untappd board is a decision somebody made -- lagers
// first, the strong stuff last -- and re-sorting it here would quietly
// override them.
func buildSections(rows []Beer, colors map[string]string) []PrintSection {
	order := make([]string, 0, 8)
	byName := make(map[string][]Beer)
	for _, r := range rows {
		name := strings.TrimSpace(r.Section)
		if name == "" {
			name = "On Tap"
		}
		if _, seen := byName[name]; !seen {
			order = append(order, name)
		}
		byName[name] = append(byName[name], r)
	}
	out := make([]PrintSection, 0, len(order))
	for i, name := range order {
		out = append(out, PrintSection{
			Name:  name,
			Color: sectionColor(name, i, colors),
			Rows:  byName[name],
		})
	}
	return out
}

// House colours, cycled when a section has not been given one. Gold, dark
// green and orange are the brewery's; the mid green breaks up a run of three
// so two adjacent headings are never the same.
var printPalette = []string{"#fec111", "#1b4525", "#f58a22", "#678f42"}

func sectionColor(name string, i int, colors map[string]string) string {
	if c, ok := colors[name]; ok && c != "" {
		return c
	}
	// Case-insensitive second look: the config is hand-typed and the section
	// name comes off a scrape, so "Pub Ales" and "PUB ALES" should be one key.
	lower := strings.ToLower(name)
	for k, v := range colors {
		if strings.ToLower(k) == lower && v != "" {
			return v
		}
	}
	return printPalette[i%len(printPalette)]
}

// mergeExtras appends the configured non-Untappd rows.
//
// They go after the scraped ones so a section that exists in both -- a
// "Specialty" heading holding a couple of taps and a couple of cans -- reads
// beers first. Extras that name a brand new section simply add one at the end,
// which is where sodas and juice boxes belong anyway.
func mergeExtras(rows, extras []Beer) []Beer {
	if len(extras) == 0 {
		return rows
	}
	seen := make(map[string]int, len(rows))
	for i, r := range rows {
		seen[strings.ToLower(strings.TrimSpace(r.Section))] = i
	}
	out := append([]Beer(nil), rows...)
	var fresh []Beer
	for _, e := range extras {
		if _, ok := seen[strings.ToLower(strings.TrimSpace(e.Section))]; ok {
			out = append(out, e)
			continue
		}
		fresh = append(fresh, e)
	}
	// Stable insert of the rows whose section already exists, so they land
	// beside their own heading rather than at the bottom of the document.
	sort.SliceStable(out, func(i, j int) bool {
		return sectionIndex(rows, out[i].Section) < sectionIndex(rows, out[j].Section)
	})
	return append(out, fresh...)
}

func sectionIndex(rows []Beer, section string) int {
	key := strings.ToLower(strings.TrimSpace(section))
	for i, r := range rows {
		if strings.ToLower(strings.TrimSpace(r.Section)) == key {
			return i
		}
	}
	return len(rows)
}
