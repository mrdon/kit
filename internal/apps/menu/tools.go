package menu

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// toolMetas is the shared ToolMeta list for the agent + MCP surfaces.
//
// Both tools are admin-only. A menu board is what a room full of customers
// reads prices off, so changing one is a publishing act, not a note.
func toolMetas() []services.ToolMeta {
	return []services.ToolMeta{
		{
			Name: "set_menu_board",
			Description: "Publish a taproom menu board: replaces the whole board for the given key " +
				"and returns the public URL to display it. The payload is one JSON document with " +
				"`venue` (wordmark, footer lines), `taps` (section, name, style, abv, price, size) " +
				"and `panels` (kind agenda/poster/cta) — see an existing board for the shape. " +
				"Publishing does NOT put the board on a screen; hand the returned URL to whoever " +
				"manages the kiosk boards.",
			AdminOnly: true,
			Schema: services.PropsReq(map[string]any{
				"key": services.Field("string",
					"Stable board key used in the public URL, e.g. 'taproom'. Lowercase letters, numbers and hyphens. Defaults to a slug of name."),
				"name":    services.Field("string", "Human label for the board, e.g. 'Taproom wall'."),
				"payload": services.Field("string", "The whole board as a JSON document."),
			}, "payload"),
		},
		{
			Name: "get_menu_board",
			Description: "List this workspace's menu boards with their public URLs and when each was " +
				"last published. Pass key to show just one.",
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
	return fmt.Sprintf("Published %q — %d taps, %d panels.\n\nDisplay it at: %s\n\n"+
		"To put it on a screen, set that URL as a kiosk board's URL.",
		row.Name, len(board.Taps), len(board.Panels), url), nil
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
		return "No menu boards yet. Publish one with set_menu_board.", nil
	}
	var b strings.Builder
	for _, row := range rows {
		url, err := a.publicURL(ctx, pool, tenantID, row.Key)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s (%s)\n  %s\n  published %s\n",
			row.Name, row.Key, url, row.UpdatedAt.Format("2 Jan 2006 15:04 MST"))
		if board, err := ParseBoard(row.Payload); err == nil {
			fmt.Fprintf(&b, "  %d taps, %d panels\n", len(board.Taps), len(board.Panels))
		} else {
			fmt.Fprintf(&b, "  WILL NOT RENDER: %s\n", err)
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
