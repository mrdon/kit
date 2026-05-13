package widget

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// registerWidgetTokenTools wires the three admin token tools onto the
// agent registry. create_widget_token does NOT mint over chat (which
// would persist plaintext in session_events). It returns the admin URL
// the user opens in their browser; the actual mint happens in the
// Slack-OAuth-gated web handler.
func registerWidgetTokenTools(r *tools.Registry, isAdmin bool) {
	for _, meta := range services.WidgetTokenTools {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     widgetTokenHandler(meta.Name),
		})
	}
}

func widgetTokenHandler(name string) tools.HandlerFunc {
	switch name {
	case "create_widget_token":
		return handleCreateWidgetToken
	case "list_widget_tokens":
		return handleListWidgetTokens
	case "revoke_widget_token":
		return handleRevokeWidgetToken
	default:
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown widget token tool: %s", name)
		}
	}
}

func handleCreateWidgetToken(ec *tools.ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Origin string `json:"origin"`
	}
	_ = json.Unmarshal(input, &inp)
	if ec.Tenant == nil {
		return "", errors.New("tenant not resolved")
	}
	adminURL := ec.Svc.WidgetTokens.AdminURL(ec.Tenant.Slug, inp.Origin)
	var b strings.Builder
	b.WriteString("To mint a widget token, open this URL in your browser (admin only):\n\n")
	b.WriteString(adminURL)
	b.WriteString("\n\nThe token is shown once on the resulting page and never written to chat history. ")
	b.WriteString("If you provided an origin, the form is pre-filled — just click Generate.")
	return b.String(), nil
}

func handleListWidgetTokens(ec *tools.ExecContext, _ json.RawMessage) (string, error) {
	tokens, err := ec.Svc.WidgetTokens.List(ec.Ctx, ec.Caller())
	if err != nil {
		return "", err
	}
	if len(tokens) == 0 {
		return "No active widget tokens.", nil
	}
	var b strings.Builder
	b.WriteString("Active widget tokens:\n")
	for _, t := range tokens {
		fmt.Fprintf(&b, "- [%s] %s · origins: %s · created: %s",
			t.ID, t.Placeholder, strings.Join(t.AllowedOrigins, ", "), t.CreatedAt)
		if t.LastUsedAt != "" {
			fmt.Fprintf(&b, " · last used: %s", t.LastUsedAt)
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func handleRevokeWidgetToken(ec *tools.ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		TokenID string `json:"token_id"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	id, err := uuid.Parse(inp.TokenID)
	if err != nil {
		return "Invalid token ID.", nil
	}
	if err := ec.Svc.WidgetTokens.Revoke(ec.Ctx, ec.Caller(), id); err != nil {
		return "", err
	}
	return "Widget token revoked.", nil
}
