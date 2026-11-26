package square

import (
	"math"
	"strconv"
	"time"
)

// Sale prices for the shop, from Square's automatic pricing rules.
//
// Only rules a shopper gets without doing anything are shown: AUTOMATIC
// rules with a plain "any of these products" set and a percentage or fixed
// discount. Coupons (ATTACHED), customer-group rules, happy-hour time
// periods, exclude sets, minimum spends and quantity deals are skipped. The
// website would otherwise advertise a price the register will not give.

// ShopSale is the best sale price a variation gets from a pricing rule. A
// zero StartsAt means "already on"; a zero EndsAt means "no end date".
type ShopSale struct {
	Name       string
	PriceCents int
	StartsAt   time.Time
	EndsAt     time.Time
}

type shopDiscount struct {
	Name     string `json:"name"`
	Type     string `json:"discount_type"`
	Percent  string `json:"percentage"`
	Amount   *money `json:"amount_money"`
	MaxMoney *money `json:"maximum_amount_money"`
}

type money struct {
	Amount int `json:"amount"`
}

type shopPricingRule struct {
	Name           string   `json:"name"`
	DiscountID     string   `json:"discount_id"`
	MatchProducts  string   `json:"match_products_id"`
	ExcludeProduct string   `json:"exclude_products_id"`
	Mode           string   `json:"application_mode"`
	TimePeriods    []string `json:"time_period_ids"`
	CustomerGroups []string `json:"customer_group_ids_any"`
	MinSubtotal    *money   `json:"minimum_order_subtotal_money"`
	FromDate       string   `json:"valid_from_date"`
	FromTime       string   `json:"valid_from_local_time"`
	UntilDate      string   `json:"valid_until_date"`
	UntilTime      string   `json:"valid_until_local_time"`
}

type shopProductSet struct {
	AnyIDs        []string `json:"product_ids_any"`
	AllIDs        []string `json:"product_ids_all"`
	AllProducts   bool     `json:"all_products"`
	QuantityExact *int     `json:"quantity_exact"`
	QuantityMin   *int     `json:"quantity_min"`
}

// shopRule is a pricing rule resolved to what the shop needs.
type shopRule struct {
	name     string
	discount shopDiscount
	all      bool
	ids      map[string]bool
	starts   time.Time
	ends     time.Time
}

// shopRules resolves the pricing rules worth showing, dropping any that have
// already ended.
func shopRules(objects []shopObject, loc *time.Location, now time.Time) []shopRule {
	discounts := map[string]shopDiscount{}
	sets := map[string]shopProductSet{}
	for i := range objects {
		o := &objects[i]
		if o.IsDeleted {
			continue
		}
		if o.DiscountData != nil {
			discounts[o.ID] = *o.DiscountData
		}
		if o.ProductSetData != nil {
			sets[o.ID] = *o.ProductSetData
		}
	}
	var out []shopRule
	for i := range objects {
		o := &objects[i]
		if o.IsDeleted || o.PricingRuleData == nil {
			continue
		}
		if r, ok := resolveRule(o.PricingRuleData, discounts, sets, loc); ok && (r.ends.IsZero() || r.ends.After(now)) {
			out = append(out, r)
		}
	}
	return out
}

func resolveRule(p *shopPricingRule, discounts map[string]shopDiscount, sets map[string]shopProductSet, loc *time.Location) (shopRule, bool) {
	if p.Mode != "AUTOMATIC" || len(p.TimePeriods) > 0 || len(p.CustomerGroups) > 0 ||
		p.ExcludeProduct != "" || p.MinSubtotal != nil {
		return shopRule{}, false
	}
	d, ok := discounts[p.DiscountID]
	if !ok || (d.Type != "FIXED_PERCENTAGE" && d.Type != "FIXED_AMOUNT") {
		return shopRule{}, false
	}
	set, ok := sets[p.MatchProducts]
	if !ok || len(set.AllIDs) > 0 || set.QuantityMin != nil ||
		(set.QuantityExact != nil && *set.QuantityExact != 1) {
		return shopRule{}, false
	}
	r := shopRule{name: p.Name, discount: d, all: set.AllProducts, ids: map[string]bool{}}
	for _, id := range set.AnyIDs {
		r.ids[id] = true
	}
	if r.name == "" {
		r.name = d.Name
	}
	r.starts = localTime(p.FromDate, p.FromTime, loc)
	r.ends = localTime(p.UntilDate, p.UntilTime, loc)
	return r, true
}

// localTime reads Square's seller-local date and time. Empty date: zero.
func localTime(date, clock string, loc *time.Location) time.Time {
	if date == "" {
		return time.Time{}
	}
	if clock == "" {
		clock = "00:00:00"
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", date+" "+clock, loc)
	if err != nil {
		return time.Time{}
	}
	return t
}

// bestSale is the lowest price any rule gives this variation. A rule matches
// the variation, its item, or one of the item's categories. On a tie, the
// sale that runs longer wins, so the shown end date is the real last day.
func bestSale(rules []shopRule, priceCents int, ids ...string) *ShopSale {
	if priceCents <= 0 {
		return nil
	}
	var best *ShopSale
	for _, r := range rules {
		if !r.matches(ids) {
			continue
		}
		price := priceCents - r.discountCents(priceCents)
		if price >= priceCents {
			continue
		}
		s := &ShopSale{Name: r.name, PriceCents: price, StartsAt: r.starts, EndsAt: r.ends}
		if best == nil || price < best.PriceCents ||
			(price == best.PriceCents && later(s.EndsAt, best.EndsAt)) {
			best = s
		}
	}
	return best
}

func (r shopRule) matches(ids []string) bool {
	if r.all {
		return true
	}
	for _, id := range ids {
		if r.ids[id] {
			return true
		}
	}
	return false
}

func (r shopRule) discountCents(priceCents int) int {
	off := 0
	switch r.discount.Type {
	case "FIXED_PERCENTAGE":
		pct, err := strconv.ParseFloat(r.discount.Percent, 64)
		if err != nil {
			return 0
		}
		off = int(math.Round(float64(priceCents) * pct / 100))
	case "FIXED_AMOUNT":
		if r.discount.Amount != nil {
			off = r.discount.Amount.Amount
		}
	}
	if m := r.discount.MaxMoney; m != nil && m.Amount > 0 && off > m.Amount {
		off = m.Amount
	}
	return min(max(off, 0), priceCents)
}

// later reports whether a ends after b, treating zero as "never ends".
func later(a, b time.Time) bool {
	if a.IsZero() {
		return !b.IsZero()
	}
	return !b.IsZero() && a.After(b)
}
