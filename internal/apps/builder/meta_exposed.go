// Package builder: meta_exposed.go implements the expose-tool meta-tools
// — the admin surface that publishes a script function as a regular
// agent/MCP tool, revokes it, and lists the current exposure roster. Sibling
// to meta_apps.go / meta_scripts.go; same handler pattern, same admin guard.
//
// The registry + invocation path for exposed tools lives next door:
//
//   - tools_call_builtin.go handles script→script invocation (Phase 3).
//   - This file handles the admin CRUD surface (Phase 4d).
//   - registerExposedTools (below) + tools.ExposedToolRunner wire exposed
//     rows into the per-session agent registry so non-admin callers can
//     discover + invoke them via the normal tool catalog.
//
// Staleness: we lazily flag rows at registry-build time via a cheap parse
// of the target script's current revision body. If `def <fn_name>(` isn't
// present we UPDATE is_stale=true and skip registration. Invocation-time
// checks in tools_call_builtin.go continue to reject stale rows as a belt-
// and-braces second layer (so a race between flag and call is safe).
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

// metaExposedTools enumerates the three exposed-tool meta-tools. Admin-only;
// registered via App.ToolMetas so the services catalog picks them up.
var metaExposedTools = []services.ToolMeta{
	{
		Name:        "expose_script_function_as_tool",
		Description: "Publish a script function as an agent/MCP tool available to callers holding one of the listed roles. UNIQUE per (tenant, tool_name); trying to reuse a name fails.",
		Schema: services.PropsReq(map[string]any{
			"app":              services.Field("string", "Builder app name that owns the script"),
			"script":           services.Field("string", "Script identifier"),
			"fn_name":          services.Field("string", "Function inside the script to expose"),
			"tool_name":        services.Field("string", "Name the tool appears under in the catalog (tenant-unique)"),
			"description":      services.Field("string", "Human-readable description for the LLM"),
			"args_schema":      map[string]any{"type": "object", "description": "JSON Schema object describing the tool's arguments"},
			"visible_to_roles": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Roles that may see + invoke the tool. Empty list means no one (default-deny)."},
		}, "app", "script", "fn_name", "tool_name"),
		AdminOnly: true,
	},
	{
		Name:        "revoke_exposed_tool",
		Description: "Delete an exposed_tools row by tool_name. Audit rows (script_runs) survive; this only removes the publication.",
		Schema: services.PropsReq(map[string]any{
			"tool_name": services.Field("string", "Tool name to revoke"),
		}, "tool_name"),
		AdminOnly: true,
	},
	{
		Name:        "list_exposed_tools",
		Description: "List exposed tools in this tenant. Optional `app` filter restricts to one builder app. Result includes is_stale so admins can spot broken publications.",
		Schema: services.Props(map[string]any{
			"app": services.Field("string", "Optional: filter to one builder app name"),
		}),
		AdminOnly: true,
	},
}

// MetaExposedTools exposes the exposed-tool meta-tools for App.ToolMetas.
func MetaExposedTools() []services.ToolMeta { return metaExposedTools }

// exposedToolDTO is the JSON shape returned by expose/list. Kept separate
// from ExposedTool (the DB struct) so internal fields (tenant_id,
// created_by) don't leak — the tool surface is a contract.
type exposedToolDTO struct {
	ID             uuid.UUID       `json:"id"`
	ToolName       string          `json:"tool_name"`
	AppName        string          `json:"app"`
	ScriptName     string          `json:"script"`
	FnName         string          `json:"fn_name"`
	Description    string          `json:"description,omitempty"`
	ArgsSchema     json.RawMessage `json:"args_schema,omitempty"`
	VisibleToRoles []string        `json:"visible_to_roles"`
	IsStale        bool            `json:"is_stale"`
	CreatedAt      string          `json:"created_at"`
}

// metaExposedAgentHandler returns the handler for a given exposed-tool
// meta-tool name. Nil for unknown names.
func metaExposedAgentHandler(name string) func(ec *execContextLike, input json.RawMessage) (string, error) {
	switch name {
	case "expose_script_function_as_tool":
		return handleExposeScriptFunctionAsTool
	case "revoke_exposed_tool":
		return handleRevokeExposedTool
	case "list_exposed_tools":
		return handleListExposedTools
	default:
		return nil
	}
}

func handleExposeScriptFunctionAsTool(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	appName, err := argString(m, "app")
	if err != nil {
		return "", err
	}
	scriptName, err := argString(m, "script")
	if err != nil {
		return "", err
	}
	fnName, err := argString(m, "fn_name")
	if err != nil {
		return "", err
	}
	toolName, err := argString(m, "tool_name")
	if err != nil {
		return "", err
	}
	desc, _ := argOptionalString(m, "description")
	schemaJSON, err := argOptionalJSON(m, "args_schema")
	if err != nil {
		return "", err
	}
	roles, err := argStringList(m, "visible_to_roles")
	if err != nil {
		return "", err
	}
	dto, err := exposeScriptFunctionAsTool(ec.Ctx, ec.Pool, ec.Caller, appName, scriptName, fnName, toolName, desc, schemaJSON, roles)
	if err != nil {
		return "", err
	}
	return formatToolResult(dto)
}

func handleRevokeExposedTool(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	toolName, err := argString(m, "tool_name")
	if err != nil {
		return "", err
	}
	if err := revokeExposedTool(ec.Ctx, ec.Pool, ec.Caller, toolName); err != nil {
		return "", err
	}
	return formatToolResult(map[string]string{"revoked": toolName})
}

func handleListExposedTools(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	appName, _ := argOptionalString(m, "app")
	rows, err := listExposedTools(ec.Ctx, ec.Pool, ec.Caller, appName)
	if err != nil {
		return "", err
	}
	return formatToolResult(rows)
}

// exposeScriptFunctionAsTool inserts an exposed_tools row. The app+script
// join is resolved server-side so the LLM doesn't need to remember UUIDs;
// it names things the way it named them during create_app / create_script.
// UNIQUE (tenant_id, tool_name) surfaces via isUniqueViolation as a clean
// duplicate error.
func exposeScriptFunctionAsTool(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	appName, scriptName, fnName, toolName, description string,
	argsSchema json.RawMessage,
	visibleToRoles []string,
) (*exposedToolDTO, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}
	if toolName == "" {
		return nil, errors.New("tool_name must be non-empty")
	}
	if fnName == "" {
		return nil, errors.New("fn_name must be non-empty")
	}

	app, err := loadBuilderAppByName(ctx, pool, caller.TenantID, appName)
	if err != nil {
		return nil, err
	}
	script, err := loadScriptByName(ctx, pool, caller.TenantID, app.ID, scriptName)
	if err != nil {
		return nil, err
	}

	var (
		descArg   any
		schemaArg any
	)
	if description == "" {
		descArg = nil
	} else {
		descArg = description
	}
	if len(argsSchema) == 0 {
		schemaArg = nil
	} else {
		schemaArg = []byte(argsSchema)
	}
	if visibleToRoles == nil {
		visibleToRoles = []string{}
	}

	var (
		id        uuid.UUID
		createdAt string
	)
	err = pool.QueryRow(ctx, `
		INSERT INTO exposed_tools (
			tenant_id, tool_name, script_id, fn_name, description,
			args_schema, visible_to_roles, is_stale, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, false, $8)
		RETURNING id, to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
	`, caller.TenantID, toolName, script.ID, fnName, descArg,
		schemaArg, visibleToRoles, caller.UserID,
	).Scan(&id, &createdAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("exposed tool %q already exists in this tenant", toolName)
		}
		return nil, fmt.Errorf("inserting exposed_tools: %w", err)
	}

	return &exposedToolDTO{
		ID:             id,
		ToolName:       toolName,
		AppName:        appName,
		ScriptName:     scriptName,
		FnName:         fnName,
		Description:    description,
		ArgsSchema:     argsSchema,
		VisibleToRoles: visibleToRoles,
		IsStale:        false,
		CreatedAt:      createdAt,
	}, nil
}

// revokeExposedTool deletes one exposed_tools row. Full delete, not a
// soft flag: callers that want audit history read script_runs (which
// survives the delete via ON DELETE SET NULL on the FK path).
func revokeExposedTool(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	toolName string,
) error {
	if err := guardAdmin(caller); err != nil {
		return err
	}
	if toolName == "" {
		return errors.New("tool_name must be non-empty")
	}
	ct, err := pool.Exec(ctx, `
		DELETE FROM exposed_tools WHERE tenant_id = $1 AND tool_name = $2
	`, caller.TenantID, toolName)
	if err != nil {
		return fmt.Errorf("deleting exposed_tools: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("exposed tool %q not found", toolName)
	}
	return nil
}

// listExposedTools returns the caller tenant's exposed_tools rows, joined
// to scripts + builder_apps so admins see names (not UUIDs). Optional
// app filter lets the LLM scope to one bundle when it's inspecting a
// specific subset.
func listExposedTools(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	appName string,
) ([]exposedToolDTO, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}

	var (
		rows pgx.Rows
		err  error
	)
	if appName == "" {
		rows, err = pool.Query(ctx, `
			SELECT et.id, et.tool_name, ba.name, s.name, et.fn_name,
			       COALESCE(et.description, ''), et.args_schema,
			       COALESCE(et.visible_to_roles, '{}'::text[]), et.is_stale,
			       to_char(et.created_at, 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
			FROM exposed_tools et
			JOIN scripts s       ON s.id  = et.script_id
			JOIN builder_apps ba ON ba.id = s.builder_app_id
			WHERE et.tenant_id = $1
			ORDER BY et.tool_name
		`, caller.TenantID)
	} else {
		rows, err = pool.Query(ctx, `
			SELECT et.id, et.tool_name, ba.name, s.name, et.fn_name,
			       COALESCE(et.description, ''), et.args_schema,
			       COALESCE(et.visible_to_roles, '{}'::text[]), et.is_stale,
			       to_char(et.created_at, 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
			FROM exposed_tools et
			JOIN scripts s       ON s.id  = et.script_id
			JOIN builder_apps ba ON ba.id = s.builder_app_id
			WHERE et.tenant_id = $1 AND ba.name = $2
			ORDER BY et.tool_name
		`, caller.TenantID, appName)
	}
	if err != nil {
		return nil, fmt.Errorf("listing exposed_tools: %w", err)
	}
	defer rows.Close()

	out := make([]exposedToolDTO, 0)
	for rows.Next() {
		var (
			dto        exposedToolDTO
			schemaJSON []byte
		)
		if err := rows.Scan(
			&dto.ID, &dto.ToolName, &dto.AppName, &dto.ScriptName, &dto.FnName,
			&dto.Description, &schemaJSON, &dto.VisibleToRoles, &dto.IsStale,
			&dto.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning exposed_tools row: %w", err)
		}
		if len(schemaJSON) > 0 {
			dto.ArgsSchema = schemaJSON
		}
		out = append(out, dto)
	}
	return out, rows.Err()
}

// The tools.ExposedToolRunner implementation lives in meta_exposed_runner.go
// — split out so each file stays under the 500-LOC soft cap while sharing
// the exposedToolDTO type + CRUD helpers defined above.
