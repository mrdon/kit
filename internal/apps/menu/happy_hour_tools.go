package menu

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

// happyHourToolMetas are the two happy hour tools, shared by both surfaces.
//
// Setting and syncing are separate on purpose. The wall follows the setting
// the moment it is saved; Square is only written by a sync, and a sync with
// apply=false is a preview. So an agent can change the beers in the middle of
// a conversation without the register changing under the bar until somebody
// has read what will happen.
func happyHourToolMetas() []services.ToolMeta {
	strList := func(desc string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
	}
	return []services.ToolMeta{
		{
			Name: "set_menu_happy_hour",
			Description: "Set the taproom's happy hour: a fixed price on a few beers on a weekly " +
				"schedule. The menu board follows it on its own — during the window it shows a " +
				"happy hour banner and the happy hour price beside the struck-through regular one. " +
				"Square does NOT change until sync_menu_happy_hour is run. Only the fields you pass " +
				"change; `beers` replaces the whole list, so pass every beer that should be on it. " +
				"Beer names are as the menu board shows them, and must match a Square item's name " +
				"or kitchen name exactly (case and spacing aside).",
			AdminOnly: true,
			Schema: services.Props(map[string]any{
				"enabled":   services.Field("boolean", "Turn happy hour on or off."),
				"days":      strList("Days it runs: any of mon, tue, wed, thu, fri, sat, sun."),
				"start":     services.Field("string", "Start time, 24-hour local, e.g. '15:00'."),
				"end":       services.Field("string", "End time, 24-hour local, e.g. '17:00'. Must be later than start."),
				"starts_on": services.Field("string", "First day it runs, YYYY-MM-DD. Empty string for already running."),
				"price":     services.Field("string", "Happy hour price of one pour, e.g. '5' or '5.50'."),
				"size":      services.Field("string", "The pour the price applies to, as Square names the variation, e.g. '16oz'."),
				"beers":     strList("Every beer on happy hour, as the menu board names them."),
			}),
		},
		{
			Name: "sync_menu_happy_hour",
			Description: "Make Square ring the happy hour as currently set: an automatic discount " +
				"on each beer's pour, during the window, sized to land on the happy hour price. " +
				"Without apply=true it only previews what it would change — always preview first " +
				"and show the user the result before applying. With apply=true it replaces the " +
				"discount Kit created last time (or removes it, if happy hour is off) and returns " +
				"the full log, including Square's own error text if it refuses.",
			AdminOnly: true,
			Schema: services.Props(map[string]any{
				"apply": services.Field("boolean", "true to write to Square; false or absent to preview."),
			}),
		},
	}
}

// setHappyHourArgs is the shared input. Pointers and nil slices mean "leave
// as it is", so changing one beer does not require restating the schedule.
type setHappyHourArgs struct {
	Enabled  *bool
	Days     []string
	Start    *string
	End      *string
	StartsOn *string
	Price    *string
	Size     *string
	Beers    []string
}

// parseHappyHourArgs reads the tool input. Lists are accepted as arrays, as a
// JSON string holding an array, or comma-separated, because clients differ.
func parseHappyHourArgs(raw []byte) (setHappyHourArgs, error) {
	var args setHappyHourArgs
	if len(raw) == 0 {
		return args, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return args, fmt.Errorf("parsing set_menu_happy_hour arguments: %w", err)
	}
	if v, ok := m["enabled"]; ok {
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			return args, fmt.Errorf("enabled must be true or false: %w", err)
		}
		args.Enabled = &b
	}
	for key, dst := range map[string]**string{
		"start": &args.Start, "end": &args.End, "starts_on": &args.StartsOn,
		"price": &args.Price, "size": &args.Size,
	} {
		if v, ok := m[key]; ok {
			s, err := scalarString(v)
			if err != nil {
				return args, fmt.Errorf("%s: %w", key, err)
			}
			*dst = &s
		}
	}
	var err error
	if v, ok := m["days"]; ok {
		if args.Days, err = stringList(v); err != nil {
			return args, fmt.Errorf("days: %w", err)
		}
		for i, d := range args.Days {
			args.Days[i] = strings.ToLower(strings.TrimSpace(d))
		}
	}
	if v, ok := m["beers"]; ok {
		if args.Beers, err = stringList(v); err != nil {
			return args, fmt.Errorf("beers: %w", err)
		}
	}
	return args, nil
}

// scalarString accepts a JSON string or number ("5" or 5).
func scalarString(v json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return strings.TrimSpace(s), nil
	}
	var f float64
	if err := json.Unmarshal(v, &f); err != nil {
		return "", fmt.Errorf("want a string, got %s", v)
	}
	return strconv.FormatFloat(f, 'f', -1, 64), nil
}

func stringList(v json.RawMessage) ([]string, error) {
	var list []string
	if err := json.Unmarshal(v, &list); err == nil {
		return list, nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return nil, fmt.Errorf("want a list of strings, got %s", v)
	}
	if err := json.Unmarshal([]byte(s), &list); err == nil {
		return list, nil
	}
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			list = append(list, p)
		}
	}
	return list, nil
}

// parsePrice reads "5", "$5.50" or "5.5" into cents.
func parsePrice(s string) (int, error) {
	f, err := strconv.ParseFloat(strings.TrimPrefix(strings.TrimSpace(s), "$"), 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("%w: price %q is not an amount like 5 or 5.50", ErrPayloadInvalid, s)
	}
	return int(math.Round(f * 100)), nil
}

// merge applies the given fields over the current setting.
func (args setHappyHourArgs) merge(h HappyHour) (HappyHour, error) {
	if args.Enabled != nil {
		h.Enabled = *args.Enabled
	}
	if args.Days != nil {
		h.Days = args.Days
	}
	if args.Start != nil {
		h.Start = *args.Start
	}
	if args.End != nil {
		h.End = *args.End
	}
	if args.StartsOn != nil {
		h.StartsOn = *args.StartsOn
	}
	if args.Size != nil {
		h.Size = *args.Size
	}
	if args.Beers != nil {
		h.Beers = args.Beers
	}
	if args.Price != nil {
		c, err := parsePrice(*args.Price)
		if err != nil {
			return h, err
		}
		h.PriceCents = c
	}
	return h, nil
}

// storeHappyHour validates and saves a setting, for the tool and the console.
func storeHappyHour(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, h HappyHour) (HappyHour, error) {
	h.Normalize()
	if err := h.Validate(); err != nil {
		return h, err
	}
	return h, SaveHappyHour(ctx, pool, tenantID, h)
}

// setHappyHour is the handler both surfaces call for set_menu_happy_hour.
func setHappyHour(ctx context.Context, pool *pgxpool.Pool, a *App, tenantID uuid.UUID, raw []byte) (string, error) {
	args, err := parseHappyHourArgs(raw)
	if err != nil {
		return "", err
	}
	state, err := LoadHappyHour(ctx, pool, tenantID)
	if err != nil {
		return "", err
	}
	h, err := args.merge(state.Config)
	if err != nil {
		return "", err
	}
	if h, err = storeHappyHour(ctx, pool, tenantID, h); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(h.Summary() + ".\n")
	if missing := a.beersOffBoard(ctx, tenantID, h); len(missing) > 0 {
		fmt.Fprintf(&b, "Not on the menu board right now, so the wall will not mark them: %s.\n",
			strings.Join(missing, ", "))
	}
	b.WriteString("The menu board follows this already. Square has NOT changed: run " +
		"sync_menu_happy_hour to preview, then again with apply=true.")
	return b.String(), nil
}

// syncHappyHourTool is the handler both surfaces call for sync_menu_happy_hour.
func syncHappyHourTool(ctx context.Context, a *App, tenantID uuid.UUID, raw []byte) (string, error) {
	var args struct {
		Apply bool `json:"apply"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parsing sync_menu_happy_hour arguments: %w", err)
		}
	}
	log, ok := a.SyncHappyHour(ctx, tenantID, args.Apply)
	if !ok && args.Apply {
		return "", fmt.Errorf("square sync did not complete:\n%s", log)
	}
	return log, nil
}

// beersOffBoard lists happy hour beers that are not on the stored tap list.
func (a *App) beersOffBoard(ctx context.Context, tenantID uuid.UUID, h HappyHour) []string {
	row, err := GetBoard(ctx, a.pool, tenantID)
	if err != nil || row == nil {
		return nil
	}
	board, err := ParseBoard(row.Payload)
	if err != nil {
		return nil
	}
	var missing []string
	for _, beer := range h.Beers {
		found := false
		for _, t := range board.Taps {
			if nameKey(t.Name) == nameKey(beer) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, beer)
		}
	}
	return missing
}

// describeHappyHour is the happy hour's part of get_menu_board.
func describeHappyHour(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) string {
	state, err := LoadHappyHour(ctx, pool, tenantID)
	if err != nil {
		return fmt.Sprintf("\nHappy hour: could not load (%v)\n", err)
	}
	if !state.Configured {
		return "\nHappy hour: not set up (set_menu_happy_hour)\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s.\n", state.Config.Summary())
	switch {
	case state.SyncedAt == nil:
		b.WriteString("  Square: never synced (sync_menu_happy_hour)\n")
	case state.InSync():
		fmt.Fprintf(&b, "  Square: in step, synced %s\n", state.SyncedAt.Format("2 Jan 2006 15:04 MST"))
	case !state.SyncOK:
		fmt.Fprintf(&b, "  Square: LAST SYNC FAILED %s — see sync_menu_happy_hour output\n",
			state.SyncedAt.Format("2 Jan 2006 15:04 MST"))
	default:
		b.WriteString("  Square: out of date — the setting changed since the last sync\n")
	}
	return b.String()
}
