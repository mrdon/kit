package events

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Choosing and trimming the words a band prints.
//
// Everything here exists because event copy is written for a web page, where
// the reader has a screen and the event has a heading. A band on a table
// topper has one or two lines at a size readable from a chair, and it already
// prints the day, the door time and the title in the largest type on the card.
// So this picks the shortest true source, then takes out what the band has
// said already.

// bandFacts is what the band prints for itself, and therefore what its bullets
// must not spend a line repeating.
type bandFacts struct {
	Weekday string // "Wednesday"
	Time    string // "6:30pm"; empty for an all-day event
	Title   string
}

// topperSource picks the copy a band's bullets come from.
//
// Summary first. It is the one-line teaser written for listings, and a table
// topper is a listing -- the same call the promo card already makes. A
// description is long-form web prose whose opening paragraph is a lede rather
// than a bullet; clipped to a band it prints "BATTLES & BREWS BRINGS …", which
// spends the only readable line repeating the title above it.
//
// The exception is a description someone wrote AS a list. Those lines are
// already bullets and are usually better than the summary, because a list
// carries the conditions ("Dine in only") that a one-line teaser smooths away.
// The test is whether they print whole: a line the band would have to clip is
// a paragraph whatever it was meant to be.
func topperSource(e *Event) []string {
	desc := strings.TrimSpace(e.Description)
	if lines := splitLines(desc); printsAsList(lines) {
		return lines
	}
	if s := strings.TrimSpace(e.Summary); s != "" {
		return splitSentences(s)
	}
	return splitSentences(desc)
}

// printsAsList reports whether a description was written as a list rather than
// as paragraphs.
//
// Two tests, and a line has to pass both. It must be short enough to print
// unclipped, because a line the band would have to cut is a paragraph whatever
// it was meant to be. And it must be a single sentence: a short paragraph can
// slip under the length test -- "Normal Friday hours up to then. Free entry,
// no booking, everyone welcome." is only 72 characters -- but two sentences on
// one line is prose being wrapped, not an item in a list.
func printsAsList(lines []string) bool {
	if len(lines) < 2 {
		return false
	}
	for _, l := range lines {
		l = stripBulletMarker(l)
		if len([]rune(l)) > topperBulletChars || len(splitSentences(l)) > 1 {
			return false
		}
	}
	return true
}

// stripBulletMarker removes the dash or dot someone typed to make a list, so
// the card draws its own.
func stripBulletMarker(s string) string {
	return strings.TrimSpace(strings.TrimLeft(s, "-*•· \t"))
}

// trimBandEcho takes out of a bullet what the band already says.
//
// It cuts only at a clause boundary. "Quiz night every Wednesday at 6:30pm"
// loses its tail cleanly, while "One hour on a Thursday night" keeps its --
// removing the day there would leave "One hour night", which is worse than the
// repetition it fixed. Saying a thing twice is a waste of a line; saying it in
// broken English is a card nobody trusts.
//
// A time is only redundant when it is the band's own time. The AFL band opens
// at 9:30pm and bounces at 10:30pm, and the second of those is the line's
// entire point.
func trimBandEcho(bullet string, band bandFacts) string {
	if title := strings.TrimSpace(band.Title); title != "" {
		// A bullet that is only the title again. Copy pasted between the
		// summary and the heading does this, and on a card the band has
		// already printed it in display type an inch above.
		if strings.EqualFold(strings.Trim(bullet, " .!?"), title) {
			return ""
		}
	}
	out := bullet
	for _, re := range echoPatterns(band) {
		out = re.ReplaceAllString(out, "${1}")
	}
	if out == bullet {
		// Nothing was being repeated, so the author's line is returned exactly
		// as written -- tidying a line this never touched would quietly
		// restyle every bullet on the card.
		return bullet
	}
	return tidyAfterCut(out)
}

// echoPatterns builds the phrases this band would be repeating, longest first
// so "every Wednesday at 6:30pm" goes as one cut rather than leaving a
// stranded "at" behind.
//
// Each pattern keeps whatever punctuation ended the clause and hands it back
// in the replacement, so removing the middle of "Quiz night every Wednesday,
// free to play" leaves the comma doing its job.
func echoPatterns(band bandFacts) []*regexp.Regexp {
	day, at := dayPhrase(band.Weekday), timePhrase(band.Time)

	var out []*regexp.Regexp
	add := func(body string) {
		if body != "" {
			// The tail is a clause boundary: the end of the line, or the
			// punctuation opening the next clause. Anything else means the
			// phrase is carrying grammar and has to stay.
			out = append(out, regexp.MustCompile(`(?i)[,;]?\s*\b`+body+`(\s*[,.;:]|\s*$)`))
		}
	}
	if day != "" && at != "" {
		add(`(?:every|each|on)\s+` + dayCore(band.Weekday) + `\s+(?:at|from)\s+` + timeCore(band.Time))
	}
	add(day)
	add(at)
	return out
}

// dayCore is the day itself: "Wednesday", "Wednesdays", "Wednesday night".
func dayCore(weekday string) string {
	if weekday == "" {
		return ""
	}
	return `(?:a\s+)?` + regexp.QuoteMeta(weekday) + `s?(?:\s+(?:night|evening|afternoon|morning))?`
}

// dayPhrase matches the ways copy names the band's own day, with or without
// the "every" in front. Bare "Wednesday" counts because at a clause boundary,
// on a band already headed WED, it can only be saying the same thing.
func dayPhrase(weekday string) string {
	core := dayCore(weekday)
	if core == "" {
		return ""
	}
	return `(?:(?:every|each|on)\s+)?` + core
}

// timeCore matches the band's door time however copy spells it -- "6:30pm",
// "6:30 PM", "6:30 p.m." An on-the-hour time also matches its ":00" form.
func timeCore(label string) string {
	m := timeLabelRE.FindStringSubmatch(label)
	if m == nil {
		return ""
	}
	hour, minute, meridiem := m[1], m[2], m[3]
	clock := regexp.QuoteMeta(hour)
	if minute == "" {
		clock += `(?::00)?`
	} else {
		clock += `:` + regexp.QuoteMeta(minute)
	}
	return clock + `\s*` + meridiem[:1] + `\.?\s*m\.?`
}

// timePhrase is the door time with whatever introduces it.
func timePhrase(label string) string {
	core := timeCore(label)
	if core == "" {
		return ""
	}
	return `(?:(?:at|from|starting(?:\s+at)?|kicking\s+off(?:\s+at)?|doors(?:\s+at)?)\s+)?` + core
}

// timeLabelRE splits a band's time label ("4pm", "6:30pm") into its parts.
var timeLabelRE = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?\s*([ap]m)$`)

// tidyAfterCut repairs the seams a removal leaves: doubled spaces, a comma
// with nothing before it, a sentence now starting lowercase, a trailing
// conjunction left dangling by the clause that used to follow it.
func tidyAfterCut(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimSpace(strings.TrimLeft(s, ",;:."))
	s = danglingRE.ReplaceAllString(s, "")
	s = strings.TrimRight(s, " ,;:-.")
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// danglingRE catches a conjunction or preposition left pointing at nothing.
var danglingRE = regexp.MustCompile(`(?i)[,\s]+\b(?:and|or|with|from|at|on|in|for|every|each)\s*$`)

// keepsALine reports whether what survived trimming is still worth a band's
// line. One word is not: "Doors at 6:30pm" trimmed to "Doors" says nothing the
// band's own 6:30PM did not, and a band reads better with one good line than
// with a good line and a stub.
func keepsALine(s string) bool {
	return len(strings.Fields(s)) >= 2
}

// splitSentences breaks a summary into bullets at its sentence ends.
//
// Still deliberately crude -- it only has to turn "Quiz night every Wednesday.
// Free to play." into two lines. What it must not do is split inside an
// abbreviation: "Home opener vs. Jacksonville" printed as "HOME OPENER VS" is
// the kind of cut that makes a customer think the card is broken, and on a
// band that has room for one line it throws away the only line.
func splitSentences(s string) []string {
	var out []string
	for len(s) > 0 {
		i := sentenceEnd(s)
		if i < 0 {
			out = append(out, strings.TrimSuffix(strings.TrimSpace(s), "."))
			break
		}
		out = append(out, strings.TrimSpace(s[:i]))
		s = s[i+2:]
	}
	return out
}

// sentenceEnd finds the first ". " that really ends a sentence, or -1.
func sentenceEnd(s string) int {
	for at := 0; at < len(s); {
		i := strings.Index(s[at:], ". ")
		if i < 0 {
			return -1
		}
		i += at
		if endsSentence(s[:i]) {
			return i
		}
		at = i + 2
	}
	return -1
}

// endsSentence reports whether the text before a period is a whole sentence
// rather than an abbreviation or an initial.
func endsSentence(before string) bool {
	fields := strings.Fields(before)
	if len(fields) == 0 {
		return false
	}
	last := strings.ToLower(strings.Trim(fields[len(fields)-1], `("'`))
	// A single LETTER is an initial -- "J. Wilson", "E. 9th Ave". A single
	// digit is not: "Doors at 7." is a whole sentence.
	if r := []rune(last); len(r) == 1 && unicode.IsLetter(r[0]) {
		return false
	} else if len(r) == 0 {
		return false
	}
	return !abbreviations[last]
}

// abbreviations are the short forms that take a period without ending a
// sentence. Listed rather than inferred: the set that turns up in event copy
// is small, and a rule clever enough to guess would also guess wrong.
var abbreviations = map[string]bool{
	"vs": true, "v": true, "no": true, "etc": true, "est": true,
	"st": true, "mt": true, "ft": true, "ave": true, "rd": true, "blvd": true,
	"dr": true, "mr": true, "mrs": true, "ms": true, "jr": true, "sr": true,
	"inc": true, "co": true, "dept": true, "approx": true, "feat": true,
	"min": true, "hr": true, "oz": true, "pt": true, "lb": true,
	"a.m": true, "p.m": true, "u.s": true, "e.g": true, "i.e": true,
}

// What a summary has room to say.
//
// The table topper is the tightest thing the summary feeds, and it is the one
// a customer reads from a chair rather than a screen. These numbers come from
// measuring the card rather than from taste: a band's detail column is 59.6mm
// wide, and at the 8pt floor a busy week falls back to, that is 46 characters
// of caps on a line. A band prints two of those lines.
//
// The first sentence is the one that has to survive, because a day with
// another event on it spends the band's second line naming that event -- so
// the headliner is left with one line and whatever fits on it.
const (
	// SummaryFirstSentenceChars is the room the opening sentence is sure to
	// get, on the busiest week, on a day with something else on.
	SummaryFirstSentenceChars = 45
	// SummaryChars is the whole summary's room: both lines of a band on a day
	// with nothing competing for the second one.
	SummaryChars = 90
)

// SummaryAdvice reports what a summary will lose on the printed card, or "" if
// it prints whole. Returned as advice rather than as validation: a summary too
// long for a band is still a good summary for the website and the feed, and
// refusing it would be worse than saying so.
func SummaryAdvice(summary string) string {
	s := strings.TrimSpace(summary)
	if s == "" {
		return ""
	}
	if first := splitSentences(s); len([]rune(first[0])) > SummaryFirstSentenceChars {
		return fmt.Sprintf(
			"the summary's first sentence is %d characters; the table topper prints about %d "+
				"on a band that is sharing its day, so lead with the short version",
			len([]rune(first[0])), SummaryFirstSentenceChars)
	}
	if len([]rune(s)) > SummaryChars {
		return fmt.Sprintf(
			"the summary is %d characters; the table topper has room for about %d, so the rest "+
				"prints only on the website",
			len([]rune(s)), SummaryChars)
	}
	return ""
}
