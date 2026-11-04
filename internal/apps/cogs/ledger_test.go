package cogs

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func day(d int) time.Time { return time.Date(2026, 1, d, 12, 0, 0, 0, time.UTC) }

func mustReplay(t *testing.T, id uuid.UUID, kind string, events []Event) VesselState {
	t.Helper()
	s, err := Replay(id, kind, events)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	return s
}

// A spill logged a day late lands before the move that followed it, and
// the move still takes what it took.
func TestReplayOrdersByWhenItHappened(t *testing.T) {
	fv, bt, batch := uuid.New(), uuid.New(), uuid.New()
	events := []Event{
		{Seq: 1, Kind: EvBrew, ToVesselID: &fv, BatchID: &batch, VolumeBBL: 10, OccurredAt: day(1)},
		{Seq: 2, Kind: EvTransfer, VesselID: &fv, ToVesselID: &bt, BatchID: &batch, VolumeBBL: 4, VolumeInBBL: 4,
			OccurredAt: day(5)},
		{Seq: 3, Kind: EvLoss, VesselID: &fv, BatchID: &batch, LossBBL: 0.5, OccurredAt: day(3)},
	}
	s := mustReplay(t, fv, KindFermenter, events)
	if s.Volume != 5.5 || s.BatchID == nil || *s.BatchID != batch {
		t.Fatalf("FV = %+v, want 5.5 bbl of the batch", s)
	}
	b := mustReplay(t, bt, KindBrite, events)
	if b.Volume != 4 || b.Stage != StageBrite || b.FilledAt == nil || !b.FilledAt.Equal(day(5)) {
		t.Fatalf("BT = %+v", b)
	}
	left := 5.5
	events = append(events, Event{Seq: 4, Kind: EvEmpty, VesselID: &fv, LossBBL: left, OccurredAt: day(6),
		Details: EventDetails{Left: &left}})
	if e := mustReplay(t, fv, KindFermenter, events); !e.Empty() || !e.Dirty {
		t.Fatalf("after empty = %+v, want empty and dirty", e)
	}
	at, err := ReplayAt(fv, KindFermenter, events, day(4))
	if err != nil || at.Volume != 9.5 {
		t.Fatalf("as of day 4 = %+v %v, want 9.5", at, err)
	}
}

// An entry slipped in before an "is now empty" changes what was left, and
// the replay says so instead of letting beer vanish into the write-off.
func TestReplayRefusesWhatCantHaveHappened(t *testing.T) {
	fv, batch := uuid.New(), uuid.New()
	left := 10.0
	events := []Event{
		{Seq: 1, Kind: EvBrew, ToVesselID: &fv, BatchID: &batch, VolumeBBL: 10, OccurredAt: day(1)},
		{Seq: 2, Kind: EvEmpty, VesselID: &fv, BatchID: &batch, LossBBL: left, OccurredAt: day(5),
			Details: EventDetails{Left: &left}},
		{Seq: 3, Kind: EvPackage, VesselID: &fv, BatchID: &batch, VolumeBBL: 4, OccurredAt: day(3)},
	}
	if _, err := Replay(fv, KindFermenter, events); !errors.Is(err, ErrConflict) {
		t.Fatalf("late package before an empty: err = %v, want ErrConflict", err)
	}
	over := []Event{
		{Seq: 1, Kind: EvBrew, ToVesselID: &fv, BatchID: &batch, VolumeBBL: 5, OccurredAt: day(1)},
		{Seq: 2, Kind: EvPackage, VesselID: &fv, BatchID: &batch, VolumeBBL: 8, OccurredAt: day(2)},
	}
	if _, err := Replay(fv, KindFermenter, over); !errors.Is(err, ErrConflict) {
		t.Fatalf("over-draw: err = %v, want ErrConflict", err)
	}
	other := uuid.New()
	refill := []Event{
		{Seq: 1, Kind: EvBrew, ToVesselID: &fv, BatchID: &batch, VolumeBBL: 5, OccurredAt: day(1)},
		{Seq: 2, Kind: EvBrew, ToVesselID: &fv, BatchID: &other, VolumeBBL: 5, OccurredAt: day(2)},
	}
	if _, err := Replay(fv, KindFermenter, refill); !errors.Is(err, ErrConflict) {
		t.Fatalf("brew into a full tank: err = %v, want ErrConflict", err)
	}
}
