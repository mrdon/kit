package menu

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Beer descriptions come from a different Untappd than the tap list does.
//
// The digital board (untappd.go) carries names, styles, ABVs and prices, and
// nothing else -- its template has no description field, so no amount of
// parsing will find one. The copy that goes under a beer on a printed menu
// lives on the consumer site instead, on the beer's own untappd.com page.
//
// So this is a second scrape against a second host: the brewery's beer list
// gives a page URL per beer, and each page gives one paragraph. It is only
// ever run for the printed menu -- the wall board has no room for prose --
// and only for beers whose description is not already stored, which in the
// steady state is none of them. A new beer costs two requests, once.

// Three listings, because none of them is complete and each truncates at
// thirty.
//
// That truncation is the whole problem. Every one of these pages returns its
// first thirty beers and no more -- ?page=2 hands back the same thirty, and
// the /brewery/more_beer endpoint the page's own "show more" calls answers an
// anonymous request with nothing -- so a brewery with a deeper catalogue than
// that simply cannot be read in one request. Each sort order, though, returns
// a *different* thirty, which is a way through: ask for several orderings and
// union them.
//
//   - the front page is what has been brewed lately, which is very nearly what
//     is pouring;
//   - /beer is the most-had list, which is the back catalogue and the classics;
//   - /beer?sort=name is alphabetical, which is the only one of the three that
//     has no popularity or recency bias at all, and so is the one that catches
//     the beer that is neither new nor famous.
//
// The third was added after two of Gravity's sixteen taps printed with no
// description for weeks. Both had perfectly good write-ups on Untappd; they
// were simply absent from the first two listings, and nothing said so -- a
// beer that cannot be found and a beer with nothing written about it looked
// identical from here. Three listings covers that board completely.
var untappdBrandURLs = []string{
	"https://untappd.com/%s",
	"https://untappd.com/%s/beer",
	"https://untappd.com/%s/beer?sort=name",
}

const untappdBeerBase = "https://untappd.com"

// notesBudget caps how many beer pages one print may fetch. A first print of
// a full board is seventeen new beers; anything beyond a couple of dozen means
// the name matching has gone wrong and is hammering a third party, so it stops
// and prints what it has rather than hanging on the request.
const notesBudget = 24

// notesParallel is deliberately low. This is someone else's site being read
// for our convenience, and a printed menu is not urgent -- four at a time
// finishes a cold board in a couple of seconds without looking like a scrape.
const notesParallel = 4

var (
	// Untappd's own class name, typo included.
	descLessRe = regexp.MustCompile(`(?s)<div class="beer-descrption-read-less"[^>]*>(.*?)</div>`)
	descMoreRe = regexp.MustCompile(`(?s)<div class="beer-descrption-read-more"[^>]*>(.*?)</div>`)
	beerHrefRe = regexp.MustCompile(`href="(/b/[^"]+)"[^>]*>\s*(?:<[^>]+>\s*)*([^<]{2,80})</`)
	parenRe    = regexp.MustCompile(`\([^)]*\)`)
	nonWordRe  = regexp.MustCompile(`[^a-z0-9 ]+`)
	spaceRe    = regexp.MustCompile(`\s+`)
)

// FetchBeerIndex maps the brewery's beers to their untappd.com pages.
//
// The listing is one page of the brewery's catalogue, which is a superset of
// what is on tap -- retired beers, collaborations brewed under the partner's
// name -- so the map is keyed on a normalised name and matched loosely later.
func FetchBeerIndex(ctx context.Context, client *http.Client, brand string) (map[string]string, error) {
	brand = strings.Trim(strings.TrimSpace(brand), "/")
	if brand == "" {
		return nil, errors.New("no Untappd brewery slug configured")
	}
	index := make(map[string]string)
	var firstErr error
	for _, tmpl := range untappdBrandURLs {
		body, err := getPage(ctx, client, fmt.Sprintf(tmpl, brand))
		if err != nil {
			// One listing being unreachable costs its half of the catalogue,
			// not the whole lookup.
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, m := range beerHrefRe.FindAllStringSubmatch(body, -1) {
			key := normalizeBeerName(cleanText(m[2]))
			if key == "" {
				continue
			}
			// First href wins: a listing repeats a beer in the rails below it,
			// and the catalogue entry comes first.
			if _, seen := index[key]; !seen {
				index[key] = untappdBeerBase + m[1]
			}
		}
	}
	if len(index) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, fmt.Errorf("no beers found on the Untappd pages for %q", brand)
	}
	return index, nil
}

// FetchBeerNote reads one beer page's description. A beer with no description
// written in Untappd returns empty with no error -- that is a fact about the
// beer, not a failure.
func FetchBeerNote(ctx context.Context, client *http.Client, url string) (string, error) {
	body, err := getPage(ctx, client, url)
	if err != nil {
		return "", err
	}
	return parseBeerNote(body), nil
}

// parseBeerNote pulls the description out of a beer page.
//
// The "read less" block holds the full text; "read more" is the truncated
// preview shown on long descriptions. Both end in their own toggle link, which
// is markup rather than copy and comes off with the tags -- except for its
// label, which has to be trimmed by name.
func parseBeerNote(body string) string {
	for _, re := range []*regexp.Regexp{descLessRe, descMoreRe} {
		m := re.FindStringSubmatch(body)
		if len(m) != 2 {
			continue
		}
		note := cleanText(m[1])
		note = strings.TrimSpace(strings.TrimSuffix(note, "Show Less"))
		note = strings.TrimSpace(strings.TrimSuffix(note, "Show More"))
		if note != "" {
			return note
		}
	}
	return ""
}

// AttachNotes fills in each row's description. It returns the ones it had to
// fetch, so the caller can cache them, and the names it could not find a page
// for at all.
//
// Those two failures look identical on paper and are not the same problem. A
// beer with no write-up in Untappd is upstream's to fix; a beer whose page the
// listings never showed us is ours, and it stays broken silently until
// somebody notices a blank row and goes looking. Reporting them apart is what
// makes the second one findable.
//
// A cached note wins outright: a beer is fetched once and read from the store
// forever after, and a description corrected by hand in Kit is not overwritten
// from upstream on the next print. The trade is that fixing a typo in Untappd
// needs the cached note cleared to take effect, which is the right way round --
// the paper is what a customer reads, so Kit gets the last word on it.
//
// A failure to reach Untappd is not a failure to print. The menu is mostly
// prices and names, and a sheet with the descriptions missing is worth far
// more than an error page at the moment somebody wants to print it, so the
// error comes back for logging and the caller carries on.
func AttachNotes(ctx context.Context, client *http.Client, brand string,
	rows []Beer, cache map[string]string) (map[string]string, []string, error) {
	todo := make([]int, 0, len(rows))
	for i := range rows {
		key := normalizeBeerName(rows[i].Name)
		if note, ok := cache[key]; ok && note != "" {
			rows[i].Notes = note
			continue
		}
		if strings.TrimSpace(rows[i].Notes) == "" {
			todo = append(todo, i)
		}
	}
	found := map[string]string{}
	var unmatched []string
	if len(todo) == 0 {
		return found, nil, nil
	}
	index, err := FetchBeerIndex(ctx, client, brand)
	if err != nil {
		return found, nil, err
	}
	if len(todo) > notesBudget {
		todo = todo[:notesBudget]
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, notesParallel)
	)
	for _, i := range todo {
		url, ok := matchBeer(index, rows[i].Name)
		if !ok {
			unmatched = append(unmatched, rows[i].Name)
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			note, err := FetchBeerNote(ctx, client, url)
			if err != nil || note == "" {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			rows[i].Notes = note
			found[normalizeBeerName(rows[i].Name)] = note
		})
	}
	wg.Wait()
	sort.Strings(unmatched)
	return found, unmatched, nil
}

// matchBeer finds a tap's page in the brewery index.
//
// The two names rarely agree exactly: the board says "Newtonian" where the
// catalogue says "Newtonian Amber Ale", and a nitro pour is tagged "(Nitro)"
// on the board and not in the catalogue. So an exact hit is tried first and
// then containment either way, longest candidate first so "Acceleration" does
// not swallow "Barrel Aged Acceleration" -- or rather, so that when both could
// match, the more specific name is not beaten by the shorter one.
func matchBeer(index map[string]string, name string) (string, bool) {
	key := normalizeBeerName(name)
	if key == "" {
		return "", false
	}
	if url, ok := index[key]; ok {
		return url, true
	}
	best, bestLen := "", 0
	for cand, url := range index {
		if !strings.Contains(cand, key) && !strings.Contains(key, cand) {
			continue
		}
		// A one-word overlap between long names is a coincidence, not a match.
		if len(cand) < 4 || len(key) < 4 {
			continue
		}
		if len(cand) > bestLen {
			best, bestLen = url, len(cand)
		}
	}
	return best, best != ""
}

// normalizeBeerName reduces a name to what two spellings of it share:
// lower case, no punctuation, no parenthetical, single spaces.
func normalizeBeerName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = parenRe.ReplaceAllString(s, " ")
	s = nonWordRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

// getPage fetches a consumer Untappd page. The browser-shaped User-Agent is
// not evasion -- the same request from curl's default agent is served the same
// page -- it is so the traffic is identifiable as a normal read rather than
// looking like an unattended crawler.
func getPage(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("building Untappd request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Kit/1.0; +menu-print)")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("untappd %s returned HTTP %d", url, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", url, err)
	}
	return string(raw), nil
}
