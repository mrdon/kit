package events

import (
	"encoding/json"

	"github.com/mrdon/kit/internal/tools"
)

// registerAgentTools exposes every events tool to the LLM agent. Each handler
// runs the SAME dispatchCore the MCP surface uses, so the two cannot drift.
func registerAgentTools(r *tools.Registry, svc *Service, isAdmin bool) {
	for _, meta := range eventsTools {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		name := meta.Name
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler: func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
				return dispatchCore(ec.Ctx, ec.Caller(), svc, name, input)
			},
		})
	}
}
