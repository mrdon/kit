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
// Happy hour is on or off right now, and three things move it: the schedule
// (on at each start, off at each end), and Start now / End now by hand. The
// most recent wins. The board and Square both follow that state by
// themselves -- Square within a minute -- so set_menu_happy_hour is the only
// tool needed day to day. sync_menu_happy_hour exists to preview what Square
// will ring, and to push straight away and read the log when it fails.
func happyHourToolMetas() []services.ToolMeta {
	strList := func(desc string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
	}
	return []services.ToolMeta{
		{
			Name: "set_menu_happy_hour",
			Description: "Set or run the taproom's happy hour: a fixed price on a few beers. It is " +
				"on or off right now; the schedule turns it on at each start time and off at each " +
				"end, and now='start' / now='end' does the same by hand, holding until the next " +
				"scheduled start or end. While it is on, the menu board shows a banner and each " +
				"beer's happy hour price, and Square applies a discount that lands on that price " +
				"(Kit updates Square within a minute of any change, including a change to the " +
				"beers or price while it is on). Only the fields you pass change; `beers` replaces " +
				"the whole list. Beer names are as the menu board shows them, and must match a " +
				"Square item's name or kitchen name exactly (case and spacing aside).",
			AdminOnly: true,
			Schema: services.Props(map[string]any{
				"now": services.Field("string", "'start' to start happy hour now, 'end' to end it now. "+
					"Either holds until the next scheduled start or end."),
				"enabled":   services.Field("boolean", "Run on the schedule: true and it turns itself on and off at the times below."),
				"days":      strList("Scheduled days: any of mon, tue, wed, thu, fri, sat, sun."),
				"start":     services.Field("string", "Scheduled start, 24-hour local, e.g. '15:00'."),
				"end":       services.Field("string", "Scheduled end, 24-hour local, e.g. '17:00'. Must be later than start."),
				"starts_on": services.Field("string", "First scheduled day, YYYY-MM-DD. Empty string for no start date."),
				"price":     services.Field("string", "Happy hour price of one pour, e.g. '5' or '5.50'."),
				"size":      services.Field("string", "The pour the price applies to, as Square names the variation, e.g. '16oz'."),
				"beers":     strList("Every beer on happy hour, as the menu board names them."),
			}),
		},
		{
			Name: "sync_menu_happy_hour",
			Description: "Check Square against happy hour. Without apply it previews, beer by beer, " +
				"what Square rings while happy hour is on, and changes nothing. With apply=true it " +
				"pushes Square to the state happy hour is in right now (discount present while on, " +
				"removed while off) and returns the full log, Square's own error text included. " +
				"Kit does this by itself every minute, so apply is for debugging a failure.",
			AdminOnly: true,
			Schema: services.Props(map[string]any{
				"apply": services.Field("boolean", "true to push to Square now; false or absent to preview."),
			}),
		},
	}
}

// setHappyHourArgs is the shared input. Pointers and nil slices mean "leave
// as it is", so changing one beer does not require restating the schedule.
type setHappyHourArgs struct {
	Now      string // "start", "end" or ""
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
	if v, ok := m["now"]; ok {
		if s, err := scalarString(v); err == nil {
			args.Now = strings.ToLower(s)
		}
		if args.Now != "" && args.Now != "start" && args.Now != "end" {
			return args, fmt.Errorf("%w: now must be 'start' or 'end'", ErrPayloadInvalid)
		}
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
	if missing := a.beersOffBoard(ctx, tenantID, h); len(missing) > 0 {
		fmt.Fprintf(&b, "Not on the menu board right now, so the wall will not mark them: %s.\n",
			strings.Join(missing, ", "))
	}
	if args.Now != "" {
		log, ok, err := a.setHappyLive(ctx, tenantID, args.Now == "start")
		if err != nil {
			return "", err
		}
		if !ok {
			fmt.Fprintf(&b, "The board has changed, but Square did not:\n%s\n\n", log)
		}
	}
	return b.String() + describeHappyHour(ctx, a, tenantID), nil
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
func describeHappyHour(ctx context.Context, a *App, tenantID uuid.UUID) string {
	state, err := LoadHappyHour(ctx, a.pool, tenantID)
	if err != nil {
		return fmt.Sprintf("\nHappy hour: could not load (%v)\n", err)
	}
	if !state.Configured {
		return "\nHappy hour: not set up (set_menu_happy_hour)\n"
	}
	loc, now := a.tenantLocation(ctx, tenantID), timeNow()
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s.\n", state.Config.Summary())
	if state.OnAt(now, loc) {
		if until := state.Config.Until(now, loc); until != "" {
			fmt.Fprintf(&b, "  ON NOW, until %s\n", until)
		} else {
			b.WriteString("  ON NOW, until someone ends it\n")
		}
	} else {
		b.WriteString("  off right now\n")
	}
	switch {
	case state.SyncedAt == nil:
		b.WriteString("  Square: nothing pushed yet\n")
	case state.InSync(now, loc):
		fmt.Fprintf(&b, "  Square: in step, synced %s\n", state.SyncedAt.Format("2 Jan 2006 15:04 MST"))
	case !state.SyncOK:
		fmt.Fprintf(&b, "  Square: LAST SYNC FAILED %s — see sync_menu_happy_hour output\n",
			state.SyncedAt.Format("2 Jan 2006 15:04 MST"))
	default:
		b.WriteString("  Square: catching up — Kit updates it within a minute\n")
	}
	return b.String()
}
