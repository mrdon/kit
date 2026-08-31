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
// Both tools are admin-only. The tap list is what a room full of customers
// reads prices off, so changing it is not a note-taking action.
func toolMetas() []services.ToolMeta {
	return []services.ToolMeta{
		{
			Name: "set_menu_board",
			Description: "Set the taproom tap list. The workspace menu already has a permanent " +
				"address — this replaces what it shows. The payload is one JSON document with " +
				"`venue` (wordmark, footer lines), `taps` (section, name, style, abv, price, size) " +
				"and `panels` (kind agenda/poster/cta). Omit key unless you are maintaining a " +
				"second, separate board. Changing the tap list changes what any screen already " +
				"showing this menu displays; no re-pointing is needed.",
			AdminOnly: true,
			Schema: services.PropsReq(map[string]any{
				"key": services.Field("string",
					"Only for an additional board separate from the workspace menu. Lowercase letters, numbers and hyphens. Omit for the main menu."),
				"name":    services.Field("string", "Human label for the board, e.g. 'Taproom wall'."),
				"payload": services.Field("string", "The whole board as a JSON document."),
			}, "payload"),
		},
		{
			Name: "set_menu_asset",
			Description: "Store an image the menu can show, by giving Kit a URL to fetch it from. " +
				"Kit downloads and keeps the bytes, so the board page stays self-contained and the " +
				"image does not have to travel inside every tap-list update. Reference it from a " +
				"poster panel as \"asset:<key>\". Re-run with the same key to replace it.",
			AdminOnly: true,
			Schema: services.PropsReq(map[string]any{
				"key": services.Field("string",
					"Short name to reference this image by, e.g. 'anniversary'."),
				"url": services.Field("string",
					"Public https URL of the image. Kit fetches it once; the page never fetches it."),
			}, "key", "url"),
		},
		{
			Name: "set_menu_source",
			Description: "Point the menu at an Untappd digital board so Kit pulls the tap list " +
				"automatically, every minute. Staff keep curating in Untappd exactly as they do " +
				"now; the board here follows. Pass board_id from the Untappd board URL " +
				"(business.untappd.com/boards/<id>). Pass an empty board_id to stop syncing and " +
				"go back to a hand-set tap list.",
			AdminOnly: true,
			Schema: services.Props(map[string]any{
				"board_id": services.Field("string",
					"Untappd digital board id, e.g. '22128'. Empty to stop syncing."),
				"key": services.Field("string",
					"Only for an additional board separate from the workspace menu."),
			}),
		},
		{
			Name: "get_menu_board",
			Description: "Show the workspace menu's public address and when its tap list was last " +
				"changed, plus any additional boards. Pass key to show just one.",
			AdminOnly: true,
			Schema: services.Props(map[string]any{
				"key": services.Field("string", "Only show the board with this key."),
			}),
		},
	}
}

// setBoardArgs is the shared input shape for set_menu_board.
type setBoardArgs struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Payload string `json:"payload"`
}

// getBoardArgs is the shared input shape for get_menu_board.
type getBoardArgs struct {
	Key string `json:"key"`
}

// setSourceArgs is the shared input shape for set_menu_source.
type setSourceArgs struct {
	BoardID string `json:"board_id"`
	Key     string `json:"key"`
}

// applySource points a board at Untappd and pulls once, so configuring it is
// also the test that it works — a wrong board id should fail here, in front of
// whoever typed it, rather than an hour later in a scheduled job's last_error.
func applySource(ctx context.Context, pool *pgxpool.Pool, a *App, tenantID uuid.UUID, args setSourceArgs) (string, error) {
	boardID := strings.TrimSpace(args.BoardID)
	kind := SourceUntappd
	if boardID == "" {
		kind = ""
	}
	row, res, err := a.SetSource(ctx, tenantID, args.Key, kind, boardID)
	if err != nil {
		return "", err
	}
	url, err := a.publicURL(ctx, pool, tenantID, row.Key)
	if err != nil {
		return "", err
	}
	if kind == "" {
		return fmt.Sprintf("Stopped syncing %q. Its tap list is now whatever was last set.", row.Name), nil
	}
	if res.Err != nil {
		return "", fmt.Errorf("board saved, but the first pull failed: %w", res.Err)
	}
	return fmt.Sprintf("Now following Untappd board %s — pulled %d taps.\n\nShowing at: %s\n\n"+
		"Kit re-checks every minute and updates the screen when the tap list changes.",
		boardID, res.Taps, url), nil
}

// setAssetArgs is the shared input shape for set_menu_asset.
type setAssetArgs struct {
	Key string `json:"key"`
	URL string `json:"url"`
}

// saveAsset fetches an image and stores it. Kit does the fetching rather than
// accepting bytes: an image is tens of kilobytes, and pushing that through a
// tool call every time is the cost this whole table exists to avoid.
//
// The fetch goes through web.Fetcher so it inherits the SSRF protection there
// -- a URL supplied by a caller must not be able to make Kit read its own
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
	return fmt.Sprintf("Stored %q — %s, %d KB.\n\nUse it from a poster panel as %q.",
		key, mime, len(data)/1024, AssetRef+key), nil
}

// saveBoard is the one code path both surfaces call, so the agent and MCP
// tools cannot drift on validation or on the message a user reads back.
func saveBoard(ctx context.Context, pool *pgxpool.Pool, a *App, tenantID uuid.UUID, args setBoardArgs) (string, error) {
	row, err := a.svc.Save(ctx, tenantID, BoardInput{
		Key:     args.Key,
		Name:    args.Name,
		Payload: []byte(args.Payload),
	})
	if err != nil {
		return "", err
	}
	board, err := ParseBoard(row.Payload)
	if err != nil {
		return "", err
	}
	url, err := a.publicURL(ctx, pool, tenantID, row.Key)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Tap list set — %d taps, %d panels.\n\nShowing at: %s\n\n"+
		"Any screen already pointed at that address is now showing it.",
		len(board.Taps), len(board.Panels), url), nil
}

// listBoards renders the board listing both surfaces return.
func listBoards(ctx context.Context, pool *pgxpool.Pool, a *App, tenantID uuid.UUID, args getBoardArgs) (string, error) {
	rows, err := a.svc.List(ctx, tenantID)
	if err != nil {
		return "", err
	}
	if key := strings.TrimSpace(args.Key); key != "" {
		rows = filterByKey(rows, key)
	}
	if len(rows) == 0 {
		return "The workspace menu has no tap list yet, so its screen shows a " +
			"placeholder. The address still works — set the taps with " +
			"set_menu_board and it will appear.", nil
	}
	var b strings.Builder
	for _, row := range rows {
		url, err := a.publicURL(ctx, pool, tenantID, row.Key)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s\n  %s\n  tap list changed %s\n",
			row.Name, url, row.UpdatedAt.Format("2 Jan 2006 15:04 MST"))
		if row.SourceKind != "" {
			fmt.Fprintf(&b, "  following Untappd board %s", row.SourceID)
			if row.SyncedAt != nil {
				fmt.Fprintf(&b, ", last checked %s", row.SyncedAt.Format("15:04:05 MST"))
			}
			b.WriteString("\n")
		}
		if row.SyncError != "" {
			fmt.Fprintf(&b, "  SYNC FAILING: %s\n", row.SyncError)
		}
		if board, err := ParseBoard(row.Payload); err == nil {
			fmt.Fprintf(&b, "  %d taps, %d panels\n", len(board.Taps), len(board.Panels))
		} else {
			fmt.Fprintf(&b, "  WILL NOT RENDER: %s\n", err)
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

func filterByKey(rows []*BoardRow, key string) []*BoardRow {
	key = strings.ToLower(key)
	for _, row := range rows {
		if row.Key == key {
			return []*BoardRow{row}
		}
	}
	return nil
}

// publicURL builds the absolute display URL. The slug comes from the tenant
// rather than the caller because it is part of the public path contract.
func (a *App) publicURL(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, key string) (string, error) {
	tenant, err := models.GetTenantByID(ctx, pool, tenantID)
	if err != nil {
		return "", fmt.Errorf("loading tenant for menu URL: %w", err)
	}
	if tenant == nil {
		return "", ErrNotFound
	}
	return strings.TrimRight(a.baseURL, "/") + PublicPath(tenant.Slug, key), nil
}
