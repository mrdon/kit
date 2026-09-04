package events

import (
	"fmt"
	"slices"
	"strings"
)

// Label limits. Generous enough that nobody sane hits them, tight enough that
// a runaway caller cannot turn one row into a document.
const (
	maxLabels    = 16
	maxLabelRune = 32
)

// The standard labels, as constants rather than strings scattered through the
// code. A downstream page filters on the exact word -- the website's Give Back
// page asks for LabelGiveback -- so the one place it is spelled should be a
// symbol the compiler checks, not a literal somebody retypes.
//
// The vocabulary is deliberately NOT closed. Labels exist so a reader can group
// events in ways Kit does not need to understand, and a closed set makes the
// most ordinary use ("tag the Wednesday market nights so I can find them")
// impossible without a deploy. What the standard set buys instead is gravity:
// these are offered in the tool schema and shown as presets in the console, so
// the common cases converge on one spelling without the uncommon ones being
// refused.
const (
	LabelGiveback  = "giveback"
	LabelFood      = "food"
	LabelTrivia    = "trivia"
	LabelMusic     = "music"
	LabelRelease   = "release"
	LabelFamily    = "family"
	LabelCommunity = "community"
)

// StandardLabel pairs a label with what it means. The description is not
// decoration: it is what the tool schema shows the model and what the console
// shows a person, and it is the thing that stops "music" and "release" being
// used interchangeably six months from now.
type StandardLabel struct {
	Label string `json:"label"`
	Means string `json:"means"`
}

// StandardLabels is an ordered slice, not a map, because this order is
// user-visible in the tool description and the console presets. Map iteration
// would reshuffle both on every process start.
var StandardLabels = []StandardLabel{
	{LabelGiveback, "a charity night: a local nonprofit takes the night and a share of sales goes to them"},
	{LabelFood, "a food offer or a food-led night, including standing deals"},
	{LabelTrivia, "a quiz night"},
	{LabelMusic, "live music"},
	{LabelRelease, "a new beer coming out"},
	{LabelFamily, "explicitly good for kids, not merely tolerant of them"},
	{LabelCommunity, "a meeting, market or gathering hosted for somebody else"},
}

// labelAliases fold the spellings people actually type onto a standard label.
//
// This is what keeps an open vocabulary from fragmenting where it matters. An
// LLM paraphrasing somebody's Slack message will write "charity" or
// "give-back", and without this each becomes its own label that the Give Back
// page will never match -- the blank-page failure, arrived at by drift rather
// than by typo. Aliases only ever point at the standard set; a label outside it
// is nobody's synonym.
//
// Only unambiguous synonyms belong here. Anything needing a judgement call
// should stay its own label so a person decides.
var labelAliases = map[string]string{
	"give-back":       LabelGiveback,
	"givebacks":       LabelGiveback,
	"charity":         LabelGiveback,
	"fundraiser":      LabelGiveback,
	"quiz":            LabelTrivia,
	"live-music":      LabelMusic,
	"band":            LabelMusic,
	"beer-release":    LabelRelease,
	"new-beer":        LabelRelease,
	"kids":            LabelFamily,
	"kid-friendly":    LabelFamily,
	"family-friendly": LabelFamily,
}

// ParseLabels normalises a caller's labels into a clean set.
//
// Normalising rather than rejecting is deliberate for spelling: labels arrive
// from an LLM reading someone's Slack message, so "Give Back", "give_back" and
// "giveback " all show up meaning the same thing, and a validation error
// teaches the model nothing it will remember next week. Each becomes
// "giveback" instead and the caller gets what they meant.
//
// Order is preserved because the first label a caller writes tends to be the
// main one, and a set that reorders itself between writes reads as a bug.
func ParseLabels(in []string) ([]string, error) {
	if len(in) == 0 {
		return []string{}, nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		l := normalizeLabel(raw)
		if l == "" {
			continue
		}
		if canon, ok := labelAliases[l]; ok {
			l = canon
		}
		if _, dup := seen[l]; dup {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	if len(out) > maxLabels {
		return nil, invalid("at most %d labels per event", maxLabels)
	}
	return out, nil
}

// IsStandardLabel reports whether a label is one of the standard set. Callers
// use it to decide presentation, never to accept or refuse.
func IsStandardLabel(label string) bool {
	for _, s := range StandardLabels {
		if s.Label == label {
			return true
		}
	}
	return false
}

// StandardLabelNames lists just the words, for schemas and error messages.
func StandardLabelNames() []string {
	out := make([]string, len(StandardLabels))
	for i, s := range StandardLabels {
		out[i] = s.Label
	}
	return out
}

// describeLabels renders the standard set for a tool description, so each
// label's meaning travels with the schema instead of living only in this file.
func describeLabels() string {
	var b strings.Builder
	for i, s := range StandardLabels {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s = %s", s.Label, s.Means)
	}
	return b.String()
}

// normalizeLabel lowercases and hyphenates one label so the alias lookup and
// every stored value see a single spelling.
func normalizeLabel(raw string) string {
	var b strings.Builder
	lastHyphen := true // leading hyphens are trimmed by never being written
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if b.Len() >= maxLabelRune {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case r == '-' || r == '_' || r == ' ' || r == '\t' || r == '/' || r == '.':
			// One separator, however many the caller typed.
			if !lastHyphen {
				b.WriteRune('-')
				lastHyphen = true
			}
		default:
			// Anything else (punctuation, emoji, accented letters) is dropped
			// rather than transliterated. A label is a machine key that people
			// happen to read, not a title.
		}
	}
	return strings.Trim(b.String(), "-")
}

// HasLabel reports whether an event carries a label. Labels are stored
// normalised, so the needle goes through the same folding rather than being
// compared raw.
func (e *Event) HasLabel(label string) bool {
	if e == nil {
		return false
	}
	want := normalizeLabel(label)
	if canon, ok := labelAliases[want]; ok {
		want = canon
	}
	if want == "" {
		return false
	}
	return slices.Contains(e.Labels, want)
}
