package events

import (
	"strings"
	"testing"
)

func TestTrimBandEcho(t *testing.T) {
	wed := bandFacts{Weekday: "Wednesday", Time: "6:30pm", Title: "Trivia with Geeks Who Drink"}
	tests := []struct {
		name   string
		bullet string
		band   bandFacts
		want   string
	}{{
		name:   "the day and the door time together",
		bullet: "Quiz night every Wednesday at 6:30pm",
		band:   wed,
		want:   "Quiz night",
	}, {
		name:   "a trailing day, keeping the punctuation after it",
		bullet: "Free to play, no sign-up needed, every Wednesday.",
		band:   wed,
		want:   "Free to play, no sign-up needed",
	}, {
		name:   "the spelling of the time does not matter",
		bullet: "Doors from 6:30 p.m.",
		band:   wed,
		want:   "Doors",
	}, {
		// The AFL band opens at 9:30pm and bounces at 10:30pm. The second of
		// those is the entire point of the line.
		name:   "a time that is not the band's own time",
		bullet: "Live from Melbourne, bounce at 10:30pm",
		band:   bandFacts{Weekday: "Friday", Time: "9:30pm"},
		want:   "Live from Melbourne, bounce at 10:30pm",
	}, {
		// A day phrase that runs into the rest of the sentence is carrying
		// grammar. Cutting it would leave "One hour in the taproom for the
		// price of a pint" reading as if an hour costs a pint, which is worse
		// than the repetition it fixed.
		name:   "a day carrying grammar is left alone",
		bullet: "One hour on a Thursday night in the taproom",
		band:   bandFacts{Weekday: "Thursday", Time: "6:30pm"},
		want:   "One hour on a Thursday night in the taproom",
	}, {
		// The same phrase closing a clause is safe to take, and "night" goes
		// with it rather than being left stranded.
		name:   "a day closing a clause goes cleanly",
		bullet: "One hour on a Thursday night, in the taproom",
		band:   bandFacts{Weekday: "Thursday", Time: "6:30pm"},
		want:   "One hour, in the taproom",
	}, {
		name:   "an on-the-hour time matches its written-out form",
		bullet: "Kitchen open from 4:00 PM",
		band:   bandFacts{Weekday: "Monday", Time: "4pm"},
		want:   "Kitchen open",
	}, {
		name:   "a dangling conjunction goes with the clause it introduced",
		bullet: "Pizza deals and every Monday",
		band:   bandFacts{Weekday: "Monday", Time: "4pm"},
		want:   "Pizza deals",
	}, {
		name:   "a bullet that is only the title again",
		bullet: "Trivia with Geeks Who Drink.",
		band:   wed,
		want:   "",
	}, {
		name:   "an all-day band has no time to echo",
		bullet: "Doors at 6:30pm",
		band:   bandFacts{Weekday: "Saturday"},
		want:   "Doors at 6:30pm",
	}, {
		name:   "nothing to take out",
		bullet: "Buy one get one free on any sourdough pizza",
		band:   bandFacts{Weekday: "Monday", Time: "4pm"},
		want:   "Buy one get one free on any sourdough pizza",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trimBandEcho(tt.bullet, tt.band); got != tt.want {
				t.Fatalf("trimBandEcho(%q) = %q, want %q", tt.bullet, got, tt.want)
			}
		})
	}
}

// A one-word remainder is not worth a band's line: "Doors" says nothing the
// band's own 6:30PM did not.
func TestKeepsALine(t *testing.T) {
	for _, s := range []string{"", "Doors", "  "} {
		if keepsALine(s) {
			t.Fatalf("%q should not earn a line", s)
		}
	}
	if !keepsALine("Quiz night") {
		t.Fatal("two words should earn a line")
	}
}

// Which copy a band draws from. A description of paragraphs is web prose whose
// opening line is a lede; a description of short lines is already a list.
func TestTopperSource(t *testing.T) {
	list := &Event{
		Summary:     "Buy any sourdough pizza and get one free when you dine in.",
		Description: "Buy one get one free on any sourdough pizza\nDine in only\nMembers only",
	}
	if got := topperSource(list); got[0] != "Buy one get one free on any sourdough pizza" || len(got) != 3 {
		t.Fatalf("a written list should win: %q", got)
	}

	// Two short paragraphs are still paragraphs. Both of these fit under the
	// length limit, so only the sentence count separates them from a list.
	shortProse := &Event{
		Summary:     "Live from Melbourne. Bounce at 10:30pm, free entry, open late.",
		Description: "The AFL Grand Final is the biggest day on the Australian calendar.\nNormal Friday hours up to then. Free entry, no booking, everyone welcome.",
	}
	if got := topperSource(shortProse); got[0] != "Live from Melbourne" {
		t.Fatalf("short paragraphs should still lose to the summary: %q", got)
	}

	prose := &Event{
		Summary:     "Tabletop D&D with professional Game Masters. Free to play.",
		Description: "Battles & Brews brings professional Dungeons & Dragons to your favorite local taprooms, delivering the adventure directly to you.\n\nNo experience is required, and all the gear is provided.",
	}
	if got := topperSource(prose); got[0] != "Tabletop D&D with professional Game Masters" {
		t.Fatalf("paragraphs should lose to the summary: %q", got)
	}
}

func TestSplitSentences(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"Quiz night every Wednesday. Free to play.", []string{"Quiz night every Wednesday", "Free to play"}},
		// The cut that printed "HOME OPENER VS" on a band with room for one
		// line, throwing away the only line it had.
		{"Home opener vs. Jacksonville. Broncos gear gets you $2 off.",
			[]string{"Home opener vs. Jacksonville", "Broncos gear gets you $2 off"}},
		{"Doors at 7. Music from 8.", []string{"Doors at 7", "Music from 8"}},
		{"Hosted by J. Wilson. Free entry.", []string{"Hosted by J. Wilson", "Free entry"}},
		{"On E. 9th Ave. Parking out back.", []string{"On E. 9th Ave. Parking out back"}},
		{"One sentence only", []string{"One sentence only"}},
	}
	for _, tt := range tests {
		got := strings.Join(splitSentences(tt.in), "|")
		if want := strings.Join(tt.want, "|"); got != want {
			t.Errorf("splitSentences(%q)\n got %q\nwant %q", tt.in, got, want)
		}
	}
}

// The summary's budget is what the printed band can hold. Advice, not
// validation: the event still publishes.
func TestSummaryAdvice(t *testing.T) {
	ok := []string{
		"",
		"Tabletop D&D with professional Game Masters. Free to play.",
		"Home opener vs. Jacksonville. Broncos gear gets you $2 off your first beer.",
		"Live from Melbourne. Bounce at 10:30pm, free entry, open late.",
	}
	for _, s := range ok {
		if got := SummaryAdvice(s); got != "" {
			t.Errorf("SummaryAdvice(%q) = %q, want no advice", s, got)
		}
	}

	// One long opening sentence: the band sharing its day prints this and
	// nothing else, so it is the sentence that has to be short.
	long := "Our Third Stage Triple IPA lifts off at last, rescheduled from the anniversary party in September."
	if got := SummaryAdvice(long); !strings.Contains(got, "first sentence") {
		t.Errorf("SummaryAdvice(long lede) = %q, want advice about the opening sentence", got)
	}

	// Short sentences that add up past the band.
	many := "Beer is on. Food is on. Music is on. Games are on. Prizes are on. Everyone is welcome here."
	if got := SummaryAdvice(many); !strings.Contains(got, "room for about") {
		t.Errorf("SummaryAdvice(long overall) = %q, want advice about the total", got)
	}
}
