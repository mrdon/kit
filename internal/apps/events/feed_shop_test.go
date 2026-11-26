package events

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrdon/kit/internal/apps/square"
)

// The site build iterates sections and items unconditionally, so an empty
// catalog must still serialise as arrays, never null.
func TestShopFeedEmptyIsArrays(t *testing.T) {
	b, err := json.Marshal(shopFeed(nil, time.Unix(0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"sections":[]`) {
		t.Fatalf("empty feed = %s, want sections as []", b)
	}
}

// A variable-priced variation has no price to show; the contract omits the
// field rather than publishing Square's -1 sentinel.
func TestShopFeedOmitsUnknownPrice(t *testing.T) {
	sections := []square.ShopSection{{ID: "S", Name: "Gear", Items: []square.ShopItem{{
		ID: "I", Name: "Shirt", Variations: []square.ShopVariation{
			{ID: "A", Name: "Any", PriceCents: -1},
			{ID: "B", Name: "Large", PriceCents: 2500, Currency: "USD", SoldOut: true},
		},
	}}}}
	feed := shopFeed(sections, time.Unix(0, 0))
	vars := feed.Sections[0].Items[0].Variations
	if vars[0].PriceCents != nil || vars[0].Currency != "" {
		t.Errorf("variable price = %+v, want no price", vars[0])
	}
	if vars[1].PriceCents == nil || *vars[1].PriceCents != 2500 || !vars[1].SoldOut {
		t.Errorf("fixed price = %+v", vars[1])
	}
}
