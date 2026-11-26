package square

import (
	"encoding/json"
	"testing"
	"time"
)

// Rules as Square sends them: a capped percentage (Black Friday crowlers), a
// coupon that must not show (ATTACHED), and one that has already ended.
const salesFixture = `[
 {"type":"DISCOUNT","id":"HALF","discount_data":{"name":"Half","discount_type":"FIXED_PERCENTAGE","percentage":"50.0","maximum_amount_money":{"amount":1000}}},
 {"type":"DISCOUNT","id":"FIVE","discount_data":{"name":"Five","discount_type":"FIXED_AMOUNT","amount_money":{"amount":500}}},
 {"type":"PRODUCT_SET","id":"CROW","product_set_data":{"product_ids_any":["ITEM"]}},
 {"type":"PRODUCT_SET","id":"ALL","product_set_data":{"all_products":true}},
 {"type":"PRICING_RULE","id":"R1","pricing_rule_data":{"name":"Black Friday Crowlers","discount_id":"HALF","match_products_id":"CROW","application_mode":"AUTOMATIC",
   "valid_from_date":"2026-11-27","valid_from_local_time":"00:00:00","valid_until_date":"2026-12-01","valid_until_local_time":"00:00:00"}},
 {"type":"PRICING_RULE","id":"R2","pricing_rule_data":{"name":"Coupon","discount_id":"FIVE","match_products_id":"ALL","application_mode":"ATTACHED"}},
 {"type":"PRICING_RULE","id":"R3","pricing_rule_data":{"name":"Old","discount_id":"FIVE","match_products_id":"ALL","application_mode":"AUTOMATIC",
   "valid_until_date":"2026-11-01"}}
]`

func TestShopSalePrices(t *testing.T) {
	var objs []shopObject
	if err := json.Unmarshal([]byte(salesFixture), &objs); err != nil {
		t.Fatal(err)
	}
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, denver)
	rules := shopRules(objs, denver, now)
	if len(rules) != 1 {
		t.Fatalf("rules = %d, want only the automatic, unexpired one", len(rules))
	}

	s := bestSale(rules, 1300, "VAR", "ITEM")
	if s == nil || s.PriceCents != 650 || s.Name != "Black Friday Crowlers" {
		t.Fatalf("sale = %+v, want $6.50", s)
	}
	if want := time.Date(2026, 11, 27, 0, 0, 0, 0, denver); !s.StartsAt.Equal(want) {
		t.Errorf("starts = %v, want %v (seller-local midnight)", s.StartsAt, want)
	}
	if capped := bestSale(rules, 2600, "ITEM"); capped == nil || capped.PriceCents != 1600 {
		t.Errorf("capped sale = %+v, want $26 - $10 cap = $16", capped)
	}
	if other := bestSale(rules, 1300, "SOMETHING-ELSE"); other != nil {
		t.Errorf("unmatched item got a sale: %+v", other)
	}
}

// A keg named after a beer picks up the beer's largest real glass: 12oz for
// Acceleration here, ignoring the taster, the crowler and the pitcher.
func TestShopPours(t *testing.T) {
	const fixture = `[
	 {"type":"ITEM","id":"ACC","item_data":{"name":"Acceleration","variations":[
	   {"id":"a1","item_variation_data":{"name":"12oz","price_money":{"amount":800}}},
	   {"id":"a2","item_variation_data":{"name":"9oz","price_money":{"amount":650}}},
	   {"id":"a3","item_variation_data":{"name":"4oz","price_money":{"amount":400}}},
	   {"id":"a4","item_variation_data":{"name":"32oz Crowler","price_money":{"amount":1500}}},
	   {"id":"a5","item_variation_data":{"name":"Pitcher","price_money":{"amount":2400}}}]}}]`
	var objs []shopObject
	if err := json.Unmarshal([]byte(fixture), &objs); err != nil {
		t.Fatal(err)
	}
	got, ok := shopPours(objs)["acceleration"]
	if !ok || got.Ounces != 12 || got.PriceCents != 800 {
		t.Fatalf("pour = %+v, want 12oz at $8", got)
	}
}
