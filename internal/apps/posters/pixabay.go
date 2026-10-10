package posters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mrdon/kit/internal/posterrender"
)

// Pixabay is the one stock source, and only when a tenant turns it on.
// Terms that shape this code: responses are cached 24 hours, images are
// downloaded rather than hotlinked (the renderer does that), the agent
// names Pixabay when it uses one, and 100 requests a minute per key.
// Without approved full API access the largest size is largeImageURL at
// 1280px: fine for 1080 canvases, soft on a 2400px hero.

const pixabayPrefix = "pixabay:"

// StockPhoto is one search hit.
type StockPhoto struct {
	ID       string `json:"id"`
	Tags     string `json:"tags"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Preview  string `json:"preview_url"`
	Large    string `json:"large_url"`
	PageURL  string `json:"page_url"`
	User     string `json:"user"`
	Likes    int    `json:"likes"`
	Comments int    `json:"comments"`
}

// Ref is the renderer's reference for the hit.
func (s StockPhoto) Ref() posterrender.ImageRef {
	return posterrender.ImageRef{ID: pixabayPrefix + s.ID, Source: posterrender.SourcePixabay, URL: s.Large, Modified: s.ID, Folder: "pixabay"}
}

type pixabayClient struct {
	key  string
	rdb  *redis.Client
	http *http.Client
}

func newPixabay(key string, rdb *redis.Client) *pixabayClient {
	return &pixabayClient{key: key, rdb: rdb, http: &http.Client{Timeout: 20 * time.Second}}
}

func (p *pixabayClient) enabled() bool { return p != nil && p.key != "" }

type pixabayHit struct {
	ID            int    `json:"id"`
	Tags          string `json:"tags"`
	ImageWidth    int    `json:"imageWidth"`
	ImageHeight   int    `json:"imageHeight"`
	PreviewURL    string `json:"previewURL"`
	WebformatURL  string `json:"webformatURL"`
	LargeImageURL string `json:"largeImageURL"`
	PageURL       string `json:"pageURL"`
	User          string `json:"user"`
	Likes         int    `json:"likes"`
	Comments      int    `json:"comments"`
}

type pixabayResponse struct {
	Hits []pixabayHit `json:"hits"`
}

// toStock maps one API hit.
func toStock(h pixabayHit) StockPhoto {
	large := h.LargeImageURL
	if large == "" {
		large = h.WebformatURL
	}
	return StockPhoto{ID: strconv.Itoa(h.ID), Tags: h.Tags, Width: h.ImageWidth, Height: h.ImageHeight, Preview: h.PreviewURL, Large: large, PageURL: h.PageURL, User: h.User, Likes: h.Likes, Comments: h.Comments}
}

// query runs one API call through the 24h cache.
func (p *pixabayClient) query(ctx context.Context, cacheKey string, params url.Values) ([]StockPhoto, error) {
	if !p.enabled() {
		return nil, errors.New("stock photos are not configured (PIXABAY_API_KEY)")
	}
	if p.rdb != nil {
		if b, err := p.rdb.Get(ctx, cacheKey).Bytes(); err == nil {
			var out []StockPhoto
			if json.Unmarshal(b, &out) == nil {
				return out, nil
			}
		}
	}
	params.Set("key", p.key)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://pixabay.com/api/?"+params.Encode(), nil)
	res, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling Pixabay: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("the Pixabay rate limit was reached; try again in %s seconds", res.Header.Get("X-RateLimit-Reset"))
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the Pixabay API returned %d", res.StatusCode)
	}
	var pr pixabayResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, fmt.Errorf("decoding Pixabay response: %w", err)
	}
	out := make([]StockPhoto, 0, len(pr.Hits))
	for _, h := range pr.Hits {
		out = append(out, toStock(h))
	}
	if p.rdb != nil {
		if b, err := json.Marshal(out); err == nil {
			_ = p.rdb.Set(ctx, cacheKey, b, 24*time.Hour).Err()
		}
	}
	return out, nil
}

// Search runs a photo search.
func (p *pixabayClient) Search(ctx context.Context, query string) ([]StockPhoto, error) {
	query = strings.TrimSpace(query)
	q := url.Values{}
	q.Set("q", query)
	q.Set("image_type", "photo")
	q.Set("safesearch", "true")
	q.Set("per_page", "20")
	return p.query(ctx, "posters:pixabay:"+strings.ToLower(query), q)
}

// Lookup resolves a pixabay:<id> reference to a current download URL.
func (p *pixabayClient) Lookup(ctx context.Context, id string) (*StockPhoto, error) {
	id = strings.TrimPrefix(id, pixabayPrefix)
	q := url.Values{}
	q.Set("id", id)
	hits, err := p.query(ctx, "posters:pixabay:id:"+id, q)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("no Pixabay image %s", id)
	}
	return &hits[0], nil
}
