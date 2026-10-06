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

// glutenReducedToolMeta is set_menu_gluten_reduced, shared by both surfaces.
//
// It takes add and remove as well as a whole list, because the way this gets
// said at the bar is one beer at a time -- "the new lager is gluten reduced"
// -- and an agent should not have to read the list back and restate it to
// say that.
func glutenReducedToolMeta() services.ToolMeta {
	strList := func(desc string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
	}
	return services.ToolMeta{
		Name: "set_menu_gluten_reduced",
		Description: "Mark which beers are gluten reduced. The menu board shows a GR badge beside " +
			"each one, with \"" + GlutenReducedNote + "\" in the footer, and the printed menu puts " +
			"the same line in the beer's description. Kept by beer name, so a beer keeps its mark " +
			"when it goes off tap and comes back. Pass `add` and `remove` to change a few, or " +
			"`beers` to replace the whole list (an empty list clears it). Names are as the menu " +
			"board shows them, matched ignoring only case and spacing. Never describe these beers " +
			"as gluten free.",
		AdminOnly: true,
		Schema: services.Props(map[string]any{
			"beers":  strList("The whole list, replacing what is stored."),
			"add":    strList("Beers to mark gluten reduced."),
			"remove": strList("Beers to unmark."),
		}),
	}
}

// glutenReducedArgs is the shared input. A nil Beers means "keep the list".
type glutenReducedArgs struct {
	Beers  []string
	Add    []string
	Remove []string
}

func parseGlutenReducedArgs(raw []byte) (glutenReducedArgs, error) {
	var args glutenReducedArgs
	if len(raw) == 0 {
		return args, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return args, fmt.Errorf("parsing set_menu_gluten_reduced arguments: %w", err)
	}
	for key, dst := range map[string]*[]string{"beers": &args.Beers, "add": &args.Add, "remove": &args.Remove} {
		v, ok := m[key]
		if !ok {
			continue
		}
		list, err := stringList(v)
		if err != nil {
			return args, fmt.Errorf("%s: %w", key, err)
		}
		if list == nil {
			list = []string{}
		}
		*dst = list
	}
	return args, nil
}

// apply works out the new list: replace, then add, then remove.
func (args glutenReducedArgs) apply(current []string) []string {
	list := current
	if args.Beers != nil {
		list = args.Beers
	}
	list = append(append([]string(nil), list...), args.Add...)
	drop := glutenReducedKeys(args.Remove)
	out := make([]string, 0, len(list))
	for _, b := range list {
		if !drop[nameKey(b)] {
			out = append(out, b)
		}
	}
	return tidyBeerNames(out)
}

// setGlutenReduced is the handler both surfaces call.
func setGlutenReduced(ctx context.Context, pool *pgxpool.Pool, a *App, tenantID uuid.UUID, raw []byte) (string, error) {
	args, err := parseGlutenReducedArgs(raw)
	if err != nil {
		return "", err
	}
	if args.Beers == nil && args.Add == nil && args.Remove == nil {
		return "", fmt.Errorf("%w: pass beers, add or remove", ErrPayloadInvalid)
	}
	current, err := LoadGlutenReduced(ctx, pool, tenantID)
	if err != nil {
		return "", err
	}
	beers := args.apply(current)
	if err := SaveGlutenReduced(ctx, pool, tenantID, beers); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(glutenReducedSummary(beers) + "\n")
	if off := a.namesOffBoard(ctx, tenantID, beers); len(off) > 0 {
		fmt.Fprintf(&b, "Not on tap right now, so not marked until they are back: %s. "+
			"Check the spelling against the board if any of them should be pouring.\n",
			strings.Join(off, ", "))
	}
	b.WriteString("The menu board follows this already; the printed menu picks it up on the next print.")
	return b.String(), nil
}

func glutenReducedSummary(beers []string) string {
	if len(beers) == 0 {
		return "No beers are marked gluten reduced."
	}
	return "Gluten reduced: " + strings.Join(beers, ", ") + "."
}

// namesOffBoard lists the names that are not on the stored tap list.
func (a *App) namesOffBoard(ctx context.Context, tenantID uuid.UUID, names []string) []string {
	row, err := GetBoard(ctx, a.pool, tenantID)
	if err != nil || row == nil {
		return nil
	}
	board, err := ParseBoard(row.Payload)
	if err != nil {
		return nil
	}
	onTap := map[string]bool{}
	for _, t := range board.Taps {
		onTap[nameKey(t.Name)] = true
	}
	var off []string
	for _, n := range names {
		if !onTap[nameKey(n)] {
			off = append(off, n)
		}
	}
	return off
}

// describeGlutenReduced is the list's part of get_menu_board.
func describeGlutenReduced(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) string {
	beers, err := LoadGlutenReduced(ctx, pool, tenantID)
	if err != nil {
		return fmt.Sprintf("\nGluten reduced: could not load (%v)\n", err)
	}
	if len(beers) == 0 {
		return "\nGluten reduced: none marked (set_menu_gluten_reduced)\n"
	}
	return "\n" + glutenReducedSummary(beers) + "\n"
}
