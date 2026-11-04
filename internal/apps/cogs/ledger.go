package cogs

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Replay works out a vessel's state from the events that stand. It is the
// only way vessel state is produced: the cache in app_cogs_vessels is
// this function's output, written after every action that touches the
// vessel, so a backdated entry or a reversal from the console lands in the
// right place without any inverse-of-an-effect logic.
//
// It is strict: a step that can't have happened (taking more than was in
// the vessel, filling one that wasn't empty, an "is now empty" that no
// longer matches what was left) fails with ErrConflict rather than being
// papered over. That is what stops a late entry or a reversal from quietly
// creating or losing beer.
//
// events must already exclude reversed actions and reversal rows; Replay
// sorts them by when they happened, then by write order.
func Replay(vesselID uuid.UUID, kind string, events []Event) (VesselState, error) {
	return replay(vesselID, kind, events, nil)
}

// ReplayAt is the state as of a moment: only events at or before it.
func ReplayAt(vesselID uuid.UUID, kind string, events []Event, at time.Time) (VesselState, error) {
	return replay(vesselID, kind, events, &at)
}

func replay(vesselID uuid.UUID, kind string, events []Event, until *time.Time) (VesselState, error) {
	sorted := append([]Event(nil), events...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].OccurredAt.Equal(sorted[j].OccurredAt) {
			return sorted[i].OccurredAt.Before(sorted[j].OccurredAt)
		}
		return sorted[i].Seq < sorted[j].Seq
	})
	var s VesselState
	for i := range sorted {
		if until != nil && sorted[i].OccurredAt.After(*until) {
			break
		}
		if err := applyEvent(&s, vesselID, kind, &sorted[i]); err != nil {
			return s, err
		}
	}
	if s.Volume < eps && s.Volume > -eps {
		s.Volume = 0
	}
	return s, nil
}

func conflict(ev *Event, format string, args ...any) error {
	return fmt.Errorf("%w: on %s %s", ErrConflict, ev.OccurredAt.Format("Jan 2"), fmt.Sprintf(format, args...))
}

// draw takes beer out of a vessel, refusing what wasn't there.
func draw(s *VesselState, ev *Event, amount float64) error {
	if s.BatchID == nil && amount > tolerance {
		return conflict(ev, "beer was taken from it while it was empty")
	}
	s.Volume -= amount
	if s.Volume < -tolerance {
		return conflict(ev, "that takes %.2f bbl more than was in it", -s.Volume)
	}
	return nil
}

func isVessel(p *uuid.UUID, id uuid.UUID) bool { return p != nil && *p == id }

func applyEvent(s *VesselState, id uuid.UUID, kind string, ev *Event) error {
	from := isVessel(ev.VesselID, id)
	at := ev.OccurredAt
	switch ev.Kind {
	case EvBrew, EvLoadVessel, EvTransfer:
		return applyMove(s, id, kind, ev)
	case EvPackage:
		if from {
			return draw(s, ev, ev.VolumeBBL)
		}
	case EvLoss, EvLevel:
		if from {
			return draw(s, ev, ev.LossBBL)
		}
	case EvEmpty, EvDump, EvKick:
		if from {
			// What was left when it was written must still be what was
			// left: an entry logged since, dated before this one, would
			// otherwise vanish into (or out of) the write-off.
			if left := ev.Details.Left; left != nil && math.Abs(*left-s.Volume) > tolerance {
				return conflict(ev, "it was emptied with %.2f bbl left, but the entries before it now leave %.2f",
					*left, s.Volume)
			}
			*s = VesselState{Dirty: true}
		}
	case EvClean:
		if from {
			s.Dirty = false
		}
	case EvDryHop:
		if from {
			s.Stage = StageDryHopped
			s.StageAt = &at
		}
	case EvOnTap:
		if from {
			s.OnTap = true
			s.Stage = StagePouring
			s.StageAt = &at
		}
	case EvVariant, EvBlend:
		if from && ev.BatchID != nil {
			b := *ev.BatchID
			s.BatchID = &b
		}
	}
	return nil
}

// applyMove is the events that put beer into a vessel (and, for a
// transfer, take it out of another).
func applyMove(s *VesselState, id uuid.UUID, kind string, ev *Event) error {
	from := isVessel(ev.VesselID, id)
	to := isVessel(ev.ToVesselID, id)
	switch ev.Kind {
	case EvBrew:
		if to {
			if s.BatchID == nil || ev.BatchID == nil || *s.BatchID != *ev.BatchID {
				if !s.Empty() {
					return conflict(ev, "a brew went into it while it still had beer in it")
				}
				fill(s, ev.BatchID, StageFermenting, ev)
			}
			s.Volume += ev.VolumeBBL
		}
	case EvLoadVessel:
		if to {
			if !s.Empty() {
				return conflict(ev, "it was loaded while it still had beer in it")
			}
			stage := ev.Details.Stage
			if stage == "" {
				stage = arrivalStage(kind)
			}
			fill(s, ev.BatchID, stage, ev)
			s.Volume += ev.VolumeBBL
			s.OnTap = ev.Details.OnTap
		}
	case EvTransfer:
		if from {
			if err := draw(s, ev, ev.VolumeBBL); err != nil {
				return err
			}
		}
		if to {
			// Into a vessel already holding beer is a blend of the same
			// beer; the blend event that follows names the new batch.
			if s.BatchID == nil {
				fill(s, ev.BatchID, arrivalStage(kind), ev)
			}
			s.Volume += ev.VolumeInBBL
		}
	}
	return nil
}

// fill starts a new occupancy of the vessel.
func fill(s *VesselState, batch *uuid.UUID, stage string, ev *Event) {
	at := ev.OccurredAt
	var b *uuid.UUID
	if batch != nil {
		v := *batch
		b = &v
	}
	*s = VesselState{BatchID: b, Stage: stage, StageAt: &at, FilledAt: &at}
}

// arrivalStage is the stage beer is in when it arrives in a vessel of this
// kind by a transfer.
func arrivalStage(kind string) string {
	switch kind {
	case KindBrite:
		return StageBrite
	case KindServing:
		return StageWaiting
	case KindBarrel:
		return StageAging
	case KindFermenter:
		return StageConditioning
	default:
		return ""
	}
}
