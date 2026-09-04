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

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/web"
)

// toolMetas is the shared ToolMeta list for the agent + MCP surfaces.
//
// All admin-only. The tap list is what a room full of customers reads prices
// off, so changing it is not a note-taking action.
//
// None of them take a board id, because a workspace has one menu.
func toolMetas() []services.ToolMeta {
	return []services.ToolMeta{
		{
			Name: "set_menu_source",
			Description: "Point the menu at an Untappd digital board so Kit pulls the tap list " +
				"automatically. Staff keep curating in Untappd exactly as they do now; the menu " +
				"follows, refreshing when a screen asks and at most once a minute. Pass board_id " +
				"from the Untappd board URL (business.untappd.com/boards/<id>). Pass an empty " +
				"board_id to stop following it and keep whatever tap list is showing.",
			AdminOnly: true,
			Schema: services.Props(map[string]any{
				"board_id": services.Field("string",
					"Untappd digital board id, e.g. '22128'. Empty to stop following."),
			}),
		},
		{
			Name: "set_menu_board",
			Description: "Set the menu by hand, replacing what it shows. Normally unnecessary — " +
				"prefer set_menu_source and let the tap list follow Untappd. The payload is one " +
				"JSON document with `venue` (wordmark, footer lines), `taps` (section, name, " +
				"style, abv, price, size) and `panels` (kind agenda/poster/cta). If the menu " +
				"follows Untappd the taps here are replaced on the next refresh; the venue " +
				"chrome and panels are not, and this is how you change those.",
			AdminOnly: true,
			Schema: services.PropsReq(map[string]any{
				"name":    services.Field("string", "Human label, shown in the console. Defaults to 'Menu'."),
				"payload": services.Field("string", "The whole menu as a JSON document."),
			}, "payload"),
		},
		{
			Name: "set_menu_asset",
			Description: "Store an image the menu can show, by giving Kit a URL to fetch it from. " +
				"Kit downloads and keeps the bytes, so the board page stays self-contained and the " +
				"image does not travel inside every tap-list update. Reference it from a panel as " +
				"\"asset:<key>\". Re-run with the same key to replace it.",
			AdminOnly: true,
			Schema: services.PropsReq(map[string]any{
				"key": services.Field("string", "Short name to reference this image by, e.g. 'anniversary'."),
				"url": services.Field("string", "Public https URL of the image. Kit fetches it once; the page never fetches it."),
			}, "key", "url"),
		},
		{
			Name: "set_menu_print",
			Description: "Configure the printed menu — the letter-sized PDF at /<slug>/menu/print.pdf. " +
				"The beers, prices and ABVs come from the same Untappd board the screen follows; " +
				"this is everything that is not on it. The payload is one JSON document: `brand` " +
				"(the brewery's untappd.com slug, e.g. 'gravitybrewing' — without it the menu " +
				"prints no beer descriptions, because the digital board carries none), `title` and " +
				"`subtitle` (the masthead, e.g. 'Beers' / '& Beverages'), `flight` (the line above " +
				"the footer), `foot_left` and `foot_right` (the wifi and social lines), `colors` " +
				"(section heading name to #rrggbb), `notes` (beer name to a description you " +
				"write yourself, which wins over Untappd — use it for anything Untappd has no " +
				"page for), `blurbs` (section heading name to one sentence printed under that " +
				"heading — a heading named here that is not on the tap list becomes a section " +
				"of its own at the end, which is how snacks go on a beer menu without a row " +
				"and a price each), `hero` (the key of an image stored with " +
				"set_menu_asset, used behind the masthead), and `extras` (rows Untappd does not " +
				"carry — canned non-alcoholics, sodas, juice boxes — each with section, name, " +
				"style and pours: [{size, label, price}]). Replaces the whole configuration.",
			AdminOnly: true,
			Schema: services.PropsReq(map[string]any{
				"payload": services.Field("string", "The printed menu's configuration as a JSON document."),
			}, "payload"),
		},
		{
			Name: "sync_menu_print",
			Description: "Refresh the printed menu's tap list from the Untappd board. The printed " +
				"menu no longer fetches anything when it is opened — it renders what was last " +
				"synced — so this is what puts a new beer on the paper. It reads the digital " +
				"board for beers, sections, prices and ABVs, and tries the brewery's untappd.com " +
				"pages for descriptions. Those two do not fail together: the board answers " +
				"anybody, while the consumer site sits behind bot management and usually refuses " +
				"a server, so a sync that gets the beers and no prose is normal. It reports which " +
				"beers have no description; fill those with set_menu_notes.",
			AdminOnly: true,
			Schema:    services.Props(map[string]any{}),
		},
		{
			Name: "set_menu_notes",
			Description: "Store beer descriptions for the printed menu, merging them into what is " +
				"already there rather than replacing it — unlike set_menu_print, which replaces " +
				"the whole configuration. Use this to fill in the beers sync_menu_print reports " +
				"as having no description. It is the way an agent running somewhere untappd.com " +
				"will actually talk to — a laptop rather than a datacenter — can crawl the " +
				"brewery's beer pages and push the prose into Kit. Names are matched loosely " +
				"against the tap list, so the board's spelling and Untappd's need not agree.",
			AdminOnly: true,
			Schema: services.PropsReq(map[string]any{
				"notes": services.Field("object",
					"Beer name to description, e.g. {\"Newtonian\": \"Award winning British style ale.\"}. "+
						"Merged into what is stored; an empty description clears that beer's."),
			}, "notes"),
		},
		{
			Name: "get_menu_board",
			Description: "Show the menu's public address, what it is following, when it last " +
				"changed, any stored images, and how the printed menu is configured.",
			AdminOnly: true,
			Schema:    services.Props(map[string]any{}),
		},
	}
}

// setSourceArgs is the shared input shape for set_menu_source.
type setSourceArgs struct {
	BoardID string `json:"board_id"`
}

// setBoardArgs is the shared input shape for set_menu_board.
type setBoardArgs struct {
	Name    string `json:"name"`
	Payload string `json:"payload"`
}

// setPrintArgs is the shared input shape for set_menu_print.
type setPrintArgs struct {
	Payload string `json:"payload"`
}

// setNotesArgs is the shared input shape for set_menu_notes.
type setNotesArgs struct {
	Notes map[string]string `json:"notes"`
}

// setAssetArgs is the shared input shape for set_menu_asset.
type setAssetArgs struct {
	Key string `json:"key"`
	URL string `json:"url"`
}

// applySource points the menu at Untappd and pulls once, so configuring it is
// also the test that it works — a wrong board id fails here, in front of
// whoever typed it, rather than later as a stale screen.
func applySource(ctx context.Context, pool *pgxpool.Pool, a *App, tenantID uuid.UUID, args setSourceArgs) (string, error) {
	boardID := strings.TrimSpace(args.BoardID)
	kind := SourceUntappd
	if boardID == "" {
		kind = ""
	}
	_, res, err := a.SetSource(ctx, tenantID, kind, boardID)
	if err != nil {
		return "", err
	}
	url, err := a.publicURL(ctx, pool, tenantID)
	if err != nil {
		return "", err
	}
	if kind == "" {
		return "The menu no longer follows Untappd. It keeps showing the tap list it has.", nil
	}
	if res.Err != nil {
		return "", fmt.Errorf("source saved, but the first pull failed: %w", res.Err)
	}
	return fmt.Sprintf("Now following Untappd board %s — pulled %d taps.\n\nShowing at: %s\n\n"+
		"The menu re-checks when a screen asks, at most once a minute.",
		boardID, res.Taps, url), nil
}

// saveBoard is the one code path both surfaces call, so the agent and MCP
// tools cannot drift on validation or on the message a user reads back.
func saveBoard(ctx context.Context, pool *pgxpool.Pool, a *App, tenantID uuid.UUID, args setBoardArgs) (string, error) {
	row, err := a.svc.Save(ctx, tenantID, args.Name, []byte(args.Payload))
	if err != nil {
		return "", err
	}
	board, err := ParseBoard(row.Payload)
	if err != nil {
		return "", err
	}
	url, err := a.publicURL(ctx, pool, tenantID)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Menu set — %d taps, %d panels.\n\nShowing at: %s",
		len(board.Taps), len(board.Panels), url)
	if row.SourceKind != "" {
		msg += fmt.Sprintf("\n\nNote: this menu follows Untappd board %s, so these taps are "+
			"replaced on the next refresh. The venue chrome and panels stay.", row.SourceID)
	}
	return msg, nil
}

// saveAsset fetches an image and stores it. Kit does the fetching rather than
// accepting bytes: an image is tens of kilobytes, and pushing that through a
// tool call every time is the cost the asset store exists to avoid.
//
// The fetch goes through web.Fetcher so it inherits the SSRF protection there
// — a URL supplied by a caller must not be able to make Kit read its own
// private network.
func saveAsset(ctx context.Context, pool *pgxpool.Pool, f *web.Fetcher, tenantID uuid.UUID, args setAssetArgs) (string, error) {
	key := strings.ToLower(strings.TrimSpace(args.Key))
	if !keyPattern.MatchString(key) {
		return "", ErrKeyInvalid
	}
	if f == nil {
		return "", errors.New("this surface cannot fetch images")
	}
	data, mime, err := f.FetchImage(ctx, strings.TrimSpace(args.URL))
	if err != nil {
		return "", err
	}
	asset := &Asset{Key: key, Mime: mime, Bytes: data, SourceURL: strings.TrimSpace(args.URL)}
	if err := UpsertAsset(ctx, pool, tenantID, asset); err != nil {
		return "", err
	}
	return fmt.Sprintf("Stored %q — %s, %d KB.\n\nUse it from a panel as %q.",
		key, mime, len(data)/1024, AssetRef+key), nil
}

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
	if err := SavePrintConfig(ctx, pool, tenantID, cfg); err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Printed menu configured — %s, %s, %s.",
		plural(len(cfg.Extras), "extra row"),
		plural(len(cfg.Colors), "section colour"),
		plural(len(cfg.Blurbs), "blurb"))
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
			if strings.TrimSpace(cfg.Notes[r.Name]) == "" && strings.TrimSpace(state.Notes[normalizeBeerName(r.Name)]) == "" {
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

// describeBoard renders the status both surfaces return.
func describeBoard(ctx context.Context, pool *pgxpool.Pool, a *App, tenantID uuid.UUID) (string, error) {
	url, err := a.publicURL(ctx, pool, tenantID)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	row, err := a.svc.Get(ctx, tenantID)
	switch {
	case errors.Is(err, ErrNotFound):
		fmt.Fprintf(&b, "The menu has no tap list yet, so its screen shows a placeholder.\n"+
			"The address already works: %s\n\nPoint it at Untappd with set_menu_source.\n", url)
	case err != nil:
		return "", err
	default:
		fmt.Fprintf(&b, "%s\n  %s\n  last changed %s\n",
			row.Name, url, row.UpdatedAt.Format("2 Jan 2006 15:04 MST"))
		if row.SourceKind != "" {
			fmt.Fprintf(&b, "  following Untappd board %s", row.SourceID)
			if row.SyncedAt != nil {
				fmt.Fprintf(&b, ", last checked %s", row.SyncedAt.Format("15:04:05 MST"))
			}
			b.WriteString("\n")
		} else {
			b.WriteString("  set by hand — not following anything\n")
		}
		if row.SyncError != "" {
			fmt.Fprintf(&b, "  SYNC FAILING: %s\n", row.SyncError)
		}
		if board, perr := ParseBoard(row.Payload); perr == nil {
			fmt.Fprintf(&b, "  %d taps, %d panels\n", len(board.Taps), len(board.Panels))
		} else {
			fmt.Fprintf(&b, "  WILL NOT RENDER: %s\n", perr)
		}
	}

	b.WriteString(describePrint(ctx, pool, tenantID))

	assets, err := ListAssetKeys(ctx, a.pool, tenantID)
	if err != nil {
		return "", err
	}
	if len(assets) > 0 {
		b.WriteString("\nStored images:\n")
		for _, as := range assets {
			fmt.Fprintf(&b, "  %s%s — %s, %d KB\n", AssetRef, as.Key, as.Mime, as.Size/1024)
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// publicURL builds the absolute display URL. The slug comes from the tenant
// rather than the caller because it is part of the public path contract.
func (a *App) publicURL(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (string, error) {
	tenant, err := models.GetTenantByID(ctx, pool, tenantID)
	if err != nil {
		return "", fmt.Errorf("loading tenant for menu URL: %w", err)
	}
	if tenant == nil {
		return "", ErrNotFound
	}
	return strings.TrimRight(a.baseURL, "/") + PublicPath(tenant.Slug), nil
}
