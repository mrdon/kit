package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// vaultToolMetas is the shared metadata between agent and MCP surfaces.
// Tool descriptions are written for the LLM, not end-users — they steer
// the agent toward returning URLs (never values) and toward the
// metadata-only listing path for "find my X" intents.
var vaultToolMetas = []services.ToolMeta{
	{
		Name:        "list_secrets",
		Description: "List the caller's stored passwords / accounts / logins / vault entries. Use this for ANY 'list/show/what are my passwords/accounts/logins/secrets' request — do not invent a tool name like list_vault. Returns metadata only (id, title, username, url, tags, role_name) — never the secret value itself. Pass role_id to scope to one role; omit it to get every entry the caller can see.",
		Schema: services.Props(map[string]any{
			"q":       services.Field("string", "Optional full-text search query (matches title, url, username)"),
			"tag":     services.Field("string", "Optional tag filter"),
			"role_id": services.Field("string", "Optional role UUID to filter by — only return entries owned by this role. Caller must be a member or owner to see anything."),
			"limit":   services.Field("integer", "Max results (default 50, capped at 200)"),
		}),
	},
	{
		Name:        "find_secret",
		Description: "Find a specific stored password / account / login by name — wrapper around list_secrets that returns the best matches for a user-named service. Use when the user says 'find the password for X' or 'what's my Y login'.",
		Schema: services.PropsReq(map[string]any{
			"q": services.Field("string", "What the user is looking for, e.g. 'aws prod' or 'github work'"),
		}, "q"),
	},
	{
		Name:        "view_secret",
		Description: "Return a one-tap URL the user can open to see a secret's value in their browser. The URL points to the vault reveal page; the agent never sees the password. Use when the user asks to see, copy, or use a specific entry.",
		Schema: services.PropsReq(map[string]any{
			"id": services.Field("string", "Vault entry UUID (from list_secrets / find_secret)"),
		}, "id"),
	},
	{
		Name:        "start_add_secret",
		Description: "Return a URL the user can open to capture a new secret in their browser. The browser encrypts the value before sending it to the server. Use when the user wants to save a password or other secret. NEVER ask the user to paste their password into the chat.",
		Schema: services.Props(map[string]any{
			"title": services.Field("string", "Optional pre-fill for the title"),
			"url":   services.Field("string", "Optional pre-fill for the URL field"),
		}),
	},
	{
		Name:        "set_secret_role",
		Description: "Change which role owns a vault entry. role_id is required; pass the tenant's 'member' role (everyone is implicitly a member) to make the entry visible to the whole workspace, or any other role to scope to that team. Cross-role changes are gated; the user will see a confirmation card before it takes effect.",
		Schema: services.PropsReq(map[string]any{
			"id":      services.Field("string", "Vault entry UUID"),
			"role_id": services.Field("string", "Role UUID — required. Use the tenant's 'member' role for everyone."),
		}, "id", "role_id"),
		// PolicyGate: see registerVaultAgentTools — cross-role changes are
		// gated. Same-role no-ops bypass the gate at service level.
	},
	{
		Name:        "delete_secret",
		Description: "Delete a vault entry. Recoverable only by re-adding from another source — there is no undo. The caller must be authorized to view the entry.",
		Schema: services.PropsReq(map[string]any{
			"id": services.Field("string", "Vault entry UUID"),
		}, "id"),
	},
}

// ===== agent registration =====

func registerVaultAgentTools(r *tools.Registry, isAdmin bool, svc *Service) {
	for _, meta := range vaultToolMetas {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		def := tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     vaultAgentHandler(meta.Name, svc),
		}
		// Gated agent tools per the plan + CLAUDE.md "gated tools must
		// have one entry point" rule. The agent path runs through the
		// registry's PolicyGate interceptor (decision card → human
		// approval → svc call). The MCP path does not have an enforced
		// gate today, so mcp.go refuses these two tools outright with
		// a "use the agent or web" error rather than calling svc
		// directly.
		switch meta.Name {
		case "set_secret_role", "delete_secret":
			def.DefaultPolicy = tools.PolicyGate
		}
		r.Register(def)
	}
}

func vaultAgentHandler(name string, svc *Service) tools.HandlerFunc {
	switch name {
	case "list_secrets":
		return handleAgentListSecrets(svc)
	case "find_secret":
		return handleAgentFindSecret(svc)
	case "view_secret":
		return handleAgentViewSecret(svc)
	case "start_add_secret":
		return handleAgentStartAddSecret(svc)
	case "set_secret_role":
		return handleAgentSetSecretRole(svc)
	case "delete_secret":
		return handleAgentDeleteSecret(svc)
	}
	return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
		return "", fmt.Errorf("unknown vault tool: %s", name)
	}
}

// auditFromExecContext builds an auditCtx for an agent-driven action.
// IP/UA aren't available from the agent path; we leave them blank.
func auditFromExecContext(ec *tools.ExecContext) auditCtx {
	caller := ec.Caller()
	id := caller.UserID
	return auditCtx{
		pool:     ec.Pool,
		tenantID: caller.TenantID,
		actorID:  &id,
		ip:       (*netip.Addr)(nil),
	}
}

func handleAgentListSecrets(svc *Service) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			Q      string `json:"q"`
			Tag    string `json:"tag"`
			RoleID string `json:"role_id"`
			Limit  int    `json:"limit"`
		}
		_ = json.Unmarshal(input, &inp)
		var roleID *uuid.UUID
		if inp.RoleID != "" {
			rid, err := uuid.Parse(inp.RoleID)
			if err != nil {
				return "Invalid role_id.", nil
			}
			roleID = &rid
		}
		caller := ec.Caller()
		rows, err := svc.ListEntries(ec.Ctx, caller, inp.Q, inp.Tag, roleID, inp.Limit)
		if err != nil {
			return "", err
		}
		return formatEntryList(caller, rows), nil
	}
}

func handleAgentFindSecret(svc *Service) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			Q string `json:"q"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		caller := ec.Caller()
		rows, err := svc.ListEntries(ec.Ctx, caller, inp.Q, "", nil, 5)
		if err != nil {
			return "", err
		}
		return formatEntryList(caller, rows), nil
	}
}

func handleAgentViewSecret(svc *Service) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		entryID, err := uuid.Parse(inp.ID)
		if err != nil {
			return "Invalid id.", nil
		}
		caller := ec.Caller()
		// Authz check via GetEntry; we don't return ciphertext.
		audit := auditFromExecContext(ec)
		audit.userAgent = "agent"
		_, err = svc.GetEntry(ec.Ctx, caller, entryID, audit)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				return "No entry with that id, or you don't have access.", nil
			}
			return "", err
		}
		url := svc.absURL(fmt.Sprintf("/%s/apps/vault/reveal/%s", tenantSlug(ec), entryID))
		return "Open in your browser to view: " + url, nil
	}
}

func handleAgentStartAddSecret(svc *Service) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		}
		_ = json.Unmarshal(input, &inp)
		q := ""
		if inp.Title != "" || inp.URL != "" {
			parts := []string{}
			if inp.Title != "" {
				parts = append(parts, "title="+queryEscape(inp.Title))
			}
			if inp.URL != "" {
				parts = append(parts, "url="+queryEscape(inp.URL))
			}
			q = "?" + strings.Join(parts, "&")
		}
		url := svc.absURL(fmt.Sprintf("/%s/apps/vault/add%s", tenantSlug(ec), q))
		return "Open in your browser to capture the secret: " + url, nil
	}
}

func handleAgentSetSecretRole(svc *Service) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			ID     string `json:"id"`
			RoleID string `json:"role_id"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		entryID, err := uuid.Parse(inp.ID)
		if err != nil {
			return "Invalid id.", nil
		}
		if inp.RoleID == "" {
			return "role_id is required (use the tenant's 'member' role for everyone).", nil
		}
		rid, err := uuid.Parse(inp.RoleID)
		if err != nil {
			return "Invalid role_id.", nil
		}
		caller := ec.Caller()
		audit := auditFromExecContext(ec)
		audit.userAgent = "agent"
		if err := svc.SetEntryRole(ec.Ctx, caller, entryID, &rid, audit); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				return "No entry with that id, or you don't have access.", nil
			}
			if errors.Is(err, ErrCallerNotInRole) {
				return "You can only scope a secret to a role you're a member of.", nil
			}
			return "", err
		}
		return "Scope updated.", nil
	}
}

func handleAgentDeleteSecret(svc *Service) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		entryID, err := uuid.Parse(inp.ID)
		if err != nil {
			return "Invalid id.", nil
		}
		caller := ec.Caller()
		audit := auditFromExecContext(ec)
		audit.userAgent = "agent"
		if err := svc.DeleteEntry(ec.Ctx, caller, entryID, audit); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				return "No entry with that id, or you don't have access.", nil
			}
			return "", err
		}
		return "Deleted.", nil
	}
}

// ===== shared helpers =====

// formatEntryList renders metadata-only rows for the agent's response.
// scope_summary is the owning role's name (post-migration 047 every entry
// is role-scoped, so creator identity is irrelevant — visibility flows
// from role membership, not ownership).
func formatEntryList(_ *services.Caller, rows []EntryListItem) string {
	if len(rows) == 0 {
		return "No vault entries match."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d entry(ies):\n", len(rows))
	for _, e := range rows {
		fmt.Fprintf(&b, "- [%s] %s", e.ID, e.Title)
		if e.Username != "" {
			fmt.Fprintf(&b, " (%s)", e.Username)
		}
		if e.URL != "" {
			fmt.Fprintf(&b, " — %s", e.URL)
		}
		if e.ScopeSummary != "" {
			fmt.Fprintf(&b, " • role: %s", e.ScopeSummary)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nUse `view_secret` with an id to get a reveal URL.")
	return b.String()
}

func tenantSlug(ec *tools.ExecContext) string {
	if ec == nil || ec.Tenant == nil {
		return ""
	}
	return ec.Tenant.Slug
}

func queryEscape(s string) string { return url.QueryEscape(s) }
