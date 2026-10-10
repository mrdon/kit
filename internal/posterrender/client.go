package posterrender

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Renderer is what the posters app depends on. The HTTP client satisfies it
// in production; tests use a fake.
type Renderer interface {
	Render(ctx context.Context, req RenderRequest) (*RenderResponse, error)
	Options(ctx context.Context, req OptionsRequest) (*OptionsResponse, error)
	Inspect(ctx context.Context, image ImageRef) (*InspectResponse, error)
	Photo(ctx context.Context, image ImageRef, size int) ([]byte, error)
	Sheet(ctx context.Context, items []SheetItem, brand Brand) ([]byte, error)
	TemplateCheck(ctx context.Context, source string, brand Brand, photos []ImageRef) (*TemplateCheckResponse, error)
	CopyCheck(ctx context.Context, content Content, brand *Brand) (*CopyCheck, error)
	BrandCheck(ctx context.Context, brand Brand) ([]string, error)
	Healthy(ctx context.Context) bool
}

// ErrUnavailable is returned when the renderer cannot be reached at all,
// as opposed to refusing a request.
var ErrUnavailable = errors.New("poster renderer is not available")

// RequestError is a refusal from the renderer: bad source, missing image,
// a font Google does not serve. The message is written for the person or
// agent who sent the request.
type RequestError struct {
	Status  int
	Message string
}

func (e *RequestError) Error() string { return e.Message }

// Client calls the renderer over HTTP.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient builds a client for the renderer at baseURL. token may be empty
// when the renderer runs without one (the supervised localhost child).
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		// Options render up to seven posters plus the skipped candidates;
		// a cold run also downloads every photo in the set.
		http: &http.Client{Timeout: 120 * time.Second},
	}
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encoding renderer request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building renderer request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("reading renderer response: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("renderer returned HTTP %d", res.StatusCode)
		}
		if res.StatusCode >= 500 {
			return fmt.Errorf("renderer failed: %s", e.Error)
		}
		return &RequestError{Status: res.StatusCode, Message: e.Error}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decoding renderer response: %w", err)
	}
	return nil
}

// Render renders one poster or template.
func (c *Client) Render(ctx context.Context, req RenderRequest) (*RenderResponse, error) {
	var out RenderResponse
	if err := c.post(ctx, "/render", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Options generates varied posters for one piece of content.
func (c *Client) Options(ctx context.Context, req OptionsRequest) (*OptionsResponse, error) {
	var out OptionsResponse
	if err := c.post(ctx, "/options", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Inspect reports an image's size, orientation and provenance verdict.
func (c *Client) Inspect(ctx context.Context, image ImageRef) (*InspectResponse, error) {
	var out InspectResponse
	if err := c.post(ctx, "/inspect", map[string]any{"image": image}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Photo returns one image as a JPEG no larger than size on its long edge.
func (c *Client) Photo(ctx context.Context, image ImageRef, size int) ([]byte, error) {
	var out struct {
		JPEG []byte `json:"jpeg"`
	}
	if err := c.post(ctx, "/photo", map[string]any{"image": image, "size": size}, &out); err != nil {
		return nil, err
	}
	return out.JPEG, nil
}

// Sheet renders a numbered contact sheet of up to twelve images.
func (c *Client) Sheet(ctx context.Context, items []SheetItem, brand Brand) ([]byte, error) {
	var out struct {
		PNG []byte `json:"png"`
	}
	if err := c.post(ctx, "/sheet", map[string]any{"items": items, "brand": brand}, &out); err != nil {
		return nil, err
	}
	return out.PNG, nil
}

// TemplateCheck runs the activation check on a template's source.
func (c *Client) TemplateCheck(ctx context.Context, source string, brand Brand, photos []ImageRef) (*TemplateCheckResponse, error) {
	var out TemplateCheckResponse
	if err := c.post(ctx, "/template/check", map[string]any{"source": source, "brand": brand, "photos": photos}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CopyCheck applies the copy rules to content.
func (c *Client) CopyCheck(ctx context.Context, content Content, brand *Brand) (*CopyCheck, error) {
	var out CopyCheck
	if err := c.post(ctx, "/copy/check", map[string]any{"content": content, "brand": brand}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BrandCheck reports which of the brand's fonts cannot be loaded.
func (c *Client) BrandCheck(ctx context.Context, brand Brand) ([]string, error) {
	var out struct {
		Problems []string `json:"problems"`
	}
	if err := c.post(ctx, "/brand/check", map[string]any{"brand": brand}, &out); err != nil {
		return nil, err
	}
	return out.Problems, nil
}

// Healthy reports whether the renderer answers.
func (c *Client) Healthy(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return false
	}
	res, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode == http.StatusOK
}
