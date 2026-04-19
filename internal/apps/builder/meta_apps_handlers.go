// Package builder: meta_apps_handlers.go wraps the low-level app-CRUD
// functions in meta_apps.go with the JSON-in / JSON-out shape the agent and
// MCP adapters expect. Split out from meta_apps.go so the DB logic (SQL +
// scoping + FK handling) stays below the 500-LOC soft cap.
package builder

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

// execContextLike mirrors tools.ExecContext just enough for meta-tool
// handlers. Declared as a plain struct so tests can exercise handlers
// directly without building a full ExecContext. The production wiring in
// app.go converts a *tools.ExecContext to one of these inline.
type execContextLike struct {
	Ctx    context.Context
	Pool   *pgxpool.Pool
	Caller *services.Caller
}

// metaAppAgentHandler returns the handler for a given meta-tool name. Nil
// for unknown names so the registration loop in app.go skips them.
func metaAppAgentHandler(name string) func(ec *execContextLike, input json.RawMessage) (string, error) {
	switch name {
	case "create_app":
		return handleCreateApp
	case "list_apps":
		return handleListApps
	case "get_app":
		return handleGetApp
	case "delete_app":
		return handleDeleteApp
	case "purge_app_data":
		return handlePurgeAppData
	default:
		return nil
	}
}

func handleCreateApp(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	name, err := argString(m, "name")
	if err != nil {
		return "", err
	}
	desc, _ := argOptionalString(m, "description")

	app, err := createApp(ec.Ctx, ec.Pool, ec.Caller, name, desc)
	if err != nil {
		return "", err
	}
	return formatToolResult(toSummary(app))
}

func handleListApps(ec *execContextLike, _ json.RawMessage) (string, error) {
	apps, err := listApps(ec.Ctx, ec.Pool, ec.Caller)
	if err != nil {
		return "", err
	}
	out := make([]appSummary, 0, len(apps))
	for i := range apps {
		out = append(out, toSummary(&apps[i]))
	}
	return formatToolResult(out)
}

func handleGetApp(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	name, err := argString(m, "name")
	if err != nil {
		return "", err
	}
	detail, err := getAppDetail(ec.Ctx, ec.Pool, ec.Caller, name)
	if err != nil {
		return "", err
	}
	return formatToolResult(detail)
}

func handleDeleteApp(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	name, err := argString(m, "name")
	if err != nil {
		return "", err
	}
	if err := requireConfirm(m); err != nil {
		return "", err
	}
	if err := deleteApp(ec.Ctx, ec.Pool, ec.Caller, name); err != nil {
		return "", err
	}
	return formatToolResult(map[string]string{"deleted": name})
}

func handlePurgeAppData(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	name, err := argString(m, "name")
	if err != nil {
		return "", err
	}
	if err := requireConfirm(m); err != nil {
		return "", err
	}
	n, err := purgeAppData(ec.Ctx, ec.Pool, ec.Caller, name)
	if err != nil {
		return "", err
	}
	return formatToolResult(map[string]any{"purged": n})
}

// friendlyErr maps internal errors to a single-line message safe to return
// to the LLM. ErrForbidden / ErrMissingConfirm / ErrAppNotFound each have a
// stable phrasing so system prompts can reference them.
func friendlyErr(err error) string {
	switch {
	case errors.Is(err, ErrForbidden):
		return "You don't have permission: admin role required."
	case errors.Is(err, ErrMissingConfirm):
		return "This operation deletes data. Pass confirm=true to proceed."
	case errors.Is(err, ErrAppNotFound):
		return "App not found."
	case errors.Is(err, pgx.ErrNoRows):
		return "Not found."
	default:
		return err.Error()
	}
}
