package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

func registerWidgetTokenTools(r *Registry, isAdmin bool) {
	for _, meta := range services.WidgetTokenTools {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		r.Register(Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     widgetTokenHandler(meta.Name),
		})
	}
}

func widgetTokenHandler(name string) HandlerFunc {
	switch name {
	case "create_widget_token":
		return handleCreateWidgetToken
	case "list_widget_tokens":
		return handleListWidgetTokens
	case "revoke_widget_token":
		return handleRevokeWidgetToken
	default:
		return func(_ *ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown widget token tool: %s", name)
		}
	}
}

func handleCreateWidgetToken(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		AllowedOrigins []string `json:"allowed_origins"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	created, err := ec.Svc.WidgetTokens.Create(ec.Ctx, ec.Caller(), inp.AllowedOrigins)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Widget token created (ID: %s).\n\n", created.ID)
	fmt.Fprintf(&b, "Token (shown once — save it now): %s\n\n", created.Plaintext)
	b.WriteString("Embed snippet:\n")
	b.WriteString(created.EmbedSnippet)
	b.WriteString("\n\nAllowed origins:\n")
	for _, o := range created.AllowedOrigin {
		fmt.Fprintf(&b, "- %s\n", o)
	}
	return b.String(), nil
}

func handleListWidgetTokens(ec *ExecContext, _ json.RawMessage) (string, error) {
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

func handleRevokeWidgetToken(ec *ExecContext, input json.RawMessage) (string, error) {
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
