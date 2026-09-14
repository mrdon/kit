package trivia

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// The picker rule, exercised as a pure function: the whole reason it is one
// is that the five cases below are five lines of table rather than five
// games driven through the service.

// cand builds a candidate with a joined-at derived from its index, so "joined
// earlier" is expressible without a pile of timestamps in the table.
func cand(id uuid.UUID, score, joinOrder int, wrote bool, eligibleFrom int) PickerCandidate {
	base := time.Date(2026, 9, 13, 19, 0, 0, 0, time.UTC)
	return PickerCandidate{
		TeamID:              id,
		Score:               score,
		JoinedAt:            base.Add(time.Duration(joinOrder) * time.Minute),
		EligibleFromOrdinal: eligibleFrom,
		WroteWinner:         wrote,
	}
}

func TestChoosePicker(t *testing.T) {
	// Fixed ids, sorted, so the last-resort id tie-break is predictable and
	// the failure messages name something a reader can follow.
	a := uuid.MustParse("00000000-0000-0000-0000-0000000000a1")
	b := uuid.MustParse("00000000-0000-0000-0000-0000000000b2")
	c := uuid.MustParse("00000000-0000-0000-0000-0000000000c3")

	cases := []struct {
		name       string
		cands      []PickerCandidate
		next       int
		wantTeam   uuid.UUID
		wantReason PickerReason
		wantOK     bool
	}{
		{
			// The ordinary night: one table wrote the winning card and it is
			// theirs whatever the score does.
			name: "one table wrote the winner",
			cands: []PickerCandidate{
				cand(a, 900, 0, true, 1),
				cand(b, 100, 1, false, 1),
				cand(c, 200, 2, false, 1),
			},
			next: 3, wantTeam: a, wantReason: PickerWroteWinner, wantOK: true,
		},
		{
			// Shared card -- common, because BuildSlots dedupes equal
			// answers onto one. The one furthest behind takes the pick.
			name: "shared winning card goes to the lowest score",
			cands: []PickerCandidate{
				cand(a, 900, 0, true, 1),
				cand(b, 300, 1, true, 1),
				cand(c, 100, 2, false, 1),
			},
			next: 3, wantTeam: b, wantReason: PickerWroteWinner, wantOK: true,
		},
		{
			// Shared AND level: earliest joined, never the id, because the
			// room can see the join order on the lobby screen.
			name: "shared and tied on score goes to the earliest joined",
			cands: []PickerCandidate{
				cand(a, 400, 3, true, 1),
				cand(b, 400, 1, true, 1),
				cand(c, 900, 2, false, 1),
			},
			next: 3, wantTeam: b, wantReason: PickerWroteWinner, wantOK: true,
		},
		{
			// Everyone overshot: the pseudo-slot took the round and nobody
			// wrote it, so the pick falls to whoever is furthest behind.
			name: "nobody wrote it goes to the lowest score overall",
			cands: []PickerCandidate{
				cand(a, 900, 0, false, 1),
				cand(b, 500, 1, false, 1),
				cand(c, 200, 2, false, 1),
			},
			next: 3, wantTeam: c, wantReason: PickerLowest, wantOK: true,
		},
		{
			// Nobody wrote it and the two at the back are level: join order
			// again.
			name: "nobody wrote it and tied on score goes to the earliest joined",
			cands: []PickerCandidate{
				cand(a, 100, 2, false, 1),
				cand(b, 100, 0, false, 1),
				cand(c, 900, 1, false, 1),
			},
			next: 3, wantTeam: b, wantReason: PickerLowest, wantOK: true,
		},
		{
			// A table that will not be eligible for the question being
			// picked cannot pick it -- even with the lowest score, and even
			// having written the winning card.
			name: "a table not yet eligible is skipped",
			cands: []PickerCandidate{
				cand(a, 0, 0, true, 9),
				cand(b, 700, 1, false, 1),
				cand(c, 800, 2, false, 1),
			},
			next: 3, wantTeam: b, wantReason: PickerLowest, wantOK: true,
		},
		{
			// The mid-round joiner the rule is careful about: eligible from
			// the NEXT ordinal is eligible, and being on zero it picks.
			name: "a mid-round joiner is eligible from the next ordinal",
			cands: []PickerCandidate{
				cand(a, 700, 0, false, 1),
				cand(b, 0, 1, false, 3),
			},
			next: 3, wantTeam: b, wantReason: PickerLowest, wantOK: true,
		},
		{
			// An empty room has no picker. Not an error -- every surface
			// renders the absence, and the next scored round fills it in.
			name: "empty room has no picker", cands: nil, next: 1, wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason, ok := ChoosePicker(tc.cands, tc.next)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if got != tc.wantTeam {
				t.Errorf("picker = %s, want %s", got, tc.wantTeam)
			}
			if reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}

// DrawPicker is random, so what is worth asserting is that it draws from the
// room and only from the room -- and that an empty room draws nobody rather
// than panicking on an empty slice.
func TestDrawPickerStaysInTheRoom(t *testing.T) {
	teams := []Team{
		{ID: uuid.New(), Name: "Bar Flies"},
		{ID: uuid.New(), Name: "Quiz Khalifa"},
		{ID: uuid.New(), Name: "Trivia Newton-John"},
	}
	seen := map[uuid.UUID]bool{}
	for range 200 {
		id, ok := DrawPicker(teams)
		if !ok {
			t.Fatal("a room with three tables drew nobody")
		}
		found := false
		for _, tm := range teams {
			if tm.ID == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("drew %s, which is not in the room", id)
		}
		seen[id] = true
	}
	// 200 draws over three tables missing one entirely is a 1-in-10^35
	// coincidence, so this catches a draw stuck on index 0 without being
	// flaky.
	if len(seen) != len(teams) {
		t.Errorf("200 draws only ever landed on %d of %d tables", len(seen), len(teams))
	}
	if _, ok := DrawPicker(nil); ok {
		t.Error("an empty room drew a picker")
	}
}

// The rule applied end to end, because a pure function that is never wired up
// is a pure function that lies. Three rounds through the real service: the
// draw at the start, the table that wrote the winner, and the round nobody
// got at all.
func TestPickerThroughTheService(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")

	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	g := f.reload(game.ID)
	if g.PickerTeamID == nil {
		t.Fatal("starting the game drew no picker")
	}
	if g.PickerReason != PickerDrawn {
		t.Fatalf("first picker reason = %q, want %q", g.PickerReason, PickerDrawn)
	}
	if *g.PickerTeamID != a.ID && *g.PickerTeamID != b.ID {
		t.Fatalf("drew %s, which is neither table in the room", *g.PickerTeamID)
	}

	// Round one: only Bar Flies gets it, so the pick is theirs.
	correct := snapCorrect(t, f, game)
	f.playOneRound(game, map[uuid.UUID]string{
		a.ID: FormatValue(correct),
		b.ID: FormatValue(correct - 10),
	})
	g = f.reload(game.ID)
	if g.PickerTeamID == nil || *g.PickerTeamID != a.ID {
		t.Fatalf("picker = %v, want the table that wrote the winner (%s)", g.PickerTeamID, a.ID)
	}
	if g.PickerReason != PickerWroteWinner {
		t.Fatalf("reason = %q, want %q", g.PickerReason, PickerWroteWinner)
	}

	// Round two: everybody overshoots, so the pseudo-slot takes it and the
	// pick falls to whoever is furthest behind -- which is Quiz Khalifa,
	// having banked nothing in round one.
	f.do(game.ID, ActionRequest{Action: ActionNext, FromPhase: PhaseScoring})
	correct = snapCorrect(t, f, game)
	f.playOneRound(game, map[uuid.UUID]string{
		a.ID: FormatValue(correct + 1000),
		b.ID: FormatValue(correct + 2000),
	})
	g = f.reload(game.ID)
	if g.PickerTeamID == nil || *g.PickerTeamID != b.ID {
		t.Fatalf("picker = %v, want the lowest-scoring table (%s)", g.PickerTeamID, b.ID)
	}
	if g.PickerReason != PickerLowest {
		t.Fatalf("reason = %q, want %q", g.PickerReason, PickerLowest)
	}

	// And it reaches every surface, which is the point of storing it.
	snap, err := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	display, player, host := ProjectDisplay(snap), ProjectPlayer(snap, a.ID), ProjectHost(snap)
	for name, got := range map[string]*wirePicker{
		"display": display.Picker, "player": player.Picker, "host": host.Picker,
	} {
		if got == nil || got.TeamID != b.ID.String() || got.Name != "Quiz Khalifa" {
			t.Errorf("%s frame picker = %+v, want Quiz Khalifa", name, got)
		}
	}
	for name, got := range map[string]string{
		"display": display.PickerReason, "player": player.PickerReason, "host": host.PickerReason,
	} {
		if got != string(PickerLowest) {
			t.Errorf("%s frame pickerReason = %q, want %q", name, got, PickerLowest)
		}
	}
}
