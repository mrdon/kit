package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

func registerMemoryTools(r *Registry, isAdmin bool) {
	for _, meta := range services.MemoryTools {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		r.Register(Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     memoryHandler(meta.Name),
		})
	}
}

func memoryHandler(name string) HandlerFunc {
	switch name {
	case "save_memory":
		return handleSaveMemory
	case "search_memories":
		return handleSearchMemories
	case "forget_memory":
		return handleForgetMemory
	default:
		return func(_ *ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown memory tool: %s", name)
		}
	}
}

func handleSaveMemory(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Content string `json:"content"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	if err := ec.Svc.Memories.Save(ec.Ctx, ec.Caller(), inp.Content, inp.Scope, ec.Session.ID); err != nil {
		return "", err
	}
	return "Got it, I'll remember that.", nil
}

func handleSearchMemories(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	results, err := ec.Svc.Memories.Search(ec.Ctx, ec.Caller(), inp.Query)
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
}

func handleForgetMemory(ec *ExecContext, input json.RawMessage) (string, error) {
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
	if err := ec.Svc.Memories.Forget(ec.Ctx, ec.Caller(), memoryID); err != nil {
		return "", err
	}
	return "Memory forgotten.", nil
}
