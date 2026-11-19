package menu

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/square"
)

// maxSquarePasses bounds how many times one check re-reads Square after
// adding to it. A new drink takes three: the item, then the sizes and
// flight option that need the item, then the crowler listing that needs its
// crowler size.
const maxSquarePasses = 4

// squareRun is what one check did.
type squareRun struct {
	Plan   sqPlan // the last look, after everything it added
	Done   []string
	Failed []string
	Filed  int
}

// checkSquare compares the menu with Square. Without apply it only looks.
// With apply it adds what has a right answer, re-reading Square after each
// round so a new item's flight option and crowler follow it, then files a
// decision card for each question not asked before and a briefing of what
// it added.
func (a *App) checkSquare(ctx context.Context, tenantID uuid.UUID, apply bool) (squareRun, error) {
	var run squareRun
	rows, _, err := a.menuRows(ctx, tenantID)
	if err != nil {
		return run, err
	}
	client, err := square.Instance().LoadClient(ctx, tenantID)
	if err != nil {
		return run, err
	}
	fresh := map[string]bool{}
	st := &stocker{client: client}
	for range maxSquarePasses {
		cat, err := loadSquareCatalog(ctx, client)
		if err != nil {
			return run, err
		}
		run.Plan = planSquare(rows, cat, fresh)
		if !apply || len(run.Plan.Adds) == 0 {
			break
		}
		done, failed := a.applyAdds(ctx, client, st, run.Plan.Adds, fresh)
		run.Done, run.Failed = append(run.Done, done...), append(run.Failed, failed...)
		if len(done) == 0 {
			break
		}
	}
	if !apply {
		return run, nil
	}
	if run.Filed, err = a.fileQuestions(ctx, tenantID, run.Plan.Questions); err != nil {
		return run, err
	}
	if err := a.briefAdds(ctx, tenantID, run.Done); err != nil {
		return run, fmt.Errorf("filing the Square briefing: %w", err)
	}
	if len(run.Failed) > 0 {
		return run, fmt.Errorf("square check: %d of %d additions failed: %s",
			len(run.Failed), len(run.Failed)+len(run.Done), strings.Join(run.Failed, "; "))
	}
	return run, nil
}

// applyAdds writes each add in its own batch, so one Square refuses does
// not hold up the rest, and counts new crowlers in.
func (a *App) applyAdds(ctx context.Context, client *square.Client, st *stocker, adds []sqAdd, fresh map[string]bool) (done, failed []string) {
	for _, add := range adds {
		ids, err := client.BatchUpsertCatalog(ctx, uuid.NewString(), add.Objects)
		if err != nil {
			failed = append(failed, add.Summary+": "+err.Error())
			continue
		}
		if add.Fresh != "" {
			fresh[nameKey(add.Fresh)] = true
		}
		var unstocked []string
		for _, temp := range add.Stock {
			if err := st.count(ctx, ids[temp]); err != nil {
				unstocked = append(unstocked, err.Error())
			}
		}
		if len(unstocked) > 0 {
			done = append(done, add.Summary+" (but not counted in, so it shows sold out online: "+strings.Join(unstocked, "; ")+")")
			continue
		}
		done = append(done, add.Summary)
	}
	return done, failed
}

// stocker counts new online crowlers in at the one location they sell from.
type stocker struct {
	client   *square.Client
	location string
}

func (s *stocker) count(ctx context.Context, variationID string) error {
	if variationID == "" {
		return errors.New("square did not return the new variation's id")
	}
	if s.location == "" {
		locs, err := s.client.ListLocations(ctx)
		if err != nil {
			return fmt.Errorf("listing locations: %w", err)
		}
		var active []string
		for _, l := range locs {
			if l.Status == "" || l.Status == "ACTIVE" {
				active = append(active, l.ID)
			}
		}
		if len(active) != 1 {
			return fmt.Errorf("%d active locations, so which one to count it in at is a choice", len(active))
		}
		s.location = active[0]
	}
	at := time.Now().UTC().Format(time.RFC3339)
	return s.client.SetInventoryCount(ctx, uuid.NewString(), s.location, variationID, inStock, at)
}

// describeSquareRun is the tool's report.
func describeSquareRun(run squareRun, apply bool, asked map[string]bool) string {
	var b strings.Builder
	if apply {
		writeList(&b, "Added to Square", run.Done)
		writeList(&b, "Failed", run.Failed)
	} else {
		var adds []string
		for _, add := range run.Plan.Adds {
			adds = append(adds, add.Summary)
		}
		writeList(&b, "Would add to Square", adds)
	}
	var qs []string
	for _, q := range run.Plan.Questions {
		line := q.Title
		if asked[q.Key] {
			line += " (already asked)"
		}
		qs = append(qs, line)
	}
	label := "Would ask on a decision card"
	if apply {
		label = fmt.Sprintf("Questions (%d new decision cards)", run.Filed)
	}
	writeList(&b, label, qs)
	writeList(&b, "Not checked", run.Plan.Notes)
	if b.Len() == 0 {
		return "Square has everything on the menu: every pour, flight option and crowler."
	}
	return strings.TrimSpace(b.String())
}

func writeList(b *strings.Builder, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "%s:\n", title)
	for _, l := range lines {
		fmt.Fprintf(b, "  - %s\n", l)
	}
	b.WriteString("\n")
}
