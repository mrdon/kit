package services

// Schema helpers — shared by all tool metadata definitions.
// These mirror the helpers in internal/tools/registry.go.

func props(fields map[string]any) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": fields,
	}
}

func propsReq(fields map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": fields,
		"required":   required,
	}
}

func field(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}
