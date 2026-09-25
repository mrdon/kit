package menu

import (
	"encoding/json"
	"regexp"
	"time"
)

// The "New" badge on the wall board.
//
// Untappd already knows when a beer went up, so nothing here has to remember
// it. Every item in the board payload carries
//
//	<item-status created-at="2026-09-12T18:46:19Z" duration-minutes="120"
//	             class="item-tag">New</item-status>
//
// which is when that row was added to the board. Untappd's own bundle shows
// the tag for `duration-minutes` after that date -- two hours, a "just tapped"
// flash for somebody refreshing the app, not the week a taproom means by new
// -- and this board does not run their bundle anyway. So the date is taken
// from the feed and the window is ours.
//
// It is the ITEM's age, not the beer's. Deleting a row in Untappd and adding
// it back resets it, which is the right answer more often than not: a seasonal
// swapped back in after a month off is news to a customer. But a board
// reshuffled by hand goes all-new for a week, and that is the thing to
// remember if every tap lights up at once -- the fix is upstream, in how the
// board was edited, and it ages out on its own.
//
// Nothing is stored for this. The date rides in the board payload next to the
// price, and whether a row is new is decided at render time, which is what
// lets the badge expire without an edit upstream.

// NewFor is how long a tap wears the badge. A week is one full cycle of a
// taproom's regulars: somebody who comes in on Fridays sees every new beer
// badged exactly once.
const NewFor = 7 * 24 * time.Hour

// timeNow is indirected for tests, which need a clock parked beside the saved
// board fixture's dates rather than whatever today happens to be.
var timeNow = time.Now

// itemStatusRe pulls the added-on date off one board item.
var itemStatusRe = regexp.MustCompile(`<item-status[^>]*created-at="([^"]+)"`)

// parseAddedAt reads when a board item went up.
//
// A missing or unreadable date returns the zero time, which reads as "not new"
// everywhere downstream. An unbadged beer is the status quo, so a date we
// cannot parse costs a badge rather than breaking a row -- and it has to,
// because this element is Untappd's markup and can move under us the same way
// the rest of the scrape can.
func parseAddedAt(item string) time.Time {
	m := itemStatusRe.FindStringSubmatch(item)
	if len(m) < 2 {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, m[1])
	if err != nil {
		return time.Time{}
	}
	return t
}

// isNewAt is the one place the window is applied, so the badge on the page and
// the count in the version stamp cannot disagree about which taps are new.
func isNewAt(added, now time.Time) bool {
	return !added.IsZero() && now.Sub(added) < NewFor
}

// IsNew reports whether this tap should wear the badge. Called from the
// template, so it takes the clock rather than being handed a decision: the
// page is rendered per request and the answer is a function of when.
func (t Tap) IsNew() bool {
	return isNewAt(t.AddedAt, timeNow())
}

// newTapCount counts badged taps in a stored payload, for the version stamp.
//
// A badge has to be able to go out on its own. A screen reloads when the
// version stamp moves, and the rest of that stamp is updated_at plus the
// render stamp -- neither of which moves as a beer simply gets older. Without
// this, a tap that stopped being new on Tuesday would keep its badge on the
// wall until the next time a keg blew.
//
// A count is enough to notice. Nothing becomes new without the payload
// changing and carrying updated_at with it, so between two payloads this can
// only fall, and every fall is a badge expiring.
//
// It parses the payload itself rather than taking a *Board because the version
// is answered on a poll every thirty seconds per screen, and a malformed
// payload must yield a version rather than an error -- ParseBoard's job is to
// refuse a bad board, and this one's is to stamp whatever is stored.
func newTapCount(payload []byte) int {
	var doc struct {
		Taps []struct {
			AddedAt time.Time `json:"added_at"`
		} `json:"taps"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return 0
	}
	now := timeNow()
	n := 0
	for _, t := range doc.Taps {
		if isNewAt(t.AddedAt, now) {
			n++
		}
	}
	return n
}
