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

// A to-go listing is sold only while its drink is on the menu (see
// square_check_togo.go). Its stock count is how it shows sold out, at the
// bar and online, and deleting it is the only way to take it off the shop:
// Square cannot hide one variation from a channel through the API. A
// listing on the menu but counted out, by hand or by an older run, is
// counted back in.

// sqCount changes one to-go listing (sets its stock to Quantity), or
// deletes a crowler or growler listing or a flight option when Remove is set.
// ObjectID is the variation or modifier it changes.
type sqCount struct {
	Summary  string
	ObjectID string
	Quantity string
	Remove   bool
}

// soldOut is the count that shows a listing sold out online. Only an item's
// last listing is left this way: Square will not keep an item with no
// variations.
const soldOut = "0"

// restock counts back in each listing on tap that is counted out.
func (p *planner) restock(list sqItem, keep map[string]bool) {
	if p.cat.Counts == nil {
		return
	}
	for _, v := range list.Vars {
		if n, _ := strconv.ParseFloat(p.cat.Counts[v.ID], 64); keep[v.ID] && n <= 0 {
			p.count(sqCount{ObjectID: v.ID, Quantity: inStock,
				Summary: fmt.Sprintf("%s: on the menu, so its %s is on sale online again", v.Name, list.Name)})
		}
	}
}

// counted reports a listing whose stock was read and is above zero.
func (p *planner) counted(id string) bool {
	n, _ := strconv.ParseFloat(p.cat.Counts[id], 64)
	return p.cat.Counts != nil && n > 0
}

// countOutLast is the last deletable listing's fate: it stays, shown sold
// out online.
func (p *planner) countOutLast(listName string) func(sqEntry) {
	return func(e sqEntry) {
		if p.counted(e.ID) {
			p.count(sqCount{ObjectID: e.ID, Quantity: soldOut,
				Summary: fmt.Sprintf("%s: off the menu, so its %s shows sold out online (the last one, so kept)", e.Name, listName)})
		}
	}
}

func (p *planner) count(c sqCount) {
	p.plan.Counts = append(p.plan.Counts, c)
}

// stocker sets to-go listing counts at the one location they sell from.
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

// counts reads the stock of every to-go listing. A variation Square has
// never counted is absent, which reads as 0.
func (s *stocker) counts(ctx context.Context, cat sqCatalog) (map[string]string, error) {
	var ids []string
	for _, g := range toGos {
		if list, ok := cat.item(g.Item); ok {
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

// applyCounts makes each listing change the last look planned, one at a
// time so one refusal does not hold up the rest.
func applyCounts(ctx context.Context, st *stocker, counts []sqCount) (done, failed []string) {
	for _, c := range counts {
		var err error
		if c.Remove {
			_, err = st.client.BatchDeleteCatalog(ctx, []string{c.ObjectID})
		} else {
			err = st.set(ctx, c.ObjectID, c.Quantity)
		}
		if err != nil {
			failed = append(failed, c.Summary+": "+err.Error())
			continue
		}
		done = append(done, c.Summary)
	}
	return done, failed
}
