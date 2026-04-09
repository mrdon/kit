package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mrdon/kit/internal/services"
)

func registerUserTools(r *Registry) {
	for _, meta := range services.UserTools {
		r.Register(Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     userHandler(meta.Name),
		})
	}
}

func userHandler(name string) HandlerFunc {
	if name != "find_user" {
		return func(_ *ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown user tool: %s", name)
		}
	}
	return func(ec *ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		users, err := ec.Svc.Users.Find(ec.Ctx, ec.Caller(), inp.Query)
		if err != nil {
			return "", fmt.Errorf("finding user: %w", err)
		}
		if len(users) == 0 {
			return fmt.Sprintf("No users found matching %q.", inp.Query), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Found %d user(s):\n", len(users))
		for _, u := range users {
			b.WriteString("- " + services.FormatUserLine(&u))
			if u.IsAdmin {
				b.WriteString(" [admin]")
			}
			b.WriteString("\n")
		}
		return b.String(), nil
	}
}
