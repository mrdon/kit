package services

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// WidgetTokenTools defines shared tool metadata for managing the
// website chat widget's per-tenant embed tokens. All three are
// admin-only: distributing a token is a sensitive act because it lets
// the holder embed your knowledge base on any allowed origin.
//
// `create_widget_token` does not mint a token over chat — the plaintext
// would land in session_events and persist forever. Instead it returns
// the admin URL where the token is generated server-side, shown once
// in the browser, and never written to any LLM transcript.
var WidgetTokenTools = []ToolMeta{
	{Name: "create_widget_token", Description: "Get the admin URL for minting a website chat widget token. Tokens are generated on a web page (not via chat) to keep secrets out of session history. If an origin is provided, the URL pre-fills the form.", Schema: props(map[string]any{
		"origin": field("string", "Optional website origin to pre-fill (e.g. https://example.com)."),
	}), AdminOnly: true},
	{Name: "list_widget_tokens", Description: "List active widget tokens for this tenant. Plaintext is never returned; each row shows a synthesized placeholder.", Schema: props(map[string]any{}), AdminOnly: true},
	{Name: "revoke_widget_token", Description: "Revoke a widget token. The embed snippet using it stops working immediately.", Schema: propsReq(map[string]any{
		"token_id": field("string", "The widget token UUID, from list_widget_tokens"),
	}, "token_id"), AdminOnly: true},
}

// WidgetTokenService manages embed tokens for the website chat widget.
type WidgetTokenService struct {
	pool    *pgxpool.Pool
	baseURL string
}

// NewWidgetTokenService binds the service to a pool and the public
// base URL used to render the embed snippet.
func NewWidgetTokenService(pool *pgxpool.Pool, baseURL string) *WidgetTokenService {
	return &WidgetTokenService{pool: pool, baseURL: baseURL}
}

// WidgetTokenCreated is the one-shot response from Create. The
// plaintext is returned exactly once; afterwards only the hash is
// retained server-side.
type WidgetTokenCreated struct {
	ID            uuid.UUID
	Plaintext     string
	EmbedSnippet  string
	AllowedOrigin []string
}

// Create mints a new token, stores its hash, and returns the
// plaintext + embed snippet for one-time display to the admin.
func (s *WidgetTokenService) Create(ctx context.Context, c *Caller, allowedOrigins []string) (*WidgetTokenCreated, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	if len(allowedOrigins) == 0 {
		return nil, errors.New("at least one allowed origin is required")
	}
	for _, o := range allowedOrigins {
		if o == "" || (!strings.HasPrefix(o, "http://") && !strings.HasPrefix(o, "https://")) {
			return nil, fmt.Errorf("allowed origin %q must be a full http(s):// origin", o)
		}
	}
	plaintext, err := generateWidgetTokenPlaintext()
	if err != nil {
		return nil, fmt.Errorf("generating token: %w", err)
	}
	hash := models.HashWidgetToken(plaintext)
	row, err := models.CreateWidgetToken(ctx, s.pool, c.TenantID, c.UserID, hash, allowedOrigins)
	if err != nil {
		return nil, err
	}
	return &WidgetTokenCreated{
		ID:            row.ID,
		Plaintext:     plaintext,
		EmbedSnippet:  fmt.Sprintf(`<script src="%s/widget.js?token=%s" async></script>`, strings.TrimRight(s.baseURL, "/"), plaintext),
		AllowedOrigin: allowedOrigins,
	}, nil
}

// WidgetTokenSummary is the redacted form returned by List. The
// placeholder string is `wt_<first-8-of-uuid>` purely for display —
// the real plaintext is never recoverable.
type WidgetTokenSummary struct {
	ID             uuid.UUID
	Placeholder    string
	AllowedOrigins []string
	CreatedAt      string
	LastUsedAt     string
}

// List returns the tenant's active widget tokens, newest first.
func (s *WidgetTokenService) List(ctx context.Context, c *Caller) ([]WidgetTokenSummary, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	rows, err := models.ListActiveWidgetTokens(ctx, s.pool, c.TenantID)
	if err != nil {
		return nil, err
	}
	out := make([]WidgetTokenSummary, 0, len(rows))
	for _, r := range rows {
		sum := WidgetTokenSummary{
			ID:             r.ID,
			Placeholder:    "wt_" + r.ID.String()[:8] + "…",
			AllowedOrigins: r.AllowedOrigins,
			CreatedAt:      r.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"),
		}
		if r.LastUsedAt != nil {
			sum.LastUsedAt = r.LastUsedAt.UTC().Format("2006-01-02 15:04 UTC")
		}
		out = append(out, sum)
	}
	return out, nil
}

// Revoke marks the token revoked. Idempotent — revoking a missing or
// already-revoked token is not an error.
func (s *WidgetTokenService) Revoke(ctx context.Context, c *Caller, tokenID uuid.UUID) error {
	if !c.IsAdmin {
		return ErrForbidden
	}
	return models.RevokeWidgetToken(ctx, s.pool, c.TenantID, tokenID)
}

// AdminURL returns the admin page where widget tokens are minted. The
// optional `origin` value (if non-empty) is encoded as a query
// parameter so the form pre-fills.
func (s *WidgetTokenService) AdminURL(tenantSlug, origin string) string {
	base := strings.TrimRight(s.baseURL, "/")
	u := base + "/" + tenantSlug + "/widget"
	if origin != "" {
		u += "?origin=" + url.QueryEscape(origin)
	}
	return u
}

// generateWidgetTokenPlaintext returns a 32-byte cryptographically
// random token encoded as base32 (no padding, lowercase) with a `wt_`
// prefix so it's recognisable in logs and snippets.
func generateWidgetTokenPlaintext() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	return "wt_" + strings.ToLower(enc), nil
}
