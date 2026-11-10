package menu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

// itemsToolMeta is set_menu_items, shared by both surfaces.
func itemsToolMeta() services.ToolMeta {
	strList := func(desc string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
	}
	return services.ToolMeta{
		Name: "set_menu_items",
		Description: "Edit the menu Untappd syncs in: add items it does not carry, and hide beers " +
			"or whole sections. The wall and the printed menu both read the result. `items` " +
			"replaces Kit's own items (get_menu_board lists them as this tool takes them, so " +
			"include the ones to keep); each is {section, name, style, abv, pours: [{size, " +
			"label, price}], show}, where label is like \"12oz Can\" and show is \"both\" (the " +
			"default), \"board\" for the wall only or \"print\" for paper only. A section matching " +
			"one on the tap list joins it. `hide` and `unhide` take beer or section names; hiding " +
			"lasts until unhidden, through any sync. For a beer that has run out today use " +
			"set_menu_86 instead. Call with no arguments to see the current edits.",
		AdminOnly: true,
		Schema: services.Props(map[string]any{
			"items": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "object"},
				"description": "Every Kit item, in menu order. Omit to leave them as they are.",
			},
			"hide":   strList("Beer or section names to keep off both menus."),
			"unhide": strList("Hidden beer or section names to bring back."),
		}),
	}
}

type itemsArgs struct {
	Items  []Beer
	Set    bool // items was given, even as an empty list
	Hide   []string
	Unhide []string
}

// setMenuItems is the handler both surfaces call, with the raw arguments.
func setMenuItems(ctx context.Context, pool *pgxpool.Pool, a *App, tenantID uuid.UUID, raw []byte) (string, error) {
	args, err := parseItemsArgs(raw)
	if err != nil {
		return "", err
	}
	edits, err := editItems(ctx, pool, a, tenantID, args)
	if err != nil {
		return "", err
	}
	return describeEdits(edits), nil
}

// editItems applies one set of changes and stores the result. The console's
// PUT takes the same arguments, so the two surfaces cannot drift.
func editItems(ctx context.Context, pool *pgxpool.Pool, a *App, tenantID uuid.UUID, args itemsArgs) (Edits, error) {
	edits, err := LoadEdits(ctx, pool, tenantID)
	if err != nil {
		return Edits{}, err
	}
	changed := args.Set || len(args.Hide)+len(args.Unhide) > 0
	if args.Set {
		if err := validateItems(args.Items); err != nil {
			return Edits{}, err
		}
		edits.Items = args.Items
	}
	if len(args.Hide)+len(args.Unhide) > 0 {
		if err := a.applyHide(ctx, tenantID, &edits, args.Hide, args.Unhide); err != nil {
			return Edits{}, err
		}
	}
	if changed {
		if err := SaveEdits(ctx, pool, tenantID, edits); err != nil {
			return Edits{}, err
		}
	}
	return edits.normalized(), nil
}

// applyHide moves names onto and off the hidden lists. A name that is a
// section heading hides the section; anything else must match one beer on
// the tap list or one Kit item, partially if only one fits.
func (a *App) applyHide(ctx context.Context, tenantID uuid.UUID, e *Edits, hide, unhide []string) error {
	taps, err := a.tapNames(ctx, tenantID)
	if err != nil {
		return err
	}
	sections := a.menuSections(ctx, tenantID, e.Items)
	for _, n := range hide {
		if s, ok := matchSection(n, sections); ok {
			e.Hidden.Sections = addName(e.Hidden.Sections, s)
			continue
		}
		hit, err := resolveAll([]string{n}, taps, "beer or section on the menu")
		if err != nil {
			return err
		}
		e.Hidden.Items = addName(e.Hidden.Items, hit[0])
	}
	for _, n := range unhide {
		before := len(e.Hidden.Items) + len(e.Hidden.Sections)
		e.Hidden.Items = dropName(e.Hidden.Items, n)
		e.Hidden.Sections = dropSection(e.Hidden.Sections, n)
		if len(e.Hidden.Items)+len(e.Hidden.Sections) == before {
			return fmt.Errorf("%w: %q is not hidden. Hidden: %s", ErrPayloadInvalid, n,
				strings.Join(append(append([]string{}, e.Hidden.Sections...), e.Hidden.Items...), ", "))
		}
	}
	return nil
}

// menuSections is every heading on the tap list or among the items.
func (a *App) menuSections(ctx context.Context, tenantID uuid.UUID, items []Beer) []string {
	var out []string
	if row, err := GetBoard(ctx, a.pool, tenantID); err == nil && row != nil {
		if board, perr := ParseBoard(row.Payload); perr == nil {
			for _, t := range board.Taps {
				out = addSection(out, t.Section)
			}
		}
	}
	for _, e := range items {
		out = addSection(out, e.Section)
	}
	return out
}

func matchSection(s string, sections []string) (string, bool) {
	for _, c := range sections {
		if sectionKey(c) == sectionKey(s) {
			return c, true
		}
	}
	return "", false
}

func addName(list []string, name string) []string {
	if containsName(list, name) {
		return list
	}
	return append(list, name)
}

func addSection(list []string, name string) []string {
	if _, ok := matchSection(name, list); ok || strings.TrimSpace(name) == "" {
		return list
	}
	return append(list, name)
}

func dropName(list []string, name string) []string {
	out := list[:0:0]
	for _, n := range list {
		if nameKey(n) != nameKey(name) {
			out = append(out, n)
		}
	}
	return out
}

func dropSection(list []string, name string) []string {
	out := list[:0:0]
	for _, n := range list {
		if sectionKey(n) != sectionKey(name) {
			out = append(out, n)
		}
	}
	return out
}

// parseItemsArgs accepts each field as the value itself or as a JSON string,
// since clients differ on how a structured argument travels. Unknown item
// fields fail, so a typo'd "shows" is reported rather than silently ignored.
func parseItemsArgs(raw []byte) (itemsArgs, error) {
	var args itemsArgs
	if len(bytes.TrimSpace(raw)) == 0 {
		return args, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return args, fmt.Errorf("%w: parsing set_menu_items arguments: %w", ErrPayloadInvalid, err)
	}
	if v, ok := m["items"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil {
			v = json.RawMessage(s)
		}
		dec := json.NewDecoder(bytes.NewReader(v))
		dec.DisallowUnknownFields()
		args.Items = []Beer{}
		if err := dec.Decode(&args.Items); err != nil {
			return args, fmt.Errorf("%w: parsing items: %w", ErrPayloadInvalid, err)
		}
		args.Set = true
	}
	for key, dst := range map[string]*[]string{"hide": &args.Hide, "unhide": &args.Unhide} {
		v, ok := m[key]
		if !ok {
			continue
		}
		list, err := stringList(v)
		if err != nil {
			return args, fmt.Errorf("%w: %s: %w", ErrPayloadInvalid, key, err)
		}
		*dst = list
	}
	return args, nil
}

// describeEdits lists the edits the way set_menu_items takes them, so a
// change starts from what is there rather than from a count.
func describeEdits(e Edits) string {
	var b strings.Builder
	b.WriteString("\nMenu edits (set_menu_items):\n")
	if len(e.Items) == 0 {
		b.WriteString("  no Kit items\n")
	} else {
		pretty, err := json.MarshalIndent(e.Items, "  ", "  ")
		if err != nil {
			fmt.Fprintf(&b, "  (could not render items: %v)\n", err)
		} else {
			fmt.Fprintf(&b, "  %d Kit items, %d on paper, %d on the wall:\n  %s\n",
				len(e.Items), len(printItems(e.Items)), len(boardItems(e.Items)), pretty)
		}
	}
	if len(e.Hidden.Sections) > 0 {
		fmt.Fprintf(&b, "  hidden sections: %s\n", strings.Join(e.Hidden.Sections, ", "))
	}
	if len(e.Hidden.Items) > 0 {
		fmt.Fprintf(&b, "  hidden beers: %s\n", strings.Join(e.Hidden.Items, ", "))
	}
	return b.String()
}

// missingDescriptions lists the beers the printed menu will show without a
// description. Kit items are left out: a can of soda needs no paragraph.
// Kit no longer fetches descriptions, so this is the warning a person sees
// before printing, and the work list for set_menu_notes.
func missingDescriptions(state *PrintState, ov Overrides) []string {
	notes := mergedNotes(state.Notes, state.Config.Notes)
	var out []string
	for _, r := range state.Rows {
		if ov.gone(r.Section, r.Name) {
			continue
		}
		if _, ok := findNote(notes, r.Name); !ok {
			out = append(out, r.Name)
		}
	}
	return out
}
