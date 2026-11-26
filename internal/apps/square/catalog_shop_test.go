package square

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A trimmed copy of the shape Square returns, covering every rule buildShop
// applies: sections come from the root's children in Square's order, items
// outside the shop are ignored, archived items and unsellable variations are
// dropped, and an image id resolves to its URL.
const shopFixture = `[
 {"type":"CATEGORY","id":"ROOT","category_data":{"name":"Online Shop"}},
 {"type":"CATEGORY","id":"GIFT","category_data":{"name":"Gift Cards","parent_category":{"id":"ROOT","ordinal":30}}},
 {"type":"CATEGORY","id":"CROW","category_data":{"name":"Crowlers","parent_category":{"id":"ROOT","ordinal":10}}},
 {"type":"CATEGORY","id":"KEGS","category_data":{"name":"Kegs","parent_category":{"id":"ROOT","ordinal":40}}},
 {"type":"CATEGORY","id":"BAR","category_data":{"name":"Bar"}},
 {"type":"IMAGE","id":"IMG1","image_data":{"url":"https://img.example/crowler.jpg"}},
 {"type":"ITEM","id":"CROWLER","item_data":{"name":"Crowler To Go","description_plaintext":" Fresh from the tap. ",
   "image_ids":["IMG1"],"categories":[{"id":"BAR","ordinal":1},{"id":"CROW","ordinal":5}],
   "variations":[
     {"id":"V2","item_variation_data":{"name":"Tsar Bomba","ordinal":2,"price_money":{"amount":1900,"currency":"USD"},"location_overrides":[{"sold_out":true}]}},
     {"id":"V1","item_variation_data":{"name":"Golden Mosaic","ordinal":1,"price_money":{"amount":1000,"currency":"USD"}}},
     {"id":"V3","item_variation_data":{"name":"Hidden","ordinal":3,"sellable":false,"price_money":{"amount":1,"currency":"USD"}}}]}},
 {"type":"ITEM","id":"GC100","item_data":{"name":"$100 Gift Card","categories":[{"id":"GIFT","ordinal":2}],
   "variations":[{"id":"G1","item_variation_data":{"name":"Regular","price_money":{"amount":10000,"currency":"USD"}}}]}},
 {"type":"ITEM","id":"GC50","item_data":{"name":"$50 Gift Card","categories":[{"id":"GIFT","ordinal":1}],
   "variations":[{"id":"G2","item_variation_data":{"name":"Regular","price_money":{"amount":5000,"currency":"USD"}}}]}},
 {"type":"ITEM","id":"OLD","item_data":{"name":"Archived Keg","is_archived":true,"categories":[{"id":"KEGS","ordinal":1}],
   "variations":[{"id":"K1","item_variation_data":{"name":"Half","price_money":{"amount":28500,"currency":"USD"}}}]}},
 {"type":"ITEM","id":"PINT","item_data":{"name":"Golden Mosaic","categories":[{"id":"BAR","ordinal":1}],
   "variations":[{"id":"P1","item_variation_data":{"name":"16oz","price_money":{"amount":700,"currency":"USD"}}}]}}
]`

func shopFixtureObjects(t *testing.T) []shopObject {
	t.Helper()
	var objs []shopObject
	if err := json.Unmarshal([]byte(shopFixture), &objs); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return objs
}

func TestBuildShopGroupsAndOrders(t *testing.T) {
	got := buildShop(shopFixtureObjects(t))

	// Kegs is dropped: its only item is archived. Bar is not a shop section.
	if len(got) != 2 || got[0].Name != "Crowlers" || got[1].Name != "Gift Cards" {
		t.Fatalf("sections = %+v, want [Crowlers, Gift Cards]", got)
	}
	gifts := got[1].Items
	if len(gifts) != 2 || gifts[0].Name != "$50 Gift Card" || gifts[1].Name != "$100 Gift Card" {
		t.Fatalf("gift cards out of Square's order: %+v", gifts)
	}

	crowler := got[0].Items[0]
	if crowler.ImageURL != "https://img.example/crowler.jpg" {
		t.Errorf("image = %q", crowler.ImageURL)
	}
	if crowler.Description != "Fresh from the tap." {
		t.Errorf("description = %q, want trimmed", crowler.Description)
	}
	if len(crowler.Variations) != 2 {
		t.Fatalf("variations = %+v, want the two sellable ones", crowler.Variations)
	}
	first, second := crowler.Variations[0], crowler.Variations[1]
	if first.Name != "Golden Mosaic" || first.PriceCents != 1000 || first.SoldOut {
		t.Errorf("first variation = %+v", first)
	}
	if second.Name != "Tsar Bomba" || !second.SoldOut {
		t.Errorf("second variation = %+v, want sold out", second)
	}
}

func TestBuildShopWithoutRootIsEmpty(t *testing.T) {
	objs := shopFixtureObjects(t)
	for i := range objs {
		if objs[i].ID == "ROOT" {
			objs[i].CategoryData.Name = "Renamed"
		}
	}
	if got := buildShop(objs); len(got) != 0 {
		t.Fatalf("sections = %+v, want none when no Online Shop category exists", got)
	}
}

func TestShopCatalogFollowsCursor(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var objs []shopObject
		_ = json.Unmarshal([]byte(shopFixture), &objs)
		half := len(objs) / 2
		page := map[string]any{"objects": objs[:half], "cursor": "next"}
		if r.URL.Query().Get("cursor") == "next" {
			page = map[string]any{"objects": objs[half:]}
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer srv.Close()

	c := &Client{apiBase: srv.URL, accessToken: "tok"}
	got, err := c.ShopCatalog(t.Context())
	if err != nil {
		t.Fatalf("ShopCatalog: %v", err)
	}
	if calls != 2 || len(got) != 2 {
		t.Fatalf("calls = %d, sections = %d; want both pages read and grouped", calls, len(got))
	}
}
