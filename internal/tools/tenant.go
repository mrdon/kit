package tools

import (
	"encoding/json"

	"github.com/mrdon/kit/internal/services"
)

func registerTenantTools(r *Registry, isAdmin bool) {
	for _, meta := range services.TenantTools {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		r.Register(Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     tenantHandler(meta.Name),
		})
	}
}

func tenantHandler(name string) HandlerFunc {
	switch name {
	case "update_tenant":
		return handleUpdateTenant
	default:
		return nil
	}
}

func handleUpdateTenant(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		BusinessType  string `json:"business_type"`
		Timezone      string `json:"timezone"`
		SetupComplete bool   `json:"setup_complete"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	if err := ec.Svc.Tenants.Update(ec.Ctx, ec.Caller(), inp.BusinessType, inp.Timezone); err != nil {
		return "", err
	}
	if inp.SetupComplete {
		return "Organization info saved and setup marked as complete!", nil
	}
	return "Organization info updated.", nil
}
