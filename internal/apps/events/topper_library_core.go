package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// The topper library's tools. Like every events tool they run through
// dispatchCore, so the agent and MCP surfaces are the same code.

// Appended to eventsTools at init, so both surfaces pick them up from the one
// list they already range over.
func init() { eventsTools = append(eventsTools, topperLibraryTools...) }

var topperLibraryTools = []services.ToolMeta{
	{
		Name: "events_topper_library",
		Description: "List the ready-made table toppers staff can print from the bar iPad's Table topper " +
			"page, beside the week's generated events card.",
		Schema: services.Props(map[string]any{}),
	},
	{
		Name: "events_topper_upload_url",
		Description: "Get a one-time link for adding a ready-made table topper PDF to the library the bar " +
			"iPad prints from. Tool calls carry text, not file bytes, so POST the file to the link: " +
			"curl -F pdf=@topper.pdf '<url>'. PDF only, up to 10MB, printed exactly as uploaded, so lay it " +
			"out for the sheet it prints on. The link works once and expires after 15 minutes. A title that " +
			"is already in the library replaces that topper's file and keeps its button.",
		Schema: services.PropsReq(map[string]any{
			"title": services.Field("string", "Button label staff will see, e.g. \"Happy hour\". Under 60 characters."),
		}, "title"),
	},
	{
		Name:        "events_topper_remove",
		Description: "Remove a ready-made table topper from the library, file and all.",
		Schema: services.PropsReq(map[string]any{
			"topper_id": services.Field("string", "Topper id, from events_topper_library."),
		}, "topper_id"),
	},
}

// dispatchTopperLibrary handles the library's tools; ok is false for any
// other name.
func dispatchTopperLibrary(ctx context.Context, caller *services.Caller, name string, raw json.RawMessage) (string, bool, error) {
	app := Instance()
	if app == nil {
		return "", true, errors.New("events app not available")
	}
	var out string
	var err error
	switch name {
	case "events_topper_library":
		out, err = coreTopperLibrary(ctx, app, caller)
	case "events_topper_upload_url":
		out, err = coreTopperUploadURL(ctx, app, caller, raw)
	case "events_topper_remove":
		out, err = coreTopperRemove(ctx, app, caller, raw)
	default:
		return "", false, nil
	}
	return out, true, err
}

func coreTopperLibrary(ctx context.Context, app *App, caller *services.Caller) (string, error) {
	toppers, err := listLibraryToppers(ctx, app.pool, caller.TenantID)
	if err != nil {
		return "", err
	}
	if len(toppers) == 0 {
		return "The topper library is empty. Add one with events_topper_upload_url.", nil
	}
	var b strings.Builder
	b.WriteString("Ready-made table toppers on the bar iPad:\n")
	for _, t := range toppers {
		fmt.Fprintf(&b, "- %s (id %s, updated %s)\n", t.Title, t.ID, t.UpdatedAt.Format("2006-01-02"))
	}
	return b.String(), nil
}

func coreTopperUploadURL(ctx context.Context, app *App, caller *services.Caller, raw json.RawMessage) (string, error) {
	var in struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("parsing input: %w", err)
	}
	title, err := cleanTopperTitle(in.Title)
	if err != nil {
		return err.Error(), nil
	}
	link, err := app.TopperUploadLink(ctx, caller.TenantID, caller.UserID, title)
	if err != nil {
		return "", err
	}
	return "One-time upload link for \"" + title + "\", good for 15 minutes and a single POST:\n\n" +
		"  curl -F pdf=@topper.pdf '" + link + "'\n\n" +
		"PDF only, up to 10MB. If a topper with this title exists, its file is replaced.", nil
}

func coreTopperRemove(ctx context.Context, app *App, caller *services.Caller, raw json.RawMessage) (string, error) {
	var in struct {
		TopperID string `json:"topper_id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("parsing input: %w", err)
	}
	id, err := uuid.Parse(strings.TrimSpace(in.TopperID))
	if err != nil {
		return "topper_id must be a topper id from events_topper_library.", nil
	}
	title, err := deleteLibraryTopper(ctx, app.pool, caller.TenantID, id)
	if errors.Is(err, errTopperNotFound) {
		return "No such topper.", nil
	}
	if err != nil {
		return "", err
	}
	return "Removed \"" + title + "\" from the topper library.", nil
}
