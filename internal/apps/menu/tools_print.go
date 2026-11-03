package menu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The printed menu's tool handlers, shared by the agent and MCP surfaces.

// savePrintConfig validates and stores the printed menu's settings.
//
// Unknown fields are rejected for the same reason the board payload rejects
// them: a typo'd key in a hand-authored document should fail here, in front of
// whoever wrote it, rather than silently print a menu missing its footer.
func savePrintConfig(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, args setPrintArgs) (string, error) {
	dec := json.NewDecoder(strings.NewReader(args.Payload))
	dec.DisallowUnknownFields()
	var cfg PrintConfig
	if err := dec.Decode(&cfg); err != nil {
		return "", fmt.Errorf("%w: %w", ErrPayloadInvalid, err)
	}
	if err := prepareFeatured(ctx, pool, tenantID, &cfg); err != nil {
		return "", err
	}
	if err := SavePrintConfig(ctx, pool, tenantID, cfg); err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Printed menu configured — %s, %s, %s.",
		plural(len(cfg.Extras), "extra row"),
		plural(len(cfg.Notes), "written description"),
		plural(len(cfg.Blurbs), "blurb"))
	if f := cfg.Featured; f != nil {
		photo := "no photo"
		if f.Photo != photoNone {
			photo = "photo " + f.Photo
		}
		fmt.Fprintf(&b, "\n\nFeatured: %s (%s), %s. It prints only while it is on tap.",
			f.Beer, firstNonEmpty(f.Label, defaultFeaturedLabel), photo)
	}
	if strings.TrimSpace(cfg.Brand) == "" {
		b.WriteString("\n\nNo `brand` set, so the menu will print without beer descriptions: " +
			"the Untappd digital board does not carry them, and the brewery's untappd.com slug " +
			"is what finds the pages that do.")
	}
	return b.String(), nil
}

// plural counts a thing the way a person would read it back. "1 blurbs" in a
// confirmation makes the whole message look generated rather than written.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// syncPrint refreshes the tap list and reports what happened, for both
// surfaces. The report is worth returning in full: the missing-description
// list is the work an agent picks up next, and hiding it behind a count would
// mean a second round trip to find out which beers.
func syncPrint(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (string, error) {
	rep, err := SyncPrintMenu(ctx, pool, tenantID)
	if err != nil {
		return "", err
	}
	return rep.Summary(), nil
}

// saveNotes merges descriptions into the store.
//
// Blank values delete rather than storing an empty string, so clearing a
// description a customer should not read is possible without a separate tool,
// and a caller that sends a beer it could find nothing for does not overwrite
// prose somebody typed with nothing.
func saveNotes(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, args setNotesArgs) (string, error) {
	if len(args.Notes) == 0 {
		return "", errors.New("no notes given")
	}
	set := map[string]string{}
	var cleared []string
	for name, note := range args.Notes {
		key := normalizeBeerName(name)
		if key == "" {
			continue
		}
		if strings.TrimSpace(note) == "" {
			cleared = append(cleared, name)
			continue
		}
		set[key] = strings.TrimSpace(note)
	}
	if err := MergePrintNotes(ctx, pool, tenantID, set); err != nil {
		return "", err
	}
	if len(cleared) > 0 {
		if err := DeletePrintNotes(ctx, pool, tenantID, cleared); err != nil {
			return "", err
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Stored %s.", plural(len(set), "description"))
	if len(cleared) > 0 {
		fmt.Fprintf(&b, " Cleared %s: %s.", plural(len(cleared), "other"), strings.Join(cleared, ", "))
	}
	// The names are matched loosely at print time, so a description stored
	// under a name no beer answers to is silent. Saying which landed nowhere
	// is the difference between a typo found now and one found on paper.
	if stray := strayNotes(ctx, pool, tenantID, set); len(stray) > 0 {
		fmt.Fprintf(&b, "\n\nStored but matching no beer on the current tap list: %s. "+
			"They will apply if those beers come back.", strings.Join(stray, ", "))
	}
	return b.String(), nil
}

// strayNotes reports which of the just-stored names match nothing on the
// synced tap list.
func strayNotes(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, set map[string]string) []string {
	state, err := LoadPrintState(ctx, pool, tenantID)
	if err != nil || len(state.Rows) == 0 {
		return nil
	}
	onTap := make(map[string]bool, len(state.Rows))
	for _, r := range state.Rows {
		onTap[normalizeBeerName(r.Name)] = true
	}
	var stray []string
	for key := range set {
		if !onTap[key] {
			stray = append(stray, key)
		}
	}
	sort.Strings(stray)
	return stray
}

// describePrint summarises the printed menu for get_menu_board.
func describePrint(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) string {
	state, err := LoadPrintState(ctx, pool, tenantID)
	if err != nil {
		return ""
	}
	cfg := state.Config
	var b strings.Builder
	b.WriteString("\nPrinted menu:\n")
	if strings.TrimSpace(cfg.Brand) == "" {
		b.WriteString("  no brewery slug set — will print without descriptions\n")
	} else {
		fmt.Fprintf(&b, "  descriptions from untappd.com/%s (%d cached)\n", cfg.Brand, len(state.Notes))
	}
	if len(cfg.Extras) > 0 {
		fmt.Fprintf(&b, "  %d extra rows (non-Untappd)\n", len(cfg.Extras))
	}
	if state.SyncedAt == nil {
		b.WriteString("  never synced — the sheet has no beers on it until sync_menu_print runs\n")
	} else {
		fmt.Fprintf(&b, "  %d beers, synced %s\n",
			len(state.Rows), state.SyncedAt.Format("2 Jan 2006 15:04 MST"))
		var missing []string
		for _, r := range state.Rows {
			if _, ok := findNote(mergedNotes(state.Notes, cfg.Notes), r.Name); !ok {
				missing = append(missing, r.Name)
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(&b, "  no description yet: %s\n", strings.Join(missing, ", "))
		}
	}
	if state.SyncError != "" {
		fmt.Fprintf(&b, "  last sync warning: %s\n", state.SyncError)
	}
	if len(cfg.Blurbs) > 0 {
		names := make([]string, 0, len(cfg.Blurbs))
		for name := range cfg.Blurbs {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "  blurbs under %s\n", strings.Join(names, ", "))
	}
	return b.String()
}
