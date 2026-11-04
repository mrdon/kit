package cogs

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Badges on a tile.
const (
	BadgeDue   = "due"
	BadgeDirty = "dirty"
	BadgeEmpty = "empty"
	BadgeOnTap = "on_tap"
)

// BoardVessel is a tile: the vessel with what the brewer reads off it.
type BoardVessel struct {
	*Vessel
	BeerID    *uuid.UUID `json:"beer_id"`
	Beer      string     `json:"beer"`
	BatchCode string     `json:"batch_code"`
	BrewedOn  string     `json:"brewed_on"`
	// StageLabel and AgeLabel make "Fermenting · day 6".
	StageLabel string `json:"stage_label"`
	AgeLabel   string `json:"age_label"`
	Next       string `json:"next"`
	Badge      string `json:"badge"`
	// ReadyOn is when the beer is predicted ready for its next step.
	ReadyOn string `json:"ready_on,omitempty"`
	// NextAction is the form "what's next" points at.
	NextAction string `json:"next_action,omitempty"`
}

// tileInputs is what a tile is worked out from.
type tileInputs struct {
	v       *Vessel
	batch   *Batch
	beer    *Beer
	code    string
	plan    *PlanEntry
	planFor string
	onTapOf map[uuid.UUID]string // beer -> serving tank pouring it
	now     time.Time
	loc     *time.Location
}

func buildTile(in tileInputs) BoardVessel {
	t := BoardVessel{Vessel: in.v}
	if in.v.Empty() || in.batch == nil || in.beer == nil {
		emptyTile(&t, in)
		return t
	}
	t.BeerID, t.Beer, t.BatchCode = &in.beer.ID, in.beer.Name, in.code
	t.BrewedOn = dateKey(in.batch.BrewedOn)
	brewed := localDate(in.batch.BrewedOn, in.loc)
	days := daysBetween(brewed, in.now, in.loc)
	t.AgeLabel = fmt.Sprintf("day %d", days)
	switch in.v.Kind {
	case KindFermenter:
		fermenterTile(&t, in, brewed, days)
	case KindBrite:
		since := sinceStage(in)
		ready := stageStart(in).AddDate(0, 0, in.beer.BriteDays)
		if since < in.beer.BriteDays {
			t.StageLabel, t.Next, t.ReadyOn = "Carbonating", "Ready around "+shortDate(ready), dateOnly(ready)
		} else {
			t.StageLabel, t.Badge, t.NextAction = "Ready", BadgeDue, EvPackage
			t.Next, t.ReadyOn = "Package it or fill a serving tank", dateOnly(in.now)
		}
	case KindServing:
		if in.v.OnTap {
			t.StageLabel, t.Badge, t.Next = "Pouring", BadgeOnTap, "On tap"
		} else {
			t.StageLabel, t.Next = "Waiting its turn", "Ready to go on"
			if sv, ok := in.onTapOf[in.beer.ID]; ok && sv != in.v.Name {
				t.Next = "Goes on after " + sv
			}
		}
	case KindBarrel:
		months := int(in.now.Sub(stageStart(in)).Hours() / 24 / 30.4)
		t.StageLabel, t.AgeLabel = "Aging", fmt.Sprintf("%d %s", months, plural(months, "month", "months"))
	}
	return t
}

func emptyTile(t *BoardVessel, in tileInputs) {
	switch {
	case in.v.Dirty:
		t.StageLabel, t.Next, t.Badge, t.NextAction = "Dirty", "Needs cleaning", BadgeDirty, EvClean
	default:
		t.StageLabel, t.Badge = "Empty", BadgeEmpty
		t.Next = "Ready to fill"
		if in.v.Kind == KindFermenter {
			t.Next = "Nothing planned yet"
		}
	}
	if in.plan != nil && in.plan.BatchID == nil {
		t.Next = in.planFor + " brews " + shortDay(in.plan.BrewOn, in.loc)
		t.NextAction = EvBrew
	}
}

// fermenterTile walks the beer's durations: fermenting, waiting to dry hop
// (due), dry hopped, conditioning or lagering, ready (due).
func fermenterTile(t *BoardVessel, in tileInputs, brewed time.Time, days int) {
	b := in.beer
	condLabel := "Conditioning"
	if strings.Contains(strings.ToLower(b.Style+" "+b.Name), "lager") {
		condLabel = "Lagering"
	}
	fermentDone := brewed.AddDate(0, 0, b.FermentDays)
	stage := in.v.Stage
	switch {
	case stage == StageFermenting && days < b.FermentDays:
		t.StageLabel = "Fermenting"
		if b.DryHop {
			t.Next = "Dry hop around " + shortDate(fermentDone)
		} else {
			ready := fermentDone.AddDate(0, 0, b.ConditionDays)
			t.Next, t.ReadyOn = readyText(ready, b), dateOnly(ready)
		}
	case stage == StageFermenting && b.DryHop:
		t.StageLabel, t.Badge, t.Next, t.NextAction = "Waiting to dry hop", BadgeDue, "Dry hop it", EvDryHop
		t.ReadyOn = dateOnly(in.now.AddDate(0, 0, b.DryHopDays+b.ConditionDays))
	default:
		start := fermentDone
		if stage == StageDryHopped || stage == StageConditioning {
			start = stageStart(in)
		}
		extra := b.ConditionDays
		if stage == StageDryHopped {
			extra += b.DryHopDays
		}
		ready := start.AddDate(0, 0, extra)
		t.ReadyOn = dateOnly(ready)
		switch {
		case stage == StageDryHopped && sinceStage(in) < b.DryHopDays:
			t.StageLabel, t.Next = "Dry hopped", readyText(ready, b)
		case in.now.Before(ready):
			t.StageLabel, t.Next = condLabel, readyText(ready, b)
		default:
			t.StageLabel, t.Badge = "Ready", BadgeDue
			t.Next, t.NextAction = readyAction(b)
		}
	}
}

func readyText(ready time.Time, b *Beer) string {
	if b.UsesBrite {
		return "Brite tank around " + shortDate(ready)
	}
	return "Ready around " + shortDate(ready)
}

func readyAction(b *Beer) (string, string) {
	if b.UsesBrite {
		return "Move it to the brite tank", EvTransfer
	}
	return "Package or move it", EvPackage
}

func stageStart(in tileInputs) time.Time {
	if in.v.StageAt != nil {
		return dayOf(*in.v.StageAt, in.loc)
	}
	return localDate(in.batch.BrewedOn, in.loc)
}

func sinceStage(in tileInputs) int { return daysBetween(stageStart(in), in.now, in.loc) }

// localDate reads a DATE column as that calendar day in loc.
func localDate(d time.Time, loc *time.Location) time.Time {
	y, m, day := d.UTC().Date()
	return time.Date(y, m, day, 0, 0, 0, 0, loc)
}

func daysBetween(from, to time.Time, loc *time.Location) int {
	a, b := dayOf(from, loc), dayOf(to, loc)
	return int(b.Sub(a).Hours()/24 + 0.5)
}

func dateOnly(t time.Time) string { return t.Format("2006-01-02") }

func shortDate(t time.Time) string { return t.Format("Jan 2") }

// shortDay reads a plan date as "Tue" within a week, "Nov 18" past it.
func shortDay(date string, loc *time.Location) string {
	d, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return date
	}
	if days := daysBetween(time.Now(), d, loc); days >= 0 && days < 7 {
		if days == 0 {
			return "today"
		}
		return d.Format("Mon")
	}
	return d.Format("Jan 2")
}
