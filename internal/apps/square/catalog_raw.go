package square

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Whole catalog objects and inventory counts, for features that add to the
// catalog by copying what is already there (the menu's Square check).
//
// CatalogItem keeps only the fields happy hour reads. Copying an item's set
// up -- its categories, tax, channels -- needs every field, and so does
// writing an object back without losing the ones a struct did not name: a
// catalog upsert replaces the object whole.

// ListCatalogObjects pulls every live object of the given types (e.g.
// "ITEM", "MODIFIER_LIST") as Square sends them. Needs ITEMS_READ.
func (c *Client) ListCatalogObjects(ctx context.Context, types string) ([]map[string]any, error) {
	var out []map[string]any
	cursor := ""
	for {
		q := url.Values{"types": {types}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var resp struct {
			Objects []map[string]any `json:"objects"`
			Cursor  string           `json:"cursor"`
		}
		if err := c.doJSON(ctx, http.MethodGet, "/v2/catalog/list?"+q.Encode(), nil, &resp); err != nil {
			return nil, scopeError(err)
		}
		for _, o := range resp.Objects {
			if deleted, _ := o["is_deleted"].(bool); !deleted {
				out = append(out, o)
			}
		}
		if resp.Cursor == "" {
			return out, nil
		}
		cursor = resp.Cursor
	}
}

// InventoryCounts returns the IN_STOCK count of each variation at one
// location. A variation Square has never counted is absent. Needs
// INVENTORY_READ.
func (c *Client) InventoryCounts(ctx context.Context, locationID string, variationIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(variationIDs) == 0 {
		return out, nil
	}
	body := map[string]any{
		"catalog_object_ids": variationIDs,
		"location_ids":       []string{locationID},
		"states":             []string{"IN_STOCK"},
	}
	for {
		var resp struct {
			Counts []struct {
				CatalogObjectID string `json:"catalog_object_id"`
				Quantity        string `json:"quantity"`
			} `json:"counts"`
			Cursor string `json:"cursor"`
		}
		if err := c.doJSON(ctx, http.MethodPost, "/v2/inventory/counts/batch-retrieve", body, &resp); err != nil {
			return nil, scopeError(err)
		}
		for _, n := range resp.Counts {
			out[n.CatalogObjectID] = n.Quantity
		}
		if resp.Cursor == "" {
			return out, nil
		}
		body["cursor"] = resp.Cursor
	}
}

// SetInventoryCount records a physical count, which is how a stock-tracked
// variation is marked available: Square ignores a sold_out flag written
// through the API. Needs INVENTORY_WRITE.
func (c *Client) SetInventoryCount(ctx context.Context, idempotencyKey, locationID, variationID, quantity, occurredAt string) error {
	body := map[string]any{
		"idempotency_key": idempotencyKey,
		"changes": []map[string]any{{
			"type": "PHYSICAL_COUNT",
			"physical_count": map[string]any{
				"catalog_object_id": variationID,
				"location_id":       locationID,
				"state":             "IN_STOCK",
				"quantity":          quantity,
				"occurred_at":       occurredAt,
			},
		}},
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v2/inventory/changes/batch-create", body, nil); err != nil {
		return fmt.Errorf("setting stock of %s: %w", variationID, scopeError(err))
	}
	return nil
}
