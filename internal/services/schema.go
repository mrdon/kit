package services

import (
	"encoding/json"
	"maps"
)

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

// RequireApprovalField is the name of the universal caller-gate flag.
// Both the agent-side tool registry and the MCP adapter inject this
// property into every tool's schema so a single affordance is visible
// across both surfaces. Handlers never see it — it's stripped before
// dispatch.
const RequireApprovalField = "require_approval"

// RequireApprovalProperty returns the JSON-schema fragment that
// describes the require_approval flag. Kept terse; the system prompt
// explains when to reach for it.
func RequireApprovalProperty() map[string]any {
	return map[string]any{
		"type":        "boolean",
		"description": "Set to true to surface this call as an approval card before it runs. Use when the user asked to verify first, or when the intent or recipient is ambiguous. Omit otherwise.",
	}
}

// InjectRequireApprovalSchema returns a shallow-cloned schema with the
// require_approval boolean added to its properties map. Clones so we
// don't mutate a schema shared between registries/adapters. Non-object
// schemas are returned unchanged — the flag only makes sense on
// object-typed inputs. Idempotent: schemas already carrying the field
// are returned untouched.
func InjectRequireApprovalSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				RequireApprovalField: RequireApprovalProperty(),
			},
		}
	}
	if t, _ := schema["type"].(string); t != "" && t != "object" {
		return schema
	}
	out := make(map[string]any, len(schema)+1)
	maps.Copy(out, schema)
	props, _ := out["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	} else {
		cloned := make(map[string]any, len(props)+1)
		maps.Copy(cloned, props)
		props = cloned
	}
	if _, exists := props[RequireApprovalField]; !exists {
		props[RequireApprovalField] = RequireApprovalProperty()
	}
	out["properties"] = props
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
	}
	return out
}

// ReadRequireApproval extracts the require_approval flag from a tool
// input payload and returns the raw JSON with the field stripped so
// the handler never sees it. Invalid or non-bool values fall back to
// false (silently ignored).
func ReadRequireApproval(input json.RawMessage) (bool, json.RawMessage) {
	if len(input) == 0 {
		return false, input
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(input, &raw); err != nil {
		return false, input
	}
	v, ok := raw[RequireApprovalField]
	if !ok {
		return false, input
	}
	delete(raw, RequireApprovalField)
	var flag bool
	if err := json.Unmarshal(v, &flag); err != nil {
		flag = false
	}
	cleaned, err := json.Marshal(raw)
	if err != nil {
		return flag, input
	}
	return flag, cleaned
}
