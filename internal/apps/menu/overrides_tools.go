package menu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

// extrasToolMeta is set_menu_extras, shared by both surfaces. It replaces the
// extras alone, the way set_menu_panels replaces the panels alone, so adding
// one can never cost the printed menu its footer or featured beer.
func extrasToolMeta() services.ToolMeta {
	return services.ToolMeta{
		Name: "set_menu_extras",
		Description: "Replace the menu rows Untappd does not carry: canned non-alcoholics, sodas, " +
			"juice boxes. Both menus read this one list. Pass the whole `extras` array; it " +
			"replaces what is there, so include the rows you want to keep (get_menu_board lists " +
			"them as this tool takes them). Each row is {section, name, style, abv, pours: " +
			"[{size, label, price}], show}. `show` is \"print\" (the default), \"board\" for the " +
			"wall menu only, or \"both\": the wall has less room, so one board row can stand for " +
			"several cans that print one by one. A section matching one on the tap list joins it. " +
			"The wall shows one price per row and drops a row with none. To take a row off for " +
			"the day use set_menu_86, not show.",
		AdminOnly: true,
		Schema: services.PropsReq(map[string]any{
			"extras": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "object"},
				"description": "Every extra row, in menu order.",
			},
		}, "extras"),
	}
}

// saveExtras is the handler both surfaces call, with the raw arguments.
func saveExtras(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, raw []byte) (string, error) {
	extras, err := parseExtrasArgs(raw)
	if err != nil {
		return "", err
	}
	if err := validateExtras(extras); err != nil {
		return "", err
	}
	state, err := LoadPrintState(ctx, pool, tenantID)
	if err != nil {
		return "", err
	}
	cfg := state.Config
	cfg.Extras = extras
	if err := SavePrintConfig(ctx, pool, tenantID, cfg); err != nil {
		return "", err
	}
	return fmt.Sprintf("Menu extras saved: %d printed, %d on the wall.",
		len(printExtras(extras)), len(boardExtras(extras))), nil
}

// parseExtrasArgs accepts the array itself or the array as a JSON string,
// since clients differ on how a structured argument travels. Unknown fields
// fail, so a typo'd "shows" is reported rather than silently printed.
func parseExtrasArgs(raw []byte) ([]Beer, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%w: parsing set_menu_extras arguments: %w", ErrPayloadInvalid, err)
	}
	v, ok := m["extras"]
	if !ok {
		return nil, fmt.Errorf("%w: extras is required", ErrPayloadInvalid)
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		v = json.RawMessage(s)
	}
	dec := json.NewDecoder(bytes.NewReader(v))
	dec.DisallowUnknownFields()
	extras := []Beer{}
	if err := dec.Decode(&extras); err != nil {
		return nil, fmt.Errorf("%w: parsing extras: %w", ErrPayloadInvalid, err)
	}
	return extras, nil
}

// describeExtras lists the extras the way set_menu_extras takes them, so an
// edit starts from what is there rather than from a count.
func describeExtras(extras []Beer) string {
	if len(extras) == 0 {
		return ""
	}
	pretty, err := json.MarshalIndent(extras, "  ", "  ")
	if err != nil {
		return fmt.Sprintf("  (could not render extras: %v)\n", err)
	}
	return "\n  Extras, as set_menu_extras takes them:\n  " + string(pretty) + "\n"
}
