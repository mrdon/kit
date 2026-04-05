package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

func registerMemoryTools(r *Registry, isAdmin bool) {
	r.Register(Def{
		Name: "save_memory", Description: "Save a fact for future conversations.",
		Schema: propsReq(map[string]any{
			"content": field("string", "The fact to remember"),
			"scope":   field("string", "Scope: 'user' (default), 'tenant', or a role name"),
		}, "content"),
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				Content string `json:"content"`
				Scope   string `json:"scope"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			scope := inp.Scope
			if scope == "" {
				scope = "user"
			}
			scopeType := "user"
			scopeValue := ec.User.SlackUserID
			if scope == "tenant" {
				scopeType = "tenant"
				scopeValue = "*"
			} else if scope != "user" {
				scopeType = "role"
				scopeValue = scope
			}
			if err := models.CreateMemory(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.Content, scopeType, scopeValue, ec.Session.ID); err != nil {
				return "", err
			}
			return "Got it, I'll remember that.", nil
		},
	})

	r.Register(Def{
		Name: "search_memories", Description: "Search saved memories for relevant facts.",
		Schema: propsReq(map[string]any{"query": field("string", "Search query")}, "query"),
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			userRoles, _ := models.GetUserRoleNames(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.ID, ec.Tenant.DefaultRoleID)
			results, err := models.SearchMemories(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.SlackUserID, userRoles, inp.Query)
			if err != nil {
				return "", err
			}
			if len(results) == 0 {
				return "No relevant memories found.", nil
			}
			var b strings.Builder
			b.WriteString("Memories:\n")
			for _, m := range results {
				fmt.Fprintf(&b, "- [%s] %s\n", m.ID, m.Content)
			}
			return b.String(), nil
		},
	})

	if !isAdmin {
		return
	}

	r.Register(Def{
		Name: "forget_memory", Description: "Delete a specific memory.",
		Schema: propsReq(map[string]any{"memory_id": field("string", "The memory UUID")}, "memory_id"), AdminOnly: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				MemoryID string `json:"memory_id"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			memoryID, err := uuid.Parse(inp.MemoryID)
			if err != nil {
				return "Invalid memory ID.", nil
			}
			if err := models.DeleteMemory(ec.Ctx, ec.Pool, ec.Tenant.ID, memoryID); err != nil {
				return "", err
			}
			return "Memory forgotten.", nil
		},
	})
}
