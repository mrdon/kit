package events

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Spelling is the forgiving part. Labels arrive from an LLM reading somebody's
// Slack message, so the same intent shows up spelled five ways, and every one
// of them has to land on the same key or the grouping they exist for silently
// splits in two.
func TestParseLabelsFoldsSpellings(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"already canonical", []string{"giveback"}, []string{"giveback"}},
		{"case and space", []string{"Give Back"}, []string{"giveback"}},
		{"underscores", []string{"give_back"}, []string{"giveback"}},
		{"trailing space", []string{"giveback "}, []string{"giveback"}},
		{"runs collapse", []string{"give   back"}, []string{"giveback"}},
		{"edges trimmed", []string{"--give-back--"}, []string{"giveback"}},
		{"emoji dropped", []string{"food 🍕"}, []string{"food"}},
		{"synonym folds", []string{"charity"}, []string{"giveback"}},
		{"quiz folds to trivia", []string{"Quiz"}, []string{"trivia"}},
		{"kids folds to family", []string{"kids"}, []string{"family"}},
		{"empty dropped", []string{"", "   ", "food"}, []string{"food"}},
		{"nil is empty not nil", nil, []string{}},
		{
			"duplicates collapse across spellings",
			[]string{"Give Back", "give_back", "charity", "giveback"},
			[]string{"giveback"},
		},
		{
			"order preserved",
			[]string{"trivia", "food", "family"},
			[]string{"trivia", "food", "family"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseLabels(tc.in)
			if err != nil {
				t.Fatalf("ParseLabels(%q): %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseLabels(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The vocabulary is open. A label outside the standard set is a normal thing
// to want ("tag the Wednesday market nights") and must be kept, not refused.
func TestParseLabelsAcceptsArbitraryLabels(t *testing.T) {
	got, err := ParseLabels([]string{"Wednesday Market", "giveback", "bingo"})
	if err != nil {
		t.Fatalf("ParseLabels: %v", err)
	}
	want := []string{"wednesday-market", LabelGiveback, "bingo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseLabels = %q, want %q", got, want)
	}
}

func TestParseLabelsRejectsTooMany(t *testing.T) {
	many := make([]string, maxLabels+5)
	for i := range many {
		many[i] = fmt.Sprintf("label%d", i)
	}
	_, err := ParseLabels(many)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("ParseLabels with %d labels: err = %v, want ErrInvalid", len(many), err)
	}
}

func TestParseLabelsCapsLength(t *testing.T) {
	long := strings.Repeat("a", 200)
	got, err := ParseLabels([]string{long})
	if err != nil {
		t.Fatalf("ParseLabels: %v", err)
	}
	if len(got) != 1 || len(got[0]) > maxLabelRune {
		t.Errorf("label length %d, want at most %d", len(got[0]), maxLabelRune)
	}
}

// Every standard label must survive a round trip unchanged, or the constants
// and the normaliser have drifted apart.
func TestEveryStandardLabelParsesUnchanged(t *testing.T) {
	for _, l := range StandardLabelNames() {
		got, err := ParseLabels([]string{l})
		if err != nil {
			t.Errorf("ParseLabels(%q): %v", l, err)
			continue
		}
		if !reflect.DeepEqual(got, []string{l}) {
			t.Errorf("ParseLabels(%q) = %q, want it unchanged", l, got)
		}
		if !IsStandardLabel(l) {
			t.Errorf("IsStandardLabel(%q) = false", l)
		}
	}
}

// Aliases are only useful if they land on a standard label, and an alias that
// is also a label of its own would shadow itself.
func TestAliasesPointAtStandardLabels(t *testing.T) {
	for alias, canon := range labelAliases {
		if !IsStandardLabel(canon) {
			t.Errorf("alias %q points at %q, which is not a standard label", alias, canon)
		}
		if IsStandardLabel(alias) {
			t.Errorf("alias %q is also a standard label; one of the two should go", alias)
		}
		if got := normalizeLabel(alias); got != alias {
			t.Errorf("alias %q does not survive normalisation (became %q), so it can never match", alias, got)
		}
	}
}

// A nil return would make the models layer write SQL NULL into a NOT NULL
// column, so the empty case is load-bearing rather than cosmetic.
func TestParseLabelsNeverReturnsNilOnSuccess(t *testing.T) {
	for _, in := range [][]string{nil, {}, {""}, {"   "}} {
		got, err := ParseLabels(in)
		if err != nil {
			t.Fatalf("ParseLabels(%q): %v", in, err)
		}
		if got == nil {
			t.Errorf("ParseLabels(%q) returned nil, want empty slice", in)
		}
	}
}

// HasLabel normalises its needle, so a caller asking for "Give Back" finds an
// event stored as "give-back" rather than quietly getting false.
func TestHasLabelNormalizesTheNeedle(t *testing.T) {
	e := &Event{Labels: []string{LabelGiveback, LabelFood}}
	for _, needle := range []string{"give-back", "Give Back", "give_back", "GIVE-BACK"} {
		if !e.HasLabel(needle) {
			t.Errorf("HasLabel(%q) = false, want true", needle)
		}
	}
	for _, needle := range []string{"trivia", "", "   ", "!!!"} {
		if e.HasLabel(needle) {
			t.Errorf("HasLabel(%q) = true, want false", needle)
		}
	}
	var nilEvent *Event
	if nilEvent.HasLabel("food") {
		t.Error("HasLabel on a nil event = true, want false")
	}
}

// The feed is the contract the website builds against. A label that does not
// survive the round trip is a label the site cannot group on.
func TestLabelsReachTheFeed(t *testing.T) {
	sf := newSyncFixture(t)

	tagged := sf.create(t, CreateParams{Title: "Give Back Monday", Visibility: VisibilityPublic,
		Labels: []string{"Give Back"}})
	sf.publish(t, tagged)
	plain := sf.create(t, CreateParams{Title: "Quiz Night", Visibility: VisibilityPublic})
	sf.publish(t, plain)

	f, err := sf.svc.BuildFeed(sf.ctx, sf.tenant.ID, "")
	if err != nil {
		t.Fatalf("BuildFeed: %v", err)
	}
	got := map[string][]string{}
	for _, e := range f.Events {
		got[e.Title] = e.Labels
	}
	if !reflect.DeepEqual(got["Give Back Monday"], []string{"giveback"}) {
		t.Errorf("feed labels = %q, want [giveback]", got["Give Back Monday"])
	}
	// Omitted rather than an empty array, so an unlabelled event's payload is
	// byte-identical to what it was before labels existed.
	if len(got["Quiz Night"]) != 0 {
		t.Errorf("unlabelled event carried labels %q", got["Quiz Night"])
	}
}

// Folding has to happen where writes actually happen, not only in the parser,
// or a caller reaching the service directly stores a spelling the website will
// never match.
func TestCreateFoldsLabelsOnWrite(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Charity Night", Labels: []string{"Charity", "MARKET day"}})
	want := []string{LabelGiveback, "market-day"}
	if !reflect.DeepEqual(e.Labels, want) {
		t.Errorf("stored labels = %q, want %q", e.Labels, want)
	}
}

// Nil means "leave alone" and empty means "clear". A single []string could not
// tell those apart, and they are opposite instructions.
func TestUpdateLabelsReplacesAndClears(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Give Back Monday", Labels: []string{"giveback"}})

	untouched, err := sf.svc.Update(sf.ctx, sf.tenant.ID, e.ID, UpdateParams{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !reflect.DeepEqual(untouched.Labels, []string{"giveback"}) {
		t.Errorf("labels = %q after an unrelated update, want them left alone", untouched.Labels)
	}

	replaced, err := sf.svc.Update(sf.ctx, sf.tenant.ID, e.ID,
		UpdateParams{Labels: &[]string{"food", "family"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !reflect.DeepEqual(replaced.Labels, []string{"food", "family"}) {
		t.Errorf("labels = %q, want the whole set replaced", replaced.Labels)
	}

	cleared, err := sf.svc.Update(sf.ctx, sf.tenant.ID, e.ID, UpdateParams{Labels: &[]string{}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(cleared.Labels) != 0 {
		t.Errorf("labels = %q, want an empty list to clear them", cleared.Labels)
	}
}

// "The same again" is the main way a recurring programme gets its next night,
// so a copy that lost its labels would drop off the page it was made for.
func TestCloneCopiesLabels(t *testing.T) {
	sf := newSyncFixture(t)
	src := sf.create(t, CreateParams{Title: "Give Back Monday", Labels: []string{"giveback"}})

	dup, err := sf.svc.Clone(sf.ctx, sf.tenant.ID, src.ID, CloneParams{})
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if !reflect.DeepEqual(dup.Labels, []string{"giveback"}) {
		t.Errorf("clone labels = %q, want [giveback]", dup.Labels)
	}

	// The struct copy shares the backing array; editing one row's labels must
	// not reach into the other's.
	if _, err := sf.svc.Update(sf.ctx, sf.tenant.ID, dup.ID,
		UpdateParams{Labels: &[]string{"food"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	reloaded, err := sf.svc.Get(sf.ctx, sf.tenant.ID, src.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(reloaded.Labels, []string{"giveback"}) {
		t.Errorf("original labels = %q after editing the copy, want [giveback]", reloaded.Labels)
	}
}

// The console hardcodes the preset list in TypeScript, following the same
// pattern the prominence options already use. That is a duplicate of
// StandardLabels, and a duplicate nobody checks is a duplicate that drifts --
// so this reads the actual file. Cheaper than an endpoint plus a load state
// for seven words that change about once a year.
func TestConsoleStandardLabelsMatchGo(t *testing.T) {
	const consoleAPI = "../../../web/console/src/api.ts"
	src, err := os.ReadFile(consoleAPI)
	if err != nil {
		t.Fatalf("reading %s: %v", consoleAPI, err)
	}
	block := string(src)
	start := strings.Index(block, "export const STANDARD_LABELS")
	if start < 0 {
		t.Fatalf("STANDARD_LABELS not found in %s", consoleAPI)
	}
	end := strings.Index(block[start:], "];")
	if end < 0 {
		t.Fatalf("STANDARD_LABELS block is unterminated in %s", consoleAPI)
	}
	block = block[start : start+end]

	found := regexp.MustCompile(`label:\s*'([^']+)'`).FindAllStringSubmatch(block, -1)
	got := make([]string, len(found))
	for i, m := range found {
		got[i] = m[1]
	}
	want := StandardLabelNames()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("console STANDARD_LABELS = %q, want %q (edit %s to match labels.go)",
			got, want, consoleAPI)
	}
}
