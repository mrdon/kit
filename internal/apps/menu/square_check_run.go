package menu

import (
	"context"
	"fmt"
	"strings"

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
	Plan    sqPlan // the last look, after everything it added
	Done    []string
	Counted []string // online crowlers taken off the shop or back on sale
	Failed  []string
	Filed   int
}

// checkSquare compares the menu with Square. Without apply it only looks.
// With apply it adds what has a right answer, re-reading Square after each
// round so a new item's flight option and crowler follow it, takes the
// crowlers of drinks no longer on the menu off the online shop, then files a
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
	var countErr error
	for range maxSquarePasses {
		cat, err := loadSquareCatalog(ctx, client)
		if err != nil {
			return run, err
		}
		if cat.Counts, err = st.counts(ctx, cat); err != nil {
			cat.Counts = nil
			countErr = err
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
	if countErr != nil {
		run.Plan.Notes = append(run.Plan.Notes, "online crowler stock not read, so none were counted back in: "+countErr.Error())
	}
	if !apply {
		return run, nil
	}
	counted, failed := applyCounts(ctx, st, run.Plan.Counts)
	run.Counted, run.Failed = counted, append(run.Failed, failed...)
	if run.Filed, err = a.fileQuestions(ctx, tenantID, run.Plan.Questions); err != nil {
		return run, err
	}
	if err := a.briefAdds(ctx, tenantID, run.Done, run.Counted); err != nil {
		return run, fmt.Errorf("filing the Square briefing: %w", err)
	}
	if len(run.Failed) > 0 {
		return run, fmt.Errorf("square check: %d of %d changes failed: %s",
			len(run.Failed), len(run.Failed)+len(run.Done)+len(run.Counted), strings.Join(run.Failed, "; "))
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
			if err := st.set(ctx, ids[temp], inStock); err != nil {
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

// describeSquareRun is the tool's report.
func describeSquareRun(run squareRun, apply bool, asked map[string]bool) string {
	var b strings.Builder
	if apply {
		writeList(&b, "Added to Square", run.Done)
		writeList(&b, "Online crowlers", run.Counted)
		writeList(&b, "Failed", run.Failed)
	} else {
		var adds, counts []string
		for _, add := range run.Plan.Adds {
			adds = append(adds, add.Summary)
		}
		for _, c := range run.Plan.Counts {
			counts = append(counts, c.Summary)
		}
		writeList(&b, "Would add to Square", adds)
		writeList(&b, "Would change online crowlers", counts)
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
