package square

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// The online shop, as the catalog describes it.
//
// Square Online groups its storefront under one top-level menu category
// ("Online Shop") whose children are the shop's sections: Crowlers, Bottles
// & Cans, Gear, Gift Cards, Kegs. That tree is what staff already maintain,
// so it is what the website renders -- no second list of products to keep in
// step. Channel ids would also identify the online items, but they are opaque
// per-seller strings; the category is named, visible in the Dashboard, and
// survives a channel being rebuilt.

// ShopRootCategory is the menu category whose children are the shop sections.
const ShopRootCategory = "Online Shop"

// ShopVariation is one size or flavour of a shop item.
type ShopVariation struct {
	ID         string
	Name       string
	PriceCents int // -1 when the variation has no fixed price
	Currency   string
	SoldOut    bool
	Sale       *ShopSale // nil when no pricing rule lowers the price
	Pour       *ShopPour // the bar's pour of the beer this is named after
}

// ShopItem is one product card.
type ShopItem struct {
	ID          string
	Name        string
	Description string
	ImageURL    string
	Variations  []ShopVariation
}

// ShopSection is one sub-category of the shop, in Square's order.
type ShopSection struct {
	ID    string
	Name  string
	Items []ShopItem
}

// shopObject is the subset of a catalog object the shop reads.
type shopObject struct {
	Type            string           `json:"type"`
	ID              string           `json:"id"`
	IsDeleted       bool             `json:"is_deleted"`
	DiscountData    *shopDiscount    `json:"discount_data"`
	PricingRuleData *shopPricingRule `json:"pricing_rule_data"`
	ProductSetData  *shopProductSet  `json:"product_set_data"`
	ImageData       *struct {
		URL string `json:"url"`
	} `json:"image_data"`
	CategoryData *struct {
		Name           string `json:"name"`
		ParentCategory *struct {
			ID      string `json:"id"`
			Ordinal int64  `json:"ordinal"`
		} `json:"parent_category"`
	} `json:"category_data"`
	ItemData *struct {
		Name        string   `json:"name"`
		Description string   `json:"description_plaintext"`
		IsArchived  bool     `json:"is_archived"`
		ImageIDs    []string `json:"image_ids"`
		Categories  []struct {
			ID      string `json:"id"`
			Ordinal int64  `json:"ordinal"`
		} `json:"categories"`
		Variations []struct {
			ID        string `json:"id"`
			IsDeleted bool   `json:"is_deleted"`
			Data      struct {
				Name       string `json:"name"`
				Ordinal    int64  `json:"ordinal"`
				Sellable   *bool  `json:"sellable"`
				PriceMoney *struct {
					Amount   int    `json:"amount"`
					Currency string `json:"currency"`
				} `json:"price_money"`
				LocationOverrides []struct {
					SoldOut bool `json:"sold_out"`
				} `json:"location_overrides"`
			} `json:"item_variation_data"`
		} `json:"variations"`
	} `json:"item_data"`
}

// ShopCatalog reads the catalog and returns the shop's sections, with sale
// prices from pricing rules that have not ended by now. loc is the seller's
// zone, which Square's rule dates are in. Needs ITEMS_READ. An account with no
// "Online Shop" category returns no sections rather than an error: an empty
// shop is a valid state of the catalog.
func (c *Client) ShopCatalog(ctx context.Context, loc *time.Location, now time.Time) ([]ShopSection, error) {
	var objects []shopObject
	cursor := ""
	for {
		q := url.Values{"types": {"ITEM,IMAGE,CATEGORY,DISCOUNT,PRICING_RULE,PRODUCT_SET"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var resp struct {
			Objects []shopObject `json:"objects"`
			Cursor  string       `json:"cursor"`
		}
		if err := c.doJSON(ctx, http.MethodGet, "/v2/catalog/list?"+q.Encode(), nil, &resp); err != nil {
			return nil, fmt.Errorf("listing catalog for the shop: %w", scopeError(err))
		}
		objects = append(objects, resp.Objects...)
		if resp.Cursor == "" {
			return buildShop(objects, loc, now), nil
		}
		cursor = resp.Cursor
	}
}

// buildShop groups items into the shop's sections. Pure, so the grouping and
// ordering rules are testable without Square.
func buildShop(objects []shopObject, loc *time.Location, now time.Time) []ShopSection {
	images, sections, order := shopIndex(objects)
	if len(sections) == 0 {
		return nil
	}
	rules := shopRules(objects, loc, now)
	pours := shopPours(objects)
	type placed struct {
		ordinal int64
		item    ShopItem
	}
	bySection := map[string][]placed{}
	for i := range objects {
		o := &objects[i]
		if o.Type != "ITEM" || o.IsDeleted || o.ItemData == nil || o.ItemData.IsArchived {
			continue
		}
		sectionID, ordinal, ok := shopSectionOf(o, sections)
		if !ok {
			continue
		}
		item, ok := shopItem(o, images, rules, pours)
		if !ok {
			continue
		}
		bySection[sectionID] = append(bySection[sectionID], placed{ordinal, item})
	}
	var out []ShopSection
	for _, id := range order {
		items := bySection[id]
		if len(items) == 0 {
			continue
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].ordinal < items[j].ordinal })
		sec := ShopSection{ID: id, Name: sections[id]}
		for _, p := range items {
			sec.Items = append(sec.Items, p.item)
		}
		out = append(out, sec)
	}
	return out
}

// shopIndex returns image URLs by id, the shop's section names by id, and the
// section ids in Square's order.
func shopIndex(objects []shopObject) (map[string]string, map[string]string, []string) {
	images := map[string]string{}
	rootID := ""
	for i := range objects {
		o := &objects[i]
		if o.IsDeleted {
			continue
		}
		if o.Type == "IMAGE" && o.ImageData != nil {
			images[o.ID] = o.ImageData.URL
		}
		if o.Type == "CATEGORY" && o.CategoryData != nil &&
			strings.EqualFold(strings.TrimSpace(o.CategoryData.Name), ShopRootCategory) {
			rootID = o.ID
		}
	}
	sections := map[string]string{}
	ordinals := map[string]int64{}
	var order []string
	if rootID == "" {
		return images, sections, order
	}
	for i := range objects {
		o := &objects[i]
		if o.Type != "CATEGORY" || o.IsDeleted || o.CategoryData == nil {
			continue
		}
		p := o.CategoryData.ParentCategory
		if p == nil || p.ID != rootID {
			continue
		}
		sections[o.ID] = o.CategoryData.Name
		ordinals[o.ID] = p.Ordinal
		order = append(order, o.ID)
	}
	sort.SliceStable(order, func(i, j int) bool { return ordinals[order[i]] < ordinals[order[j]] })
	return images, sections, order
}

// shopSectionOf finds the first shop section an item is filed under, and the
// item's position within it.
func shopSectionOf(o *shopObject, sections map[string]string) (string, int64, bool) {
	for _, c := range o.ItemData.Categories {
		if _, ok := sections[c.ID]; ok {
			return c.ID, c.Ordinal, true
		}
	}
	return "", 0, false
}

// shopItem projects a catalog item onto a shop card. An item with no sellable
// variation is dropped: a card with nothing to buy is noise.
func shopItem(o *shopObject, images map[string]string, rules []shopRule, pours map[string]ShopPour) (ShopItem, bool) {
	d := o.ItemData
	item := ShopItem{ID: o.ID, Name: d.Name, Description: strings.TrimSpace(d.Description)}
	for _, id := range d.ImageIDs {
		if u := images[id]; u != "" {
			item.ImageURL = u
			break
		}
	}
	vars := d.Variations
	sort.SliceStable(vars, func(i, j int) bool { return vars[i].Data.Ordinal < vars[j].Data.Ordinal })
	for _, v := range vars {
		if v.IsDeleted || (v.Data.Sellable != nil && !*v.Data.Sellable) {
			continue
		}
		sv := ShopVariation{ID: v.ID, Name: v.Data.Name, PriceCents: -1}
		if v.Data.PriceMoney != nil {
			sv.PriceCents, sv.Currency = v.Data.PriceMoney.Amount, v.Data.PriceMoney.Currency
		}
		ids := []string{v.ID, o.ID}
		for _, c := range d.Categories {
			ids = append(ids, c.ID)
		}
		sv.Sale = bestSale(rules, sv.PriceCents, ids...)
		if p, ok := pours[strings.ToLower(strings.TrimSpace(v.Data.Name))]; ok {
			sv.Pour = &p
		}
		for _, lo := range v.Data.LocationOverrides {
			if lo.SoldOut {
				sv.SoldOut = true
			}
		}
		item.Variations = append(item.Variations, sv)
	}
	return item, len(item.Variations) > 0
}
