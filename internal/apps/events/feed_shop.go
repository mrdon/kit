package events

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/square"
	"github.com/mrdon/kit/internal/auth"
)

// The website's shop feed: the Square "Online Shop" catalog, for the site
// build to render as a static product page.
//
// It lives beside the events feed rather than in the square app because it is
// the same kind of thing -- a build-time projection of a tenant's data onto
// the open web -- and it authenticates the same way, with the site-build
// token. One secret per website, not one per feed.
//
// Like the events feed, the payload is assembled field by field into a type
// of its own. Square items carry costs, vendor ids and inventory notes; none
// of that may leak because a field was added to a struct.

// ShopFeed is the wire contract the website builds against.
type ShopFeed struct {
	GeneratedAt string            `json:"generated_at"`
	Sections    []ShopFeedSection `json:"sections"`
}

// ShopFeedSection is one shop category, in Square's order.
type ShopFeedSection struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Items []ShopFeedItem `json:"items"`
}

// ShopFeedItem is one product.
type ShopFeedItem struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	ImageURL    string              `json:"image_url,omitempty"`
	Variations  []ShopFeedVariation `json:"variations"`
}

// ShopFeedVariation is one size or flavour. PriceCents is omitted for a
// variable-priced variation rather than sent as a sentinel.
type ShopFeedVariation struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PriceCents *int   `json:"price_cents,omitempty"`
	Currency   string `json:"currency,omitempty"`
	SoldOut    bool   `json:"sold_out,omitempty"`
	// Sale is the price from an automatic Square pricing rule, present while
	// the rule is running or still to come. The site shows it only between
	// starts_at and ends_at; either may be absent (already on / no end).
	Sale *ShopFeedSale `json:"sale,omitempty"`
	// Pour is the bar's glass of the beer this variation is named after
	// (a keg of Little Dipper -> a 16oz pour at $6.50), so the site can show
	// what a keg works out to per glass. Absent when no beer matches.
	Pour *ShopFeedPour `json:"pour,omitempty"`
}

// ShopFeedPour is a beer's house pour at the bar.
type ShopFeedPour struct {
	Ounces     int `json:"ounces"`
	PriceCents int `json:"price_cents"`
}

// ShopFeedSale is one variation's sale price and when it applies, as RFC 3339
// timestamps in the venue's zone.
type ShopFeedSale struct {
	Name       string `json:"name,omitempty"`
	PriceCents int    `json:"price_cents"`
	StartsAt   string `json:"starts_at,omitempty"`
	EndsAt     string `json:"ends_at,omitempty"`
}

func shopFeedSale(s *square.ShopSale) *ShopFeedSale {
	if s == nil {
		return nil
	}
	out := &ShopFeedSale{Name: s.Name, PriceCents: s.PriceCents}
	if !s.StartsAt.IsZero() {
		out.StartsAt = s.StartsAt.Format(time.RFC3339)
	}
	if !s.EndsAt.IsZero() {
		out.EndsAt = s.EndsAt.Format(time.RFC3339)
	}
	return out
}

// shopFeed projects the catalog's sections onto the wire contract.
func shopFeed(sections []square.ShopSection, now time.Time) ShopFeed {
	feed := ShopFeed{GeneratedAt: now.UTC().Format(time.RFC3339), Sections: []ShopFeedSection{}}
	for _, s := range sections {
		sec := ShopFeedSection{ID: s.ID, Name: s.Name, Items: []ShopFeedItem{}}
		for _, it := range s.Items {
			item := ShopFeedItem{ID: it.ID, Name: it.Name, Description: it.Description,
				ImageURL: it.ImageURL, Variations: []ShopFeedVariation{}}
			for _, v := range it.Variations {
				fv := ShopFeedVariation{ID: v.ID, Name: v.Name, SoldOut: v.SoldOut, Sale: shopFeedSale(v.Sale)}
				if v.Pour != nil {
					fv.Pour = &ShopFeedPour{Ounces: v.Pour.Ounces, PriceCents: v.Pour.PriceCents}
				}
				if v.PriceCents >= 0 {
					price := v.PriceCents
					fv.PriceCents, fv.Currency = &price, v.Currency
				}
				item.Variations = append(item.Variations, fv)
			}
			sec.Items = append(sec.Items, item)
		}
		feed.Sections = append(feed.Sections, sec)
	}
	return feed
}

// registerShopFeedRoute mounts the shop feed on the same public, token-gated
// path as the events feed, so disabling the events app 404s it too.
func (a *App) registerShopFeedRoute(mux apps.Mux) {
	mux.Handle("GET /{slug}/events/shop.json",
		auth.TenantFromPath(a.pool)(http.HandlerFunc(a.handleShopFeed)))
}

// handleShopFeed serves the shop feed. Auth and the not-configured 404 match
// handleFeed: one token, one answer to "may you see this".
func (a *App) handleShopFeed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	settings, err := getSettings(ctx, a.pool, tenant.ID)
	if err != nil {
		slog.Error("shop feed: loading settings", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if settings.FeedToken == "" {
		http.NotFound(w, r)
		return
	}
	if !validFeedToken(r, settings.FeedToken) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="events feed"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	client, err := square.Instance().LoadClient(ctx, tenant.ID)
	if errors.Is(err, square.ErrNotConfigured) {
		// No Square connection: 404 so the build fails loudly instead of
		// publishing an empty shop.
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.Error("shop feed: loading square client", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sections, err := client.ShopCatalog(ctx, settings.Loc(), timeNow())
	if err != nil {
		slog.Error("shop feed: reading catalog", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if err := json.NewEncoder(w).Encode(shopFeed(sections, timeNow())); err != nil {
		slog.Warn("shop feed: writing response", "tenant_id", tenant.ID, "error", err)
	}
}
