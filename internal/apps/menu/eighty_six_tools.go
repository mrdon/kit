package menu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

// eightySixToolMeta is set_menu_86, shared by both surfaces.
//
// Not admin only: whoever is pouring is the one who finds the keg blown, and
// the whole point is that they can say so from their phone without finding a
// manager. The worst a wrong 86 does is hide a beer that is still pouring,
// which the person standing in front of the wall sees at once.
func eightySixToolMeta() services.ToolMeta {
	strList := func(desc string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
	}
	return services.ToolMeta{
		Name: "set_menu_86",
		Description: "86 a beer: take it off the menu board and the printed menu because it has " +
			"run out, without waiting for someone to edit Untappd. Pass `add` to 86 beers and " +
			"`remove` to bring them back. A name may be part of the board's name (\"hazy\" for " +
			"\"Hazy Daze IPA\") as long as only one beer on the board matches; otherwise the " +
			"error lists the board so you can pick. Once Untappd itself drops the beer, the 86 " +
			"clears on its own. Call with no arguments to see what is 86'd.",
		Schema: services.Props(map[string]any{
			"add":    strList("Beers that have run out."),
			"remove": strList("Beers that are back."),
		}),
	}
}

type eightySixArgs struct {
	Add    []string
	Remove []string
}

func parseEightySixArgs(raw []byte) (eightySixArgs, error) {
	var args eightySixArgs
	if len(raw) == 0 {
		return args, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return args, fmt.Errorf("parsing set_menu_86 arguments: %w", err)
	}
	for key, dst := range map[string]*[]string{"add": &args.Add, "remove": &args.Remove} {
		v, ok := m[key]
		if !ok {
			continue
		}
		list, err := stringList(v)
		if err != nil {
			return args, fmt.Errorf("%s: %w", key, err)
		}
		*dst = list
	}
	return args, nil
}

// match86 finds the one name in candidates that s means: an exact match
// ignoring case and spacing, or else the only one containing s.
func match86(s string, candidates []string) (string, bool) {
	key := nameKey(s)
	if key == "" {
		return "", false
	}
	var hits []string
	for _, c := range candidates {
		if nameKey(c) == key {
			return c, true
		}
		if strings.Contains(nameKey(c), key) {
			hits = append(hits, c)
		}
	}
	if len(hits) == 1 {
		return hits[0], true
	}
	return "", false
}

// resolveAll maps every name onto candidates, or fails naming what did not
// match and listing what could have.
func resolveAll(names, candidates []string, what string) ([]string, error) {
	out := make([]string, 0, len(names))
	var missed []string
	for _, n := range names {
		if hit, ok := match86(n, candidates); ok {
			out = append(out, hit)
		} else {
			missed = append(missed, n)
		}
	}
	if len(missed) > 0 {
		return nil, fmt.Errorf("%w: no single %s matches %s. Choose from: %s",
			ErrPayloadInvalid, what, strings.Join(missed, ", "), strings.Join(candidates, ", "))
	}
	return out, nil
}

// tapNames is the stored board's tap list, before any 86 is applied.
func (a *App) tapNames(ctx context.Context, tenantID uuid.UUID) ([]string, error) {
	row, err := GetBoard(ctx, a.pool, tenantID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("%w: the menu board has no tap list yet", ErrPayloadInvalid)
	}
	board, err := ParseBoard(row.Payload)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(board.Taps))
	for _, t := range board.Taps {
		names = append(names, t.Name)
	}
	return names, nil
}

// setEightySix is the handler both surfaces call.
func setEightySix(ctx context.Context, pool *pgxpool.Pool, a *App, tenantID uuid.UUID, raw []byte) (string, error) {
	args, err := parseEightySixArgs(raw)
	if err != nil {
		return "", err
	}
	current, err := LoadEightySix(ctx, pool, tenantID)
	if err != nil {
		return "", err
	}
	if len(args.Add) == 0 && len(args.Remove) == 0 {
		return eightySixSummary(current), nil
	}
	taps, err := a.tapNames(ctx, tenantID)
	if err != nil {
		return "", err
	}
	add, err := resolveAll(args.Add, taps, "beer on the board")
	if err != nil {
		return "", err
	}
	remove, err := resolveAll(args.Remove, current, "86'd beer")
	if err != nil {
		return "", err
	}
	drop := glutenReducedKeys(remove)
	next := make([]string, 0, len(current)+len(add))
	for _, b := range append(append([]string(nil), current...), add...) {
		if !drop[nameKey(b)] {
			next = append(next, b)
		}
	}
	next = tidyBeerNames(next)
	if err := SaveEightySix(ctx, pool, tenantID, next); err != nil {
		return "", err
	}
	return eightySixSummary(next) + "\nThe menu board follows this already; the printed menu " +
		"picks it up on the next print.", nil
}

func eightySixSummary(beers []string) string {
	if len(beers) == 0 {
		return "Nothing is 86'd."
	}
	return "86'd: " + strings.Join(beers, ", ") + "."
}

// describeEightySix is the list's part of get_menu_board.
func describeEightySix(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) string {
	beers, err := LoadEightySix(ctx, pool, tenantID)
	if err != nil {
		return fmt.Sprintf("\n86'd: could not load (%v)\n", err)
	}
	if len(beers) == 0 {
		return "\n86'd: nothing (set_menu_86)\n"
	}
	return "\n" + eightySixSummary(beers) + "\n"
}
