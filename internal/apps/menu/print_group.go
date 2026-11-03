package menu

import (
	"strings"
)

// Grouping variants of one beer under one description.
//
// A brewery that pours four vintages of the same stout writes one description
// and Untappd repeats it on each: "2026 Tsar Bomba", "2026 Barrel Aged Tsar
// Bomba", "2025 Tsar Bomba", "Tsar Bomba W/ Snickers", all saying the same
// paragraph. Printed as four rows that is the same paragraph four times.
//
// The rule is mechanical, because nothing at print time can judge what a
// family of beers is: rows that sit next to each other in a section and carry
// the identical description become one entry. Its heading is the longest run
// of whole words every name shares ("Tsar Bomba") and each row keeps its own
// line, labelled with what is left of its name ("2026 Barrel Aged"), with its
// own strength, price and marks.
//
// The trigger is the text Untappd already repeats. A brewery that writes each
// vintage its own description gets four ordinary rows again, with no setting
// to change. Two unrelated beers that happen to share a sentence still print
// it once, but keep their full names, because a shared word or two ("The")
// is not a family name.

// printUnit is one thing the layout places: a beer, a family of beers sharing
// a description, or a section's sentence.
type printUnit struct {
	Beer  *Beer
	Group *beerGroup
	Blurb string
}

// beerGroup is a run of rows printed under one description.
type beerGroup struct {
	// Name is the shared run of words, or empty when the names share too
	// little to be called a family; the variants then carry their full names.
	Name  string
	Style string // the style most of them share, tidied
	Size  string // the pour they all share, or empty when they differ
	Notes string
	Rows  []groupVariant
}

// groupVariant is one row inside a group.
type groupVariant struct {
	Label string // the name with the shared words taken out
	Style string // set only when it differs from the group's
	Beer  Beer
}

// minFamilyWords and minFamilyChars are what a shared run must reach to head
// a group: two words, or one word long enough to be a name ("Barleywine"
// rather than "The").
const (
	minFamilyWords = 2
	minFamilyChars = 6
)

// sectionUnits turns a section into what the layout places, in order: its
// sentence first, then its rows with every run of shared descriptions folded.
func sectionUnits(s PrintSection) []printUnit {
	var out []printUnit
	if b := strings.TrimSpace(s.Blurb); b != "" {
		out = append(out, printUnit{Blurb: b})
	}
	rows := s.Rows
	for i := 0; i < len(rows); {
		j := i + 1
		key := descKey(rows[i].Notes)
		for key != "" && j < len(rows) && descKey(rows[j].Notes) == key {
			j++
		}
		if j-i < 2 {
			out = append(out, printUnit{Beer: &rows[i]})
		} else {
			out = append(out, printUnit{Group: newBeerGroup(rows[i:j])})
		}
		i = j
	}
	return out
}

// descKey is a description as the comparison sees it: trimmed, with runs of
// whitespace collapsed, so a stray double space upstream does not split a
// family. Empty means "no description" and never groups.
func descKey(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// newBeerGroup builds a group from rows already known to share a description.
func newBeerGroup(rows []Beer) *beerGroup {
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Name
	}
	run := commonWordRun(names)
	family := strings.Join(run, " ")
	if len(run) < minFamilyWords && len([]rune(family)) < minFamilyChars {
		run, family = nil, ""
	}

	g := &beerGroup{Name: family, Style: commonStyle(rows), Notes: rows[0].Notes}
	g.Size = sharedSize(rows)
	for _, r := range rows {
		label := r.Name
		if run != nil {
			if rest := removeWordRun(r.Name, run); rest != "" {
				label = rest
			}
		}
		v := groupVariant{Label: label, Beer: r}
		if st := tidyStyle(r.Style); st != g.Style {
			v.Style = st
		}
		g.Rows = append(g.Rows, v)
	}
	return g
}

// commonWordRun is the longest run of consecutive whole words that appears in
// every name, compared without case. The first name's spelling is kept.
func commonWordRun(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	first := strings.Fields(names[0])
	rest := make([][]string, 0, len(names)-1)
	for _, n := range names[1:] {
		rest = append(rest, lowerFields(n))
	}
	var best []string
	for i := range first {
		for j := len(first); j > i && j-i > len(best); j-- {
			run := lowerWords(first[i:j])
			if allContainRun(rest, run) {
				best = first[i:j]
				break
			}
		}
	}
	return best
}

// removeWordRun takes the first occurrence of run out of name, by whole words
// and without case, and returns what is left.
func removeWordRun(name string, run []string) string {
	words := strings.Fields(name)
	if k := indexRun(lowerWords(words), lowerWords(run)); k >= 0 {
		left := append(append([]string{}, words[:k]...), words[k+len(run):]...)
		return strings.Join(left, " ")
	}
	return name
}

// commonStyle is the tidied style most of the rows share. A tie goes to the
// one that appears first, so the answer does not move between prints.
func commonStyle(rows []Beer) string {
	counts := map[string]int{}
	best, bestN := "", 0
	for _, r := range rows {
		st := tidyStyle(r.Style)
		counts[st]++
		if counts[st] > bestN {
			best, bestN = st, counts[st]
		}
	}
	return best
}

// sharedSize is the headline pour every row has, or empty when they differ.
func sharedSize(rows []Beer) string {
	size := ""
	for i, r := range rows {
		p, _ := r.Headline()
		if i == 0 {
			size = p.Size
			continue
		}
		if p.Size != size {
			return ""
		}
	}
	return size
}

func allContainRun(names [][]string, run []string) bool {
	for _, n := range names {
		if indexRun(n, run) < 0 {
			return false
		}
	}
	return true
}

func indexRun(words, run []string) int {
	for k := 0; k+len(run) <= len(words); k++ {
		match := true
		for m := range run {
			if words[k+m] != run[m] {
				match = false
				break
			}
		}
		if match {
			return k
		}
	}
	return -1
}

func lowerFields(s string) []string { return lowerWords(strings.Fields(s)) }

func lowerWords(ws []string) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = strings.ToLower(w)
	}
	return out
}
