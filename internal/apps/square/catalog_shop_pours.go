package square

import (
	"regexp"
	"strconv"
	"strings"
)

// The bar's pour for each beer, so the shop can say what a keg works out to
// per glass. A keg variation is named after the beer ("Little Dipper"), and
// the register item of the same name carries its glass sizes as variations
// ("16oz", "9oz", "4oz"). Strong beers pour smaller, so the house pour is the
// largest glass between 8 and 20 ounces: tasters, pitchers, crowlers and
// growlers are not a glass of beer.

// ShopPour is a beer's house pour at the bar.
type ShopPour struct {
	Ounces     int
	PriceCents int
}

var pourName = regexp.MustCompile(`^\s*(\d+)\s*oz\s*$`)

// shopPours maps a lower-cased item name to its house pour.
func shopPours(objects []shopObject) map[string]ShopPour {
	out := map[string]ShopPour{}
	for i := range objects {
		o := &objects[i]
		if o.Type != "ITEM" || o.IsDeleted || o.ItemData == nil || o.ItemData.IsArchived {
			continue
		}
		var best ShopPour
		for _, v := range o.ItemData.Variations {
			m := pourName.FindStringSubmatch(v.Data.Name)
			if v.IsDeleted || m == nil || v.Data.PriceMoney == nil {
				continue
			}
			oz, _ := strconv.Atoi(m[1])
			if oz < 8 || oz > 20 || oz <= best.Ounces {
				continue
			}
			best = ShopPour{Ounces: oz, PriceCents: v.Data.PriceMoney.Amount}
		}
		if best.Ounces > 0 {
			out[strings.ToLower(strings.TrimSpace(o.ItemData.Name))] = best
		}
	}
	return out
}
