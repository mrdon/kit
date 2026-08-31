package menu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Untappd is the tap list's upstream. Staff curate the board there — it is
// what feeds the Untappd app, so it gets kept current whether or not we read
// it — and this pulls that curation across rather than asking anyone to
// maintain the same seventeen beers twice.
//
// It is a SCRAPER, with a scraper's contract: it breaks when Untappd reskins
// the board. Untappd's own API would be the clean way in, but it is gated to
// their Premium tier, and the board page is server-rendered — the whole menu
// arrives in the HTML as a JSON-escaped blob that the page's bundle unpacks —
// so a plain GET has everything without auth.
//
// Because it can break, it fails LOUDLY and atomically: a parse that yields
// implausibly few taps returns an error and the stored board is left alone.
// A menu board that quietly drops half the taps is worse than one that is a
// few hours stale, because staff will pour from it.

const untappdBoardURL = "https://business.untappd.com/boards/%s"

// minPlausibleTaps is the tripwire. A real board that genuinely fell to two
// beers is a phone call, not a scrape; this many rows almost certainly means
// the markup moved under us.
const minPlausibleTaps = 5

// ErrSourceUnchanged reports that the upstream board matches what is stored,
// so nothing needed writing.
var ErrSourceUnchanged = errors.New("tap list unchanged")

// ErrScrapeImplausible reports a parse that produced too little to trust.
var ErrScrapeImplausible = errors.New("parsed too few taps from the Untappd board")

var (
	// The payload arrives JSON-escaped inside the page bootstrap.
	unicodeEsc = regexp.MustCompile(`\\u([0-9a-fA-F]{4})`)
	sectionRe  = regexp.MustCompile(`(?s)<div class="section-title"[^>]*>(.*?)</div>`)
	// Go's RE2 has no lookahead, so an item's extent is found by scanning to
	// the next boundary rather than by a non-consuming match.
	itemStartRe = regexp.MustCompile(`<div class="item"[^>]*>`)
	footerRe    = regexp.MustCompile(`<div class="footer"`)
	nameRe      = regexp.MustCompile(`(?s)<span class="name">(.*?)</span>`)
	styleRe     = regexp.MustCompile(`(?s)<div class="item-style">(.*?)</div>`)
	abvRe       = regexp.MustCompile(`(?s)<span class="abv">(.*?)</span>`)
	containerRe = regexp.MustCompile(`(?s)<div class="container"><div class="price">(.*?)</div><div class="name">(.*?)</div>`)
	tagRe       = regexp.MustCompile(`<[^>]+>`)
)

// draftRank orders draft pours largest first. Draft wins the headline column
// outright: a growler is takeaway, and showing its price beside a beer name
// would say a pint costs twenty-six dollars.
//
// When a beer has NO draft price -- packaged-only, or simply not priced on
// the board -- the largest other container is shown with its own label, so
// the row says what Untappd says rather than inventing a number. A beer with
// no price of any kind is dropped from the list entirely; the fix for that
// belongs in Untappd.
var draftRank = map[string]int{"16oz": 4, "12oz": 3, "9oz": 2, "4oz": 1}

// FetchUntappdBody returns the raw board page and a hash of it.
//
// Untappd serves the board with `cache-control: no-cache` and no ETag or
// Last-Modified, so there is no conditional request to make -- the bytes come
// down either way. Hashing them is what makes the common case cheap: an
// unchanged board skips the parse, the validate, the JSON encode and the
// UPDATE, which is all of the work except the transfer.
func FetchUntappdBody(ctx context.Context, client *http.Client, boardID string) (body string, hash string, err error) {
	boardID = strings.TrimSpace(boardID)
	if boardID == "" {
		return "", "", errors.New("no Untappd board id configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf(untappdBoardURL, boardID), nil)
	if err != nil {
		return "", "", fmt.Errorf("building Untappd request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Kit/1.0; +menu-board)")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetching Untappd board %s: %w", boardID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("untappd board %s returned HTTP %d", boardID, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", "", fmt.Errorf("reading Untappd board: %w", err)
	}
	sum := sha256.Sum256(raw)
	return string(raw), hex.EncodeToString(sum[:]), nil
}

// ParseUntappdBoard extracts the tap list from a board page's HTML.
func ParseUntappdBoard(raw string) []Tap {
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

	var taps []Tap
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
		price, size := headlinePour(mk.item)
		// No price, no row. A beer nobody can be quoted from the board is
		// just a question for the bartender, and an empty price column reads
		// as a mistake rather than as information. If it should be on the
		// wall, it needs a price in Untappd.
		if price == "" {
			continue
		}
		taps = append(taps, Tap{
			Section: section,
			Name:    name,
			Style:   firstGroup(styleRe, mk.item),
			ABV:     firstGroup(abvRe, mk.item),
			Price:   price,
			Size:    size,
		})
	}
	return taps
}

// headlinePour picks the one price the board has room for. Untappd lists
// every container a beer comes in; a row shows one.
func headlinePour(item string) (price, size string) {
	bestDraft := 0
	var fallbackPrice, fallbackSize string
	for _, m := range containerRe.FindAllStringSubmatch(item, -1) {
		p, label := cleanText(m[1]), cleanText(m[2])
		fields := strings.Fields(label)
		if len(fields) == 0 || p == "" {
			continue
		}
		size0 := strings.ToLower(fields[0])
		// Draft is decided by the word, not the size. Untappd labels are
		// "16oz Draft", "64oz Growler", "12oz Can" -- keying on the size alone
		// made a 12oz can outrank a 9oz pour and take the pint column.
		if strings.Contains(strings.ToLower(label), "draft") {
			if rank, ok := draftRank[size0]; ok && rank > bestDraft {
				bestDraft, price, size = rank, p, size0
			}
			continue
		}
		// Not a draft pour. Keep the first as a fallback for a beer that has
		// no draft price at all.
		if fallbackPrice == "" {
			fallbackPrice, fallbackSize = p, strings.ToLower(label)
		}
	}
	if bestDraft == 0 {
		return fallbackPrice, fallbackSize
	}
	return price, size
}

// unescapePayload undoes the JSON escaping the board markup arrives in.
func unescapePayload(raw string) string {
	decoded := unicodeEsc.ReplaceAllStringFunc(raw, func(m string) string {
		n, err := strconv.ParseInt(m[2:], 16, 32)
		if err != nil {
			return m
		}
		return string(rune(n))
	})
	return strings.ReplaceAll(decoded, `\"`, `"`)
}

func firstGroup(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return cleanText(m[1])
}

func cleanText(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(
		html.UnescapeString(tagRe.ReplaceAllString(s, " ")), " ", " "))
}

// untappdClient is the HTTP client the scheduled sync uses. Untappd is a
// third party on the critical path of a wall display, so the timeout is short
// and there is no retry: the next tick is minutes away and a stale board is
// the correct behaviour in the meantime.
func untappdClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second}
}
