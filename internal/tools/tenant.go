package tools

import (
	"encoding/json"

	"github.com/mrdon/kit/internal/models"
)

func registerTenantTools(r *Registry, isAdmin bool) {
	if !isAdmin {
		return
	}

	r.Register(Def{
		Name:        "update_tenant",
		Description: "Update the organization's business info and mark setup as complete.",
		Schema: propsReq(map[string]any{
			"business_type":  field("string", "Type of business (e.g., 'brewery', 'nonprofit')"),
			"timezone":       field("string", "IANA timezone (e.g., 'America/Denver')"),
			"setup_complete": map[string]any{"type": "boolean", "description": "Mark setup as complete"},
		}, "business_type"),
		AdminOnly: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				BusinessType  string `json:"business_type"`
				Timezone      string `json:"timezone"`
				SetupComplete bool   `json:"setup_complete"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			tz := inp.Timezone
			if tz == "" {
				tz = "UTC"
			}
			if err := models.UpdateTenantSetup(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.BusinessType, tz); err != nil {
				return "", err
			}
			if inp.SetupComplete {
				return "Organization info saved and setup marked as complete!", nil
			}
			return "Organization info updated.", nil
		},
	})
}
