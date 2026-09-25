package menu

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fixtureClock is a moment at which the saved board fixture has at least one
// badged tap: a day after the newest row it still shows.
//
// Read off the fixture rather than written down, for two reasons. The dates in
// a captured page are fixed, so a test that asked "is this new today" would
// answer no from the day after it was written. And the newest DATE in the page
// is not necessarily the newest TAP -- the board drops a row with no price at
// all -- so a literal copied out of the markup can name an item that never
// reaches the wall, which is how this test first failed.
func fixtureClock(t *testing.T, taps []Tap) time.Time {
	t.Helper()
	var newest time.Time
	for _, tp := range taps {
		if tp.AddedAt.After(newest) {
			newest = tp.AddedAt
		}
	}
	if newest.IsZero() {
		t.Fatal("no tap in the fixture carries an added-on date")
	}
	return newest.AddDate(0, 0, 1)
}

// freezeClock parks the package clock for one test.
func freezeClock(t *testing.T, at time.Time) {
	t.Helper()
	prev := timeNow
	timeNow = func() time.Time { return at }
	t.Cleanup(func() { timeNow = prev })
}

// TestBoardCarriesTheAddedDate is the whole feature's premise: Untappd already
// tells us when a beer went up, so nothing in Kit has to remember it. If this
// fails the badge is not wrong, it is absent — every tap goes unbadged, which
// is silent on the wall.
func TestBoardCarriesTheAddedDate(t *testing.T) {
	taps := ParseUntappdBoard(loadFixture(t))
	if len(taps) == 0 {
		t.Fatal("no taps parsed")
	}
	var dated int
	for _, tp := range taps {
		if !tp.AddedAt.IsZero() {
			dated++
		}
	}
	if dated != len(taps) {
		t.Errorf("%d of %d taps carry an added-on date; the item-status element "+
			"is how the New badge is dated, so a tap without one can never be badged",
			dated, len(taps))
	}
}

// TestAddedDateSurvivesToTheWall walks the date the whole way a real sync does
// — scrape, encode, store, decode — because the badge is decided on the far
// side of the stored payload. A field parsed and then dropped by ParseBoard's
// DisallowUnknownFields would look perfect in a parser test and badge nothing.
func TestAddedDateSurvivesToTheWall(t *testing.T) {
	taps := ParseUntappdBoard(loadFixture(t))
	payload, err := json.Marshal(&Board{Taps: taps})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	board, err := ParseBoard(payload)
	if err != nil {
		t.Fatalf("ParseBoard: %v", err)
	}
	for i, tp := range board.Taps {
		if !tp.AddedAt.Equal(taps[i].AddedAt) {
			t.Fatalf("tap %q lost its date in the round trip: %v -> %v",
				tp.Name, taps[i].AddedAt, tp.AddedAt)
		}
	}
}

func TestIsNewSpansExactlyAWeek(t *testing.T) {
	added := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"the hour it went up", added, true},
		{"a day later", added.AddDate(0, 0, 1), true},
		{"a minute short of a week", added.Add(NewFor - time.Minute), true},
		{"a minute past a week", added.Add(NewFor + time.Minute), false},
		{"a month later", added.AddDate(0, 1, 0), false},
		// A board edited on a machine whose clock is ahead, or a date Untappd
		// stamps a moment into the future. Brand new is new.
		{"stamped slightly ahead of us", added.Add(-time.Minute), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			freezeClock(t, c.now)
			if got := (Tap{AddedAt: added}).IsNew(); got != c.want {
				t.Errorf("IsNew() = %v, want %v", got, c.want)
			}
		})
	}
}

// A hand-authored tap list carries no dates, and a board stored before this
// field existed carries none either. Neither should light up.
func TestUndatedTapIsNeverNew(t *testing.T) {
	if (Tap{Name: "Newtonian"}).IsNew() {
		t.Error("a tap with no added-on date must not be badged")
	}
}

func TestParseAddedAtIgnoresWhatItCannotRead(t *testing.T) {
	for _, item := range []string{
		`<div class="item"><span class="name">Newtonian</span></div>`,
		`<item-status created-at="" class="item-tag">New</item-status>`,
		`<item-status created-at="last Tuesday" class="item-tag">New</item-status>`,
	} {
		if got := parseAddedAt(item); !got.IsZero() {
			t.Errorf("parseAddedAt(%q) = %v, want the zero time", item, got)
		}
	}
}

// TestBadgeRendersOnlyOnNewTaps checks the markup, not just the predicate: the
// badge belongs inside the fitted beer name, which is what makes the width pass
// measure the pill along with the letters. Hung outside .tap-name, a long name
// would fit its column and shove the badge past the edge of it.
func TestBadgeRendersOnlyOnNewTaps(t *testing.T) {
	added := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	freezeClock(t, added.AddDate(0, 0, 2))

	b := &Board{Taps: []Tap{
		{Section: "Pale Ales & IPAs", Name: "Third Stage", Style: "IPA - Hazy",
			ABV: "6.8%", Price: "8", Size: DefaultPour, AddedAt: added},
		{Section: "Pale Ales & IPAs", Name: "Newtonian", Style: "Amber Ale",
			ABV: "5.4%", Price: "7", Size: DefaultPour,
			AddedAt: added.AddDate(0, -3, 0)},
	}}
	html, err := Render(b, nil, "v1")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if n := strings.Count(html, `class="tap-new"`); n != 1 {
		t.Fatalf("%d badges rendered, want exactly 1 (the week-old tap)", n)
	}
	// The badge must sit inside the fitted name element, after the name, and
	// before that element closes.
	if !strings.Contains(html,
		`<p class="tap-name" data-fit>Third Stage<span class="tap-new">New</span></p>`) {
		t.Errorf("badge is not inside .tap-name after the name; the width fit "+
			"pass only measures what is inside [data-fit]:\n%s",
			html[strings.Index(html, "Third Stage")-200:strings.Index(html, "Third Stage")+200])
	}
	if !strings.Contains(stylesheetOrDie(t), ".tap-new {") {
		t.Error("no .tap-new rule in the stylesheet: the badge would render as bare text")
	}
}

// TestBadgeSizeRidesTheFitPass pins the badge to --meta-size, which is the one
// thing that makes it behave in both directions. It is a child of a 46px name,
// so an em would draw a 38px pill shouting over the beer it labels; and
// --meta-size is a fit-pass variable, so a fixed px would stay put while a long
// tap list shrank everything around it.
func TestBadgeSizeRidesTheFitPass(t *testing.T) {
	css := stylesheetOrDie(t)
	i := strings.Index(css, ".tap-new {")
	if i < 0 {
		t.Fatal("no .tap-new rule")
	}
	body := css[i : i+strings.Index(css[i:], "}")]
	if !strings.Contains(body, "font-size: var(--meta-size") {
		t.Errorf(".tap-new font-size must come off --meta-size: %q", body)
	}
	// calc() is deliberately absent — see the rule's comment; the wall panel's
	// browser predates it, and a badge that fails to size is a badge at the
	// browser's 16px default in the middle of a 46px name.
	if strings.Contains(body, "calc(") {
		t.Errorf(".tap-new must not use calc(): %q", body)
	}
	// The pill is centred on the name's cap height by a measured
	// vertical-align, and that measurement assumes symmetric padding: the
	// three-value form puts the lettering off its own box centre, so the pill
	// reads as misaligned however well the box is placed.
	if !strings.Contains(body, "padding: .18em .42em;") {
		t.Errorf(".tap-new padding must stay symmetric (top/bottom equal), "+
			"or the measured vertical-align no longer centres it: %q", body)
	}
	if !strings.Contains(body, "vertical-align: .5em") {
		t.Errorf(".tap-new must keep its measured vertical-align: %q", body)
	}
}

// TestVersionMovesWhenABadgeExpires is the reason the count is in the version
// stamp at all. A screen reloads when the stamp moves, and nothing else in the
// stamp moves as a beer simply gets older — so without this a tap that stopped
// being new on Tuesday would wear its badge until the next keg blew.
func TestVersionMovesWhenABadgeExpires(t *testing.T) {
	added := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(&Board{Taps: []Tap{
		{Section: "Specialty", Name: "Third Stage", Price: "8", AddedAt: added},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	row := &BoardRow{UpdatedAt: added, Payload: payload}

	freezeClock(t, added.AddDate(0, 0, 2))
	badged := boardVersion(row)

	freezeClock(t, added.Add(NewFor+time.Hour))
	expired := boardVersion(row)

	if badged == expired {
		t.Errorf("version is %q both while the badge is up and after it expires; "+
			"a screen would never reload to drop it", badged)
	}
	// The render stamp has to stay the suffix — TestVersionCarriesTheRenderStamp
	// and anyone reading a version off a misbehaving screen both rely on it.
	if !strings.HasSuffix(expired, "."+RenderStamp()) {
		t.Errorf("version %q no longer ends in the render stamp", expired)
	}
}

// A payload that no longer parses still has to yield a version: the poll runs
// every thirty seconds per screen and a 500 there stops a screen noticing any
// future fix.
func TestNewTapCountToleratesRubbish(t *testing.T) {
	for _, payload := range []string{``, `{}`, `not json`, `{"taps":null}`} {
		if got := newTapCount([]byte(payload)); got != 0 {
			t.Errorf("newTapCount(%q) = %d, want 0", payload, got)
		}
	}
}

// The badge on the page and the count in the version stamp are two readings of
// the same question, and they must not disagree — a stamp that says one badge
// while the page draws none is a screen that reloads forever, or never.
func TestCountAgreesWithTheRenderedBadges(t *testing.T) {
	taps := ParseUntappdBoard(loadFixture(t))
	freezeClock(t, fixtureClock(t, taps))

	payload, err := json.Marshal(&Board{Taps: taps})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var badged int
	for _, tp := range taps {
		if tp.IsNew() {
			badged++
		}
	}
	if badged == 0 {
		t.Fatal("no tap is badged a day after the fixture's newest row went up")
	}
	if got := newTapCount(payload); got != badged {
		t.Errorf("newTapCount = %d, but %d taps report IsNew", got, badged)
	}
}

func stylesheetOrDie(t *testing.T) string {
	t.Helper()
	css, err := stylesheet()
	if err != nil {
		t.Fatalf("stylesheet: %v", err)
	}
	return css
}
