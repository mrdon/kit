package square

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// Catalog reads and writes, for features that keep something in Square in
// step with a setting in Kit (happy hour today).
//
// Writes need ITEMS_WRITE, which the scopes this integration was first set up
// with do not include. A token without it answers 403, which these methods
// turn into ErrMissingScope with Square's own body attached, so whoever is
// looking at the failure can see which permission to add.

// CatalogVariation is the subset of an item variation the callers need.
type CatalogVariation struct {
	ID         string
	Name       string
	PriceCents int // -1 when the variation has no fixed price
	Currency   string
}

// CatalogItem is the subset of a catalog item the callers need.
type CatalogItem struct {
	ID          string
	Name        string
	KitchenName string
	Variations  []CatalogVariation
}

type catalogObjectJSON struct {
	ID        string `json:"id"`
	IsDeleted bool   `json:"is_deleted"`
	ItemData  *struct {
		Name        string `json:"name"`
		KitchenName string `json:"kitchen_name"`
		Variations  []struct {
			ID   string `json:"id"`
			Data struct {
				Name       string `json:"name"`
				PriceMoney *struct {
					Amount   int    `json:"amount"`
					Currency string `json:"currency"`
				} `json:"price_money"`
			} `json:"item_variation_data"`
		} `json:"variations"`
	} `json:"item_data"`
}

// ListCatalogItems pulls every live item in the catalog. Needs ITEMS_READ.
func (c *Client) ListCatalogItems(ctx context.Context) ([]CatalogItem, error) {
	var out []CatalogItem
	cursor := ""
	for {
		q := url.Values{"types": {"ITEM"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var resp struct {
			Objects []catalogObjectJSON `json:"objects"`
			Cursor  string              `json:"cursor"`
		}
		if err := c.doJSON(ctx, http.MethodGet, "/v2/catalog/list?"+q.Encode(), nil, &resp); err != nil {
			return nil, scopeError(err)
		}
		for _, o := range resp.Objects {
			if o.IsDeleted || o.ItemData == nil {
				continue
			}
			item := CatalogItem{ID: o.ID, Name: o.ItemData.Name, KitchenName: o.ItemData.KitchenName}
			for _, v := range o.ItemData.Variations {
				cv := CatalogVariation{ID: v.ID, Name: v.Data.Name, PriceCents: -1}
				if v.Data.PriceMoney != nil {
					cv.PriceCents, cv.Currency = v.Data.PriceMoney.Amount, v.Data.PriceMoney.Currency
				}
				item.Variations = append(item.Variations, cv)
			}
			out = append(out, item)
		}
		if resp.Cursor == "" {
			return out, nil
		}
		cursor = resp.Cursor
	}
}

// BatchUpsertCatalog creates or updates objects in one batch, so objects can
// refer to each other by their "#temp" ids. It returns the temp id -> real id
// mapping for the objects it created. Needs ITEMS_WRITE.
func (c *Client) BatchUpsertCatalog(ctx context.Context, idempotencyKey string, objects []map[string]any) (map[string]string, error) {
	body := map[string]any{
		"idempotency_key": idempotencyKey,
		"batches":         []map[string]any{{"objects": objects}},
	}
	var resp struct {
		IDMappings []struct {
			ClientObjectID string `json:"client_object_id"`
			ObjectID       string `json:"object_id"`
		} `json:"id_mappings"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v2/catalog/batch-upsert", body, &resp); err != nil {
		return nil, scopeError(err)
	}
	out := make(map[string]string, len(resp.IDMappings))
	for _, m := range resp.IDMappings {
		out[m.ClientObjectID] = m.ObjectID
	}
	return out, nil
}

// BatchDeleteCatalog deletes objects by id and returns the ids Square reports
// deleted. An id that is already gone is not an error. Needs ITEMS_WRITE.
func (c *Client) BatchDeleteCatalog(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var resp struct {
		DeletedObjectIDs []string `json:"deleted_object_ids"`
	}
	body := map[string]any{"object_ids": ids}
	if err := c.doJSON(ctx, http.MethodPost, "/v2/catalog/batch-delete", body, &resp); err != nil {
		return nil, scopeError(err)
	}
	return resp.DeletedObjectIDs, nil
}

// scopeError marks a 403 as a missing permission, keeping Square's body.
func scopeError(err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.IsForbidden() {
		return fmt.Errorf("%w: %s", ErrMissingScope, apiErr.Body)
	}
	return err
}
