package trivia

import (
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/tools"
)

// registerAgentTools exposes both trivia tools to the LLM agent. Each handler
// runs the SAME dispatchCore the MCP surface uses, so the two cannot drift.
func registerAgentTools(r *tools.Registry, pool *pgxpool.Pool, svc *Service) {
	for _, meta := range triviaTools {
		name := meta.Name
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler: func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
				return dispatchCore(ec.Ctx, ec.Caller(), pool, svc, name, input)
			},
		})
	}
}
