package menu

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Filling the printed menu in, deliberately.
//
// This is the half of the paper menu that talks to the outside world, and it
// runs when somebody presses Sync rather than when somebody presses Print. The
// difference matters more than it sounds:
//
//   - Printing is now offline. Nobody standing at a printer waits on Untappd,
//     and no third party's bad afternoon becomes a blank sheet.
//   - Failures land in front of a person, at the moment they asked for the
//     thing, instead of silently subtracting prose from a document.
//
// It reads two different Untappds and they do not fail together. The digital
// board (business.untappd.com) carries the beers, sections, prices and ABVs,
// and answers anybody. The descriptions live on the consumer site
// (untappd.com), which sits behind bot management and answers a datacenter IP
// with a challenge -- so from a server this half generally cannot succeed at
// all, whatever the network is doing.
//
// That asymmetry is the design. A sync that gets the tap list and misses the
// prose is a good sync with a note attached, not a failure: the menu is mostly
// names and numbers, and the copy is the part a person wants to write anyway.
// The descriptions Kit cannot fetch can be pushed in from a machine the
// consumer site will talk to -- see set_menu_notes -- and once stored they are
// kept forever.

// SyncReport is what one sync did, for a human to read.
type SyncReport struct {
	// Rows is how many beers the board yielded, and Dropped how many it listed
	// that are not actually pouring.
	Rows    int `json:"rows"`
	Dropped int `json:"dropped"`

	// Described is how many of the rows have a description from any source,
	// and Missing lists by name the ones that do not. Missing is the work list
	// an agent with a better address than Kit's can pick up.
	Described int      `json:"described"`
	Missing   []string `json:"missing"`

	// Unmatched is the subset of Missing whose page could not be found on the
	// brewery's listings at all -- a different problem from a beer nobody has
	// written up, and one on Kit's side rather than upstream's.
	Unmatched []string `json:"unmatched,omitempty"`

	// Fetched is how many descriptions this sync newly pulled and stored.
	Fetched int `json:"fetched"`

	// NotesError is why the description half did not run or did not finish.
	// Empty means it worked. It is separate from an outright failure because
	// the tap list can succeed while this does not, which is the normal case
	// on a server.
	NotesError string `json:"notes_error,omitempty"`
}

// SyncPrintMenu refreshes the stored tap list from the board and, where it
// can, the descriptions.
//
// The board failing is the only thing that fails the sync, because without it
// there is no menu. Everything else is recorded and carried on from.
func SyncPrintMenu(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (SyncReport, error) {
	var rep SyncReport

	row, err := GetBoard(ctx, pool, tenantID)
	if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, pgx.ErrNoRows) {
		return rep, fmt.Errorf("loading menu board: %w", err)
	}
	if row == nil || row.SourceKind != SourceUntappd || strings.TrimSpace(row.SourceID) == "" {
		return rep, errors.New("the menu is not following an Untappd board — set one with set_menu_source")
	}

	state, err := LoadPrintState(ctx, pool, tenantID)
	if err != nil {
		return rep, err
	}

	body, _, err := FetchUntappdBody(ctx, printClient(), row.SourceID)
	if err != nil {
		// Nothing is written: the stored tap list is the last one known good,
		// and replacing it with nothing because Untappd blinked would turn a
		// failed refresh into a lost menu.
		return rep, fmt.Errorf("reading Untappd board %s: %w", row.SourceID, err)
	}
	parsed := ParseBeers(body)
	if len(parsed) < minPlausibleTaps {
		return rep, fmt.Errorf("untappd board %s parsed to only %d beers, which is too few to be real — "+
			"the page shape has probably changed; the stored tap list has been left alone",
			row.SourceID, len(parsed))
	}
	rows := OnTapOnly(parsed)
	rep.Rows, rep.Dropped = len(rows), len(parsed)-len(rows)

	// Descriptions. Hand-written ones are folded in as though already cached,
	// so they both win and stop Untappd being asked about that beer at all.
	cache := state.Notes
	for name, note := range state.Config.Notes {
		if strings.TrimSpace(note) != "" {
			cache[normalizeBeerName(name)] = note
		}
	}
	if brand := strings.TrimSpace(state.Config.Brand); brand == "" {
		rep.NotesError = "no Untappd brewery slug set, so no descriptions were fetched"
		applyNotes(rows, cache)
	} else {
		found, unmatched, ferr := AttachNotes(ctx, printClient(), brand, rows, cache)
		if ferr != nil {
			rep.NotesError = ferr.Error()
		}
		rep.Unmatched = unmatched
		rep.Fetched = len(found)
		if len(found) > 0 {
			if merr := MergePrintNotes(ctx, pool, tenantID, found); merr != nil {
				return rep, merr
			}
		}
		// AttachNotes fills what it could; the cache covers the rest.
		applyNotes(rows, cache)
	}

	for _, r := range rows {
		if strings.TrimSpace(r.Notes) == "" {
			rep.Missing = append(rep.Missing, r.Name)
		}
	}
	rep.Described = len(rows) - len(rep.Missing)

	if err := SavePrintRows(ctx, pool, tenantID, rows, rep.NotesError); err != nil {
		return rep, err
	}
	return rep, nil
}

// applyNotes fills in descriptions from the cache for any row that does not
// already carry one.
func applyNotes(rows []Beer, cache map[string]string) {
	for i := range rows {
		if strings.TrimSpace(rows[i].Notes) != "" {
			continue
		}
		if note, ok := cache[normalizeBeerName(rows[i].Name)]; ok {
			rows[i].Notes = note
		}
	}
}

// Summary renders the report the way both the console banner and the tool
// response say it, so the two cannot describe the same sync differently.
func (r SyncReport) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Synced %s from Untappd", plural(r.Rows, "beer"))
	if r.Dropped > 0 {
		fmt.Fprintf(&b, " (%s on the board but not pouring)", plural(r.Dropped, "other"))
	}
	fmt.Fprintf(&b, ". %d of %d have a description", r.Described, r.Rows)
	if r.Fetched > 0 {
		fmt.Fprintf(&b, ", %d newly fetched", r.Fetched)
	}
	b.WriteString(".")
	if r.NotesError != "" {
		fmt.Fprintf(&b, "\n\nDescriptions: %s", r.NotesError)
	}
	if len(r.Missing) > 0 {
		fmt.Fprintf(&b, "\n\nNo description yet: %s", strings.Join(r.Missing, ", "))
	}
	if len(r.Unmatched) > 0 {
		fmt.Fprintf(&b, "\n\nNo page found on the brewery's Untappd listings for: %s. "+
			"That is a lookup failure rather than a beer nobody has written up — "+
			"the description may well exist.", strings.Join(r.Unmatched, ", "))
	}
	return b.String()
}
