package services

// Schema helpers — shared by all tool metadata definitions.
// These mirror the helpers in internal/tools/registry.go.
// Exported variants (Props, PropsReq, Field) are used by apps.

func props(fields map[string]any) map[string]any {
	return Props(fields)
}

func propsReq(fields map[string]any, required ...string) map[string]any {
	return PropsReq(fields, required...)
}

func field(typ, desc string) map[string]any {
	return Field(typ, desc)
}

// Props builds a JSON Schema object with the given properties.
func Props(fields map[string]any) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": fields,
	}
}

// PropsReq builds a JSON Schema object with required fields.
func PropsReq(fields map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": fields,
		"required":   required,
	}
}

// Field creates a JSON Schema field with type and description.
func Field(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}
