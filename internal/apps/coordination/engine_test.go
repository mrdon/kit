package coordination

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// Engine state-machine tests. These exercise the pure-Go logic
// (recomputeMeeting, meetingIsComplete, nudgeInterval, pickReason)
// without any DB or LLM involvement.

func TestRecomputeMeeting_AllAccept(t *testing.T) {
	slot1 := slotAt(t, "2026-05-12T14:00:00Z")
	slot2 := slotAt(t, "2026-05-13T13:00:00Z")
	coord := &Coordination{
		Config: CoordinationConfig{CandidateSlots: []Slot{slot1, slot2}},
	}
	parts := []Participant{
		respondedWith(slot1, VerdictAccept),
		respondedWith(slot1, VerdictAccept),
	}
	candidates, invalidated := recomputeMeeting(coord, parts)
	if len(candidates) != 2 {
		t.Errorf("candidates = %d, want 2 (no rejections)", len(candidates))
	}
	if len(invalidated) != 0 {
		t.Errorf("invalidated = %d, want 0", len(invalidated))
	}
}

func TestRecomputeMeeting_RejectionRemoves(t *testing.T) {
	slot1 := slotAt(t, "2026-05-12T14:00:00Z")
	slot2 := slotAt(t, "2026-05-13T13:00:00Z")
	coord := &Coordination{
		Config: CoordinationConfig{CandidateSlots: []Slot{slot1, slot2}},
	}
	parts := []Participant{
		respondedWith(slot1, VerdictReject),
		respondedWith(slot2, VerdictAccept),
	}
	candidates, _ := recomputeMeeting(coord, parts)
	if len(candidates) != 1 || candidates[0].Key() != slot2.Key() {
		t.Errorf("expected only slot2 to survive; got %v", keys(candidates))
	}
}

func TestRecomputeMeeting_InvalidatedRespondent(t *testing.T) {
	// Alice accepted slot1. Bob rejected slot1. After recompute slot1 is
	// gone, slot2 survives. Both Alice and Bob need re-engagement: Alice
	// because her only accept is now eliminated; Bob because he hasn't
	// committed to slot2 either.
	slot1 := slotAt(t, "2026-05-12T14:00:00Z")
	slot2 := slotAt(t, "2026-05-13T13:00:00Z")
	coord := &Coordination{
		Config: CoordinationConfig{CandidateSlots: []Slot{slot1, slot2}},
	}
	alice := respondedWith(slot1, VerdictAccept)
	bob := respondedWith(slot1, VerdictReject)
	candidates, invalidated := recomputeMeeting(coord, []Participant{alice, bob})
	if len(candidates) != 1 || candidates[0].Key() != slot2.Key() {
		t.Errorf("expected slot2 only; got %v", keys(candidates))
	}
	if len(invalidated) != 2 {
		t.Errorf("expected both Alice and Bob invalidated; got %v", invalidated)
	}
}

func TestMeetingIsComplete_AllRespondedAndOneSlotWorks(t *testing.T) {
	slot1 := slotAt(t, "2026-05-12T14:00:00Z")
	slot2 := slotAt(t, "2026-05-13T13:00:00Z")
	coord := &Coordination{
		Config: CoordinationConfig{CandidateSlots: []Slot{slot1, slot2}},
	}
	parts := []Participant{
		respondedWithAll(VerdictAccept, slot1, slot2),
		respondedWith(slot1, VerdictAccept),
	}
	done, slot := meetingIsComplete(coord, parts)
	if !done {
		t.Fatalf("expected complete")
	}
	if slot.Key() != slot1.Key() {
		t.Errorf("preferred slot = %s, want slot1", slot.Key())
	}
}

func TestMeetingIsComplete_PendingBlocks(t *testing.T) {
	slot1 := slotAt(t, "2026-05-12T14:00:00Z")
	coord := &Coordination{
		Config: CoordinationConfig{CandidateSlots: []Slot{slot1}},
	}
	parts := []Participant{
		respondedWith(slot1, VerdictAccept),
		{Status: ParticipantPending}, // still waiting on this one
	}
	done, _ := meetingIsComplete(coord, parts)
	if done {
		t.Errorf("expected not complete with pending participant")
	}
}

func TestMeetingIsComplete_NoOverlap(t *testing.T) {
	slot1 := slotAt(t, "2026-05-12T14:00:00Z")
	slot2 := slotAt(t, "2026-05-13T13:00:00Z")
	coord := &Coordination{
		Config: CoordinationConfig{CandidateSlots: []Slot{slot1, slot2}},
	}
	parts := []Participant{
		respondedWith(slot1, VerdictAccept), // alice accepts only slot1
		respondedWith(slot2, VerdictAccept), // bob accepts only slot2
	}
	done, _ := meetingIsComplete(coord, parts)
	if done {
		t.Errorf("expected not complete: no slot accepted by both")
	}
}

func TestPickReason(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{ParticipantPending, "initial"},
		{ParticipantContacted, "nudge"},
		{ParticipantResponded, "reengage_invalidated"},
	}
	for _, c := range cases {
		got := pickReason(nil, &Participant{Status: c.status})
		if got != c.want {
			t.Errorf("status=%s → reason=%s, want %s", c.status, got, c.want)
		}
	}
}

func TestNudgeInterval(t *testing.T) {
	cases := []struct {
		count int
		want  time.Duration
	}{
		{1, 24 * time.Hour},
		{2, 24 * time.Hour},
		{3, 48 * time.Hour},
	}
	for _, c := range cases {
		got := nudgeInterval(c.count)
		if got != c.want {
			t.Errorf("count=%d → interval=%v, want %v", c.count, got, c.want)
		}
	}
}

// Helpers

func slotAt(t *testing.T, startStr string) Slot {
	t.Helper()
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		t.Fatalf("parsing %s: %v", startStr, err)
	}
	return Slot{Start: start, End: start.Add(30 * time.Minute)}
}

func respondedWith(s Slot, v SlotVerdict) Participant {
	return Participant{
		ID:     uuid.New(),
		Status: ParticipantResponded,
		Constraints: Constraints{
			SlotVerdicts: map[string]SlotVerdict{s.Key(): v},
		},
	}
}

func respondedWithAll(v SlotVerdict, slots ...Slot) Participant {
	verdicts := map[string]SlotVerdict{}
	for _, s := range slots {
		verdicts[s.Key()] = v
	}
	return Participant{
		ID:          uuid.New(),
		Status:      ParticipantResponded,
		Constraints: Constraints{SlotVerdicts: verdicts},
	}
}

func keys(slots []Slot) []string {
	out := make([]string, len(slots))
	for i, s := range slots {
		out[i] = s.Key()
	}
	return out
}
