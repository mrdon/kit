package menu

import (
	"context"
	"errors"
	"fmt"
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
			Name: "get_menu_board",
			Description: "Show the menu's public address, what it is following, when it last " +
				"changed, and any stored images.",
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
