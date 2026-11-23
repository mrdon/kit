package agents

import (
	"math"
	"testing"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
)

// A wider band raises a seat's hit rate the way a Normal aim would.
func TestBandSkill(t *testing.T) {
	sigma := 0.1
	hit := func(w float64) float64 { return math.Erf(w / 2 / (sigma * math.Sqrt2)) }
	p := hit(0.12)
	if got := bandSkill(p, 0.12, 0.12); got != p {
		t.Errorf("same band: %g, want %g", got, p)
	}
	if got, want := bandSkill(p, 0.12, 0.24), hit(0.24); math.Abs(got-want) > 1e-9 {
		t.Errorf("double band: %g, want %g", got, want)
	}
	if bandSkill(0, 0.12, 0.5) != 0 || bandSkill(1, 0.12, 0.5) != 1 {
		t.Error("no-skill and perfect seats should not change")
	}
}

// Every race carries consistent fun numbers.
func TestFunStats(t *testing.T) {
	cfg := engine.Default().WithRandomSpecials(1, 1)
	seats, err := Crowd(4, "human", 9)
	if err != nil {
		t.Fatal(err)
	}
	for i := range seats {
		seats[i].Skill = 0.5
	}
	r, err := Play(cfg, 9, seats)
	if err != nil {
		t.Fatal(err)
	}
	for _, rc := range r.Races {
		f := rc.Fun
		if len(f.Stuck) != rc.Frogs || len(f.LapSpreads) != rc.Hops/rc.Frogs || len(f.LapLast) != len(f.LapSpreads) {
			t.Errorf("race %d: %d stuck, %d lap spreads for %d frogs and %d hops", rc.Round, len(f.Stuck), len(f.LapSpreads), rc.Frogs, rc.Hops)
		}
		if f.LongBack > f.Turnarounds || f.FrogsLongBack > f.LongBack || f.Stragglers >= rc.Frogs {
			t.Errorf("race %d: inconsistent %+v", rc.Round, f)
		}
		for _, s := range f.LapSpreads {
			if s < 0 || s > rc.TrackLen {
				t.Errorf("race %d: spread %d on a %d track", rc.Round, s, rc.TrackLen)
			}
		}
	}
}
