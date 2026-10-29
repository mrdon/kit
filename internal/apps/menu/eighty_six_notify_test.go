package menu

import (
	"slices"
	"strings"
	"testing"

	"github.com/mrdon/kit/internal/services"
)

func TestNewlyEightySixedIsOnlyWhatWasAdded(t *testing.T) {
	// Spacing and case differ from what is stored: a re-save of the same
	// beer must not post it again.
	got := newlyEightySixed([]string{"Galactic Orbit"}, []string{"galactic  orbit", "Cerveza Espacial"})
	if !slices.Equal(got, []string{"Cerveza Espacial"}) {
		t.Errorf("added = %v, want only Cerveza Espacial", got)
	}
	if got := newlyEightySixed([]string{"A", "B"}, []string{"A"}); len(got) != 0 {
		t.Errorf("bringing a beer back added %v, want nothing", got)
	}
}

func TestEightySixWhoNamesTheActor(t *testing.T) {
	cases := []struct {
		caller *services.Caller
		want   string
	}{
		{&services.Caller{Kind: services.CallerUser, Identity: "U123"}, " by <@U123>"},
		{&services.Caller{Kind: services.CallerDevice, Label: "Bar <iPad>"}, " on Bar &lt;iPad&gt;"},
		{&services.Caller{Kind: services.CallerAgent, Identity: "U123", Label: "Keg check job"}, " by Keg check job"},
		{&services.Caller{Kind: services.CallerDevice}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := eightySixWho(c.caller); got != c.want {
			t.Errorf("who(%+v) = %q, want %q", c.caller, got, c.want)
		}
	}
}

func TestEightySixTextEscapesBeerNames(t *testing.T) {
	got := eightySixText([]string{"Hazy <!channel> IPA", "Stout"}, " by <@U1>")
	if strings.Contains(got, "<!channel>") {
		t.Errorf("text %q would ping the channel", got)
	}
	if !strings.Contains(got, "*Stout*") || !strings.HasSuffix(got, " 86'd by <@U1>") {
		t.Errorf("text %q is missing the beer or who", got)
	}
}
