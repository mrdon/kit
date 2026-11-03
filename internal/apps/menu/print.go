package menu

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
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

// PrintSection is a run of rows under one heading.
type PrintSection struct {
	Name string
	Rows []Beer

	// featured is set only on the headless section the renderer puts first
	// to carry the featured box through the same flow as everything else.
	featured *Featured

	// Blurb is a sentence set under the heading, before any rows. It is how
	// the menu says a thing exists without pricing it line by line -- snacks
	// being the case it was added for: a taproom puts out pretzels and popcorn
	// and wants one heading and one sentence, not six rows and six prices.
	//
	// A section may have a blurb and rows both, which reads as a note about
	// the list under it ("all cans to go").
	Blurb string
}

// PrintMenu is everything one print run needs.
type PrintMenu struct {
	// Brand is the venue's name for the masthead -- the board's wordmark, so
	// paper and wall say the same thing. Empty puts the title there instead.
	Brand     string
	Title     string
	Subtitle  string
	Flight    string
	Sizes     string
	FootLeft  string
	FootRight string
	Sections  []PrintSection
	Featured  *Featured // the one beer boxed at the head of the menu, if any
	Hero      []byte    // optional masthead photo, JPEG or PNG
	HeroKind  string    // fpdf's image-type string
	Logo      []byte
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
// first appear upstream.
//
// Order comes from the source rather than from sorting, because the order
// beers appear on the Untappd board is a decision somebody made -- lagers
// first, the strong stuff last -- and re-sorting it here would quietly
// override them.
func buildSections(rows []Beer, blurbs map[string]string) []PrintSection {
	order := make([]string, 0, 8)
	names := make(map[string]string) // key -> the spelling seen first
	byKey := make(map[string][]Beer)
	for _, r := range rows {
		name := strings.TrimSpace(r.Section)
		if name == "" {
			name = "On Tap"
		}
		key := sectionKey(name)
		if _, seen := byKey[key]; !seen {
			order = append(order, key)
			names[key] = name
		}
		byKey[key] = append(byKey[key], r)
	}
	out := make([]PrintSection, 0, len(order))
	for _, key := range order {
		out = append(out, PrintSection{
			Name:  names[key],
			Blurb: lookupFold(blurbs, names[key]),
			Rows:  byKey[key],
		})
	}
	return standaloneBlurbs(out, blurbs)
}

// standaloneBlurbs adds a heading for every blurb that matched no section.
//
// This is how snacks reach a beer menu. There is nothing upstream to hang them
// on -- Untappd knows about beer -- and they are not extras either, because an
// extra is a row with a price and the whole point of a blurb is not having
// one.
//
// They land after everything that pours, which is where a food line belongs on
// a drinks menu, and in name order so that two of them do not swap places
// between prints for no reason a reader could see.
func standaloneBlurbs(sections []PrintSection, blurbs map[string]string) []PrintSection {
	if len(blurbs) == 0 {
		return sections
	}
	used := make(map[string]bool, len(sections))
	for _, s := range sections {
		used[sectionKey(s.Name)] = true
	}
	names := make([]string, 0, len(blurbs))
	for name, text := range blurbs {
		if strings.TrimSpace(text) == "" || used[sectionKey(name)] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sections = append(sections, PrintSection{
			Name:  name,
			Blurb: blurbs[name],
		})
	}
	return sections
}

// lookupFold reads a hand-typed config map with a scraped key.
//
// One side is written by a person and the other comes off Untappd, so "Pub
// Ales", "PUB ALES" and "Pub-Ales" have to be one entry (see sectionKey). An exact hit is tried first, so a
// config that does distinguish two casings is not second-guessed.
func lookupFold(m map[string]string, key string) string {
	if v, ok := m[key]; ok && v != "" {
		return v
	}
	want := sectionKey(key)
	for k, v := range m {
		if sectionKey(k) == want && v != "" {
			return v
		}
	}
	return ""
}

// sectionKey is a heading as matching sees it: letters and digits only, in
// lower case. Untappd's "Non Alcoholic" and a hand-typed "Non-Alcoholic" are
// the same heading, and printing them as two sections, one under the other,
// is the bug this exists to prevent.
func sectionKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
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
		seen[sectionKey(r.Section)] = i
	}
	out := append([]Beer(nil), rows...)
	var fresh []Beer
	for _, e := range extras {
		if _, ok := seen[sectionKey(e.Section)]; ok {
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
	key := sectionKey(section)
	for i, r := range rows {
		if sectionKey(r.Section) == key {
			return i
		}
	}
	return len(rows)
}
