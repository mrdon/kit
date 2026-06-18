package expense

import (
	"encoding/json"

	"github.com/mrdon/kit/internal/tools"
)

func registerExpenseAgentTools(r *tools.Registry, isAdmin bool, svc *ExpenseService) {
	for _, meta := range expenseTools {
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
