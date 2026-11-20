package menu

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/square"
)

// An online crowler is filled from the tap on pickup day, so it can be sold
// only while its drink is on the menu. The listings stay in the catalog
// either way: a drink that comes off is counted to 0, which is how Square
// shows it sold out, and counted back in when it returns. That is a count,
// not a change to what Square sells, so it needs no card, and a drink 86'd
// in Kit comes off the online shop with it.

// sqCount sets the stock of one online crowler listing.
type sqCount struct {
	Summary     string
	VariationID string
	Quantity    string
}

// soldOut is the count that shows a listing sold out online.
const soldOut = "0"

// crowlerStock counts each crowler listing in or out to match the menu.
// onMenu holds the listings of every tap drink the menu shows.
func (p *planner) crowlerStock(onMenu map[string]bool) {
	if p.cat.Counts == nil {
		return
	}
	if len(onMenu) == 0 {
		// A menu with no crowlers on it is a sync gone wrong far more often
		// than a brewery with nothing on tap.
		p.plan.Notes = append(p.plan.Notes, "no crowler on the menu matches Square, so none were marked sold out")
		return
	}
	for _, name := range []string{beerCrowlerItem, sodaCrowlerItem} {
		list, ok := p.cat.item(name)
		if !ok {
			continue
		}
		for _, v := range list.Vars {
			if tracked, _ := obj(v.Raw, "item_variation_data")["track_inventory"].(bool); !tracked {
				continue
			}
			n, _ := strconv.ParseFloat(p.cat.Counts[v.ID], 64)
			switch {
			case onMenu[v.ID] && n <= 0:
				p.count(v, inStock, fmt.Sprintf("%s: back on the menu, so its %s is on sale online again", v.Name, list.Name))
			case !onMenu[v.ID] && n > 0:
				p.count(v, soldOut, fmt.Sprintf("%s: off the menu, so its %s shows sold out online", v.Name, list.Name))
			}
		}
	}
}

func (p *planner) count(v sqVar, quantity, summary string) {
	p.plan.Counts = append(p.plan.Counts, sqCount{Summary: summary, VariationID: v.ID, Quantity: quantity})
}

// stocker sets online crowler counts at the one location they sell from.
type stocker struct {
	client   *square.Client
	location string
}

func (s *stocker) where(ctx context.Context) (string, error) {
	if s.location != "" {
		return s.location, nil
	}
	locs, err := s.client.ListLocations(ctx)
	if err != nil {
		return "", fmt.Errorf("listing locations: %w", err)
	}
	var active []string
	for _, l := range locs {
		if l.Status == "" || l.Status == "ACTIVE" {
			active = append(active, l.ID)
		}
	}
	if len(active) != 1 {
		return "", fmt.Errorf("%d active locations, so which one to count it in at is a choice", len(active))
	}
	s.location = active[0]
	return s.location, nil
}

func (s *stocker) set(ctx context.Context, variationID, quantity string) error {
	if variationID == "" {
		return errors.New("square did not return the new variation's id")
	}
	loc, err := s.where(ctx)
	if err != nil {
		return err
	}
	at := time.Now().UTC().Format(time.RFC3339)
	return s.client.SetInventoryCount(ctx, uuid.NewString(), loc, variationID, quantity, at)
}

// counts reads the stock of every online crowler listing. A variation
// Square has never counted is absent, which reads as 0.
func (s *stocker) counts(ctx context.Context, cat sqCatalog) (map[string]string, error) {
	var ids []string
	for _, name := range []string{beerCrowlerItem, sodaCrowlerItem} {
		if list, ok := cat.item(name); ok {
			for _, v := range list.Vars {
				ids = append(ids, v.ID)
			}
		}
	}
	loc, err := s.where(ctx)
	if err != nil {
		return nil, err
	}
	return s.client.InventoryCounts(ctx, loc, ids)
}

// applyCounts sets each count the last look planned, one at a time so one
// refusal does not hold up the rest.
func applyCounts(ctx context.Context, st *stocker, counts []sqCount) (done, failed []string) {
	for _, c := range counts {
		if err := st.set(ctx, c.VariationID, c.Quantity); err != nil {
			failed = append(failed, c.Summary+": "+err.Error())
			continue
		}
		done = append(done, c.Summary)
	}
	return done, failed
}
