package agents

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
)

// A whole night plays out headless with every built-in strategy seated.
func TestPlayNightAllStrategies(t *testing.T) {
	cfg := engine.GameConfig{Rules: engine.Rules{MinFrogs: 7, HeatSize: 8, Rounds: 2, FlickTimeout: 8 * time.Second, SlipChance: 0.12}, Levels: engine.SingleLevel(16)}
	seats, err := SeatsFor([]string{"instant", "human", "asleep"}, 7)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Play(cfg, 7, seats)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Races) != 2 || r.Races[0].Rotations == 0 || r.Races[0].Hops == 0 || r.Races[0].Frogs != 7 {
		t.Fatalf("unexpected night: %+v", r)
	}
}

// 20 people: heats every round, ending in The Final.
func TestPlayHeats(t *testing.T) {
	cfg := engine.GameConfig{Rules: engine.Rules{MinFrogs: 7, HeatSize: 8, Rounds: 3, FlickTimeout: 8 * time.Second, SlipChance: 0.08}, Levels: engine.SingleLevel(16)}
	seats, err := Crowd(20, "human", 3)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Play(cfg, 3, seats)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Races) != 9 || r.Champion < 0 {
		t.Fatalf("want 3 rounds of 3 heats and a champion, got %d races, champion %d", len(r.Races), r.Champion)
	}
	t.Logf("20-person night: %s", r.GameTime.Round(time.Second))
}

// 30 people on a generated cup with random launches and chutes: heats every
// round, The Final, and specials firing.
func TestThirtyPeopleRandomSpecials(t *testing.T) {
	cfg, err := engine.Generate(rand.New(rand.NewPCG(5, 5)), engine.Preferences{Launches: 2, Chutes: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	seats, err := Crowd(30, "human", 11)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Play(cfg, 11, seats)
	if err != nil {
		t.Fatal(err)
	}
	specials := 0
	for _, rc := range r.Races {
		specials += rc.Launches + rc.Chutes
	}
	if len(r.Races) != 9 || r.Champion < 0 || specials == 0 {
		t.Fatalf("want 3 rounds of 3 heats, a champion and specials firing; got %d races, champion %d, %d specials", len(r.Races), r.Champion, specials)
	}
	t.Logf("30-person night with specials: %s, %d specials hit", r.GameTime.Round(time.Second), specials)
}
