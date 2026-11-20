package menu

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/square"
	"github.com/mrdon/kit/internal/services"
)

const (
	checkSquareTool = "check_menu_square"
	applyFixTool    = "apply_menu_square_fix"
)

func squareCheckToolMetas() []services.ToolMeta {
	return []services.ToolMeta{
		{
			Name: checkSquareTool,
			Description: "Check that Square can sell everything on the menu (the same menu the wall and the " +
				"printed page show): every drink and pour as an item at the menu's price, every tap drink " +
				"with a taster in the Flight pours picker, and every crowler in the online crowler items, " +
				"in stock, with the crowlers of drinks no longer on the menu taken off the online shop. Without apply it previews and changes nothing. With apply=true it does what Kit " +
				"does every night: adds what has a right answer (copying how similar drinks are set up, at " +
				"the menu's or Square's own prices), and files a decision card for anything that needs a " +
				"person, such as a price that disagrees. It never removes or reprices without a card, except an " +
				"online crowler listing, which it deletes when its drink leaves the menu and adds back when it returns.",
			AdminOnly: true,
			Schema: services.Props(map[string]any{
				"apply": services.Field("boolean", "true to add to Square and file decision cards now; false or absent to preview."),
			}),
		},
		{
			Name: applyFixTool,
			Description: "Make one change in Square that the menu's Square check proposed on a decision card: " +
				"reprice a variation or flight option, add a size to an item, or create a menu drink as a new item. " +
				"Normally run by approving that card rather than called directly.",
			AdminOnly: true,
			Schema: services.PropsReq(map[string]any{
				"fix": map[string]any{
					"type":        "object",
					"description": "The fix, as the decision card carries it: {kind, object_id, name, cents}.",
				},
			}, "fix"),
		},
	}
}

// checkSquareFromTool is the shared body of check_menu_square.
func checkSquareFromTool(ctx context.Context, a *App, tenantID uuid.UUID, raw []byte) (string, error) {
	var args struct {
		Apply bool `json:"apply"`
	}
	if err := decode(raw, &args, checkSquareTool); err != nil {
		return "", err
	}
	run, err := a.checkSquare(ctx, tenantID, args.Apply)
	if err != nil && run.Plan.Adds == nil && run.Plan.Questions == nil && run.Done == nil {
		return "", squareToolError(err)
	}
	keys := make([]string, 0, len(run.Plan.Questions))
	for _, q := range run.Plan.Questions {
		keys = append(keys, q.Key)
	}
	asked, aerr := askedBefore(ctx, a.pool, tenantID, keys)
	if aerr != nil {
		return "", aerr
	}
	out := describeSquareRun(run, args.Apply, asked)
	if err != nil {
		out += "\n\n" + err.Error()
	}
	return out, nil
}

func squareToolError(err error) error {
	if errors.Is(err, square.ErrNotConfigured) {
		return errors.New("square is not connected for this workspace")
	}
	return err
}

// applyFixFromTool is the shared body of apply_menu_square_fix.
func applyFixFromTool(ctx context.Context, a *App, tenantID uuid.UUID, raw []byte) (string, error) {
	var args struct {
		Fix sqFix `json:"fix"`
	}
	if err := decode(raw, &args, applyFixTool); err != nil {
		return "", err
	}
	client, err := square.Instance().LoadClient(ctx, tenantID)
	if err != nil {
		return "", squareToolError(err)
	}
	f := args.Fix
	switch f.Kind {
	case "price":
		return repriceVariation(ctx, client, f)
	case "flight_price":
		return repriceFlightOption(ctx, client, f)
	case "variation":
		return addSize(ctx, client, f)
	case "item":
		return a.createMenuItem(ctx, tenantID, client, f.Name)
	default:
		return "", fmt.Errorf("%w: unknown fix kind %q", ErrPayloadInvalid, f.Kind)
	}
}

// writable is a fresh copy of an object Square sent, minus the fields it
// sets itself, ready to send back with its version.
func writable(o map[string]any) map[string]any {
	out := maps.Clone(o)
	for _, k := range []string{"updated_at", "created_at", "is_deleted"} {
		delete(out, k)
	}
	return out
}

func repriceVariation(ctx context.Context, client *square.Client, f sqFix) (string, error) {
	items, err := client.ListCatalogObjects(ctx, "ITEM")
	if err != nil {
		return "", err
	}
	for _, o := range items {
		it := parseSqItem(o)
		for _, v := range it.Vars {
			if v.ID != f.ObjectID {
				continue
			}
			obj2 := writable(v.Raw)
			data := maps.Clone(obj(obj2, "item_variation_data"))
			data["price_money"] = map[string]any{"amount": f.Cents, "currency": currencyOf(data)}
			obj2["item_variation_data"] = data
			if _, err := client.BatchUpsertCatalog(ctx, uuid.NewString(), []map[string]any{obj2}); err != nil {
				return "", fmt.Errorf("repricing %s %s: %w", it.Name, v.Name, err)
			}
			return fmt.Sprintf("Square now rings %s %s at %s.", it.Name, v.Name, dollars(f.Cents)), nil
		}
	}
	return "", fmt.Errorf("%w: no variation %s in Square any more", ErrPayloadInvalid, f.ObjectID)
}

func repriceFlightOption(ctx context.Context, client *square.Client, f sqFix) (string, error) {
	cat, err := loadSquareCatalog(ctx, client)
	if err != nil {
		return "", err
	}
	if cat.Flight != nil {
		for _, m := range cat.Flight.Mods {
			if m.ID != f.ObjectID {
				continue
			}
			o := writable(m.Raw)
			data := maps.Clone(obj(o, "modifier_data"))
			data["price_money"] = map[string]any{"amount": f.Cents, "currency": "USD"}
			o["modifier_data"] = data
			if _, err := client.BatchUpsertCatalog(ctx, uuid.NewString(), []map[string]any{o}); err != nil {
				return "", fmt.Errorf("repricing flight option %s: %w", m.Name, err)
			}
			return fmt.Sprintf("%s now adds +%s to a flight.", m.Name, dollars(f.Cents)), nil
		}
	}
	return "", fmt.Errorf("%w: no flight option %s in Square any more", ErrPayloadInvalid, f.ObjectID)
}

func addSize(ctx context.Context, client *square.Client, f sqFix) (string, error) {
	cat, err := loadSquareCatalog(ctx, client)
	if err != nil {
		return "", err
	}
	for _, it := range cat.Items {
		if it.ID != f.ObjectID {
			continue
		}
		if _, ok := it.variation(f.Name); ok {
			return fmt.Sprintf("%s already has a %s.", it.Name, f.Name), nil
		}
		v := newVariation(it, "#size", it.ID, f.Name, f.Cents)
		if _, err := client.BatchUpsertCatalog(ctx, uuid.NewString(), []map[string]any{v}); err != nil {
			return "", fmt.Errorf("adding %s to %s: %w", f.Name, it.Name, err)
		}
		return fmt.Sprintf("Added %s to %s at %s. Tonight's check lists it online if it is a crowler.",
			f.Name, it.Name, dollars(f.Cents)), nil
	}
	return "", fmt.Errorf("%w: no item %s in Square any more", ErrPayloadInvalid, f.ObjectID)
}

// createMenuItem creates a drink a near-miss question was asked about, once
// an admin has said it is a different drink, then runs the check so its
// flight option and crowler follow.
func (a *App) createMenuItem(ctx context.Context, tenantID uuid.UUID, client *square.Client, name string) (string, error) {
	rows, _, err := a.menuRows(ctx, tenantID)
	if err != nil {
		return "", err
	}
	cat, err := loadSquareCatalog(ctx, client)
	if err != nil {
		return "", err
	}
	if it, ok := cat.match(name); ok {
		return fmt.Sprintf("%s is already in Square as %s.", name, it.Name), nil
	}
	p := &planner{cat: cat, rows: rows}
	for _, b := range rows {
		if nameKey(b.Name) != nameKey(name) {
			continue
		}
		add, ok := p.createItem(b)
		if !ok {
			return "", fmt.Errorf("%w: no drink like %s in Square to copy the set up from", ErrPayloadInvalid, name)
		}
		if _, err := client.BatchUpsertCatalog(ctx, uuid.NewString(), add.Objects); err != nil {
			return "", fmt.Errorf("creating %s: %w", name, err)
		}
		run, err := a.checkSquare(ctx, tenantID, true)
		out := add.Summary + "\n\n" + describeSquareRun(run, true, nil)
		if err != nil {
			out += "\n\n" + err.Error()
		}
		return out, nil
	}
	return "", fmt.Errorf("%w: %s is not on the menu any more", ErrPayloadInvalid, name)
}
