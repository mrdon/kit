package cogs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// The tools are read-only. Writes are physical facts and belong to the
// person standing at the tank (the iPad) or to the console's corrections,
// both of which go through Service.Record; an agent restating them would be
// a second, unaccountable way in.
func toolMetas() []services.ToolMeta {
	return []services.ToolMeta{
		{
			Name: "get_cogs_state",
			Description: "What is in every fermenter, brite tank, serving tank and barrel (beer, batch, stage, " +
				"volume), packaged stock per beer and format with the oldest batch's age, this week's jobs, " +
				"and the planned brews.",
			Schema: services.Props(map[string]any{}),
		},
		{
			Name: "get_cogs_history",
			Description: "What was logged in the brewery, newest first: brews, moves, packaging, losses, counts, " +
				"repacks, and anything undone. Optionally for one vessel by name (FV-2).",
			Schema: services.Props(map[string]any{
				"vessel": map[string]any{"type": "string", "description": "A vessel name, e.g. FV-2. Omit for all."},
				"limit":  map[string]any{"type": "integer", "description": "How many entries (default 25)."},
			}),
		},
	}
}

type historyArgs struct {
	Vessel string `json:"vessel"`
	Limit  int    `json:"limit"`
}

// runTool is the one handler both surfaces call, given the same bytes.
func runTool(ctx context.Context, a *App, caller *services.Caller, name string, raw []byte) (string, error) {
	if a.svc == nil || a.svc.pool == nil {
		return "", ErrNotConfigured
	}
	switch name {
	case "get_cogs_state":
		st, err := a.svc.State(ctx, caller)
		if err != nil {
			return "", err
		}
		return describeState(st), nil
	case "get_cogs_history":
		var args historyArgs
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("parsing get_cogs_history arguments: %w", err)
			}
		}
		return historyText(ctx, a.svc, caller, args)
	}
	return "", fmt.Errorf("unknown cogs tool: %s", name)
}

func historyText(ctx context.Context, svc *Service, caller *services.Caller, args historyArgs) (string, error) {
	var vessel *uuid.UUID
	if args.Vessel != "" {
		vs, err := listVessels(ctx, svc.pool, caller.TenantID, true)
		if err != nil {
			return "", err
		}
		for _, v := range vs {
			if strings.EqualFold(v.Name, strings.TrimSpace(args.Vessel)) {
				id := v.ID
				vessel = &id
			}
		}
		if vessel == nil {
			return "", fmt.Errorf("no vessel called %q", args.Vessel)
		}
	}
	if args.Limit <= 0 {
		args.Limit = 25
	}
	entries, err := svc.History(ctx, caller, vessel, args.Limit)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "Nothing logged yet.", nil
	}
	loc := svc.location(ctx, caller)
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "- %s: %s", e.At.In(loc).Format("Mon Jan 2"), e.Text)
		if e.By != "" {
			fmt.Fprintf(&b, " (%s)", e.By)
		}
		if e.Reversed {
			b.WriteString(" [undone]")
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// describeState is the state as text for the agent.
func describeState(st *State) string {
	var b strings.Builder
	if len(st.Vessels) == 0 {
		return "No vessels are set up yet. An admin adds them on the console's COGS setup page."
	}
	fmt.Fprintf(&b, "Vessels (today %s):\n", st.Today)
	for _, v := range st.Vessels {
		if v.Beer == "" {
			fmt.Fprintf(&b, "- %s (%s bbl %s): %s. %s\n", v.Name, num(v.SizeBBL), v.Kind, v.StageLabel, v.Next)
			continue
		}
		fmt.Fprintf(&b, "- %s (%s bbl %s): %s %s, %s bbl, %s · %s. Next: %s\n", v.Name, num(v.SizeBBL), v.Kind,
			v.Beer, v.BatchCode, bbl(v.Volume), v.StageLabel, v.AgeLabel, v.Next)
	}
	b.WriteString("\nPackaged:\n")
	if len(st.Packaged) == 0 {
		b.WriteString("- none recorded\n")
	}
	for _, p := range st.Packaged {
		fmt.Fprintf(&b, "- %s %s: %s units (%s bbl)", p.Beer, p.Format, num(p.Units), bbl(p.BBL))
		if p.OldestOn != "" {
			fmt.Fprintf(&b, ", oldest packaged %s", p.OldestOn)
		}
		if p.PastShelf {
			b.WriteString(", PAST SHELF LIFE")
		}
		b.WriteString("\n")
	}
	b.WriteString("\nPlanned brews:\n")
	if len(st.Plan) == 0 {
		b.WriteString("- none\n")
	}
	for _, p := range st.Plan {
		fmt.Fprintf(&b, "- %s: %s into %s, %s bbl", p.BrewOn, beerNameOf(st, p.BeerID), vesselNameOf(st, p.VesselID),
			bbl(p.VolumeBBL))
		if p.BatchID != nil {
			b.WriteString(" (brewed)")
		}
		if p.Why != "" {
			fmt.Fprintf(&b, ". %s", p.Why)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nThis week:\n")
	if len(st.ThisWeek) == 0 {
		b.WriteString("- nothing due\n")
	}
	for _, w := range st.ThisWeek {
		fmt.Fprintf(&b, "- %s", w.Text)
		if w.When != "" {
			fmt.Fprintf(&b, " (%s)", w.When)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func registerAgentTools(registerer any, caller *services.Caller, a *App) {
	r, ok := registerer.(*tools.Registry)
	if !ok || caller == nil || a.svc == nil {
		return
	}
	for _, meta := range toolMetas() {
		name := meta.Name
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler: func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
				return runTool(ec.Ctx, a, ec.Caller(), name, raw)
			},
		})
	}
}

func registerMCPTools(a *App) []mcpserver.ServerTool {
	var out []mcpserver.ServerTool
	for _, meta := range toolMetas() {
		name := meta.Name
		out = append(out, apps.MCPToolFromMeta(meta, mcpauth.WithCaller(
			func(ctx context.Context, req mcp.CallToolRequest, c *services.Caller) (*mcp.CallToolResult, error) {
				// Re-encoded so both surfaces parse the exact same bytes.
				raw, err := json.Marshal(req.GetArguments())
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				msg, err := runTool(ctx, a, c, name, raw)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(msg), nil
			})))
	}
	return out
}

func beerNameOf(st *State, id uuid.UUID) string {
	for _, b := range st.Beers {
		if b.ID == id {
			return b.Name
		}
	}
	return "a beer"
}

func vesselNameOf(st *State, id uuid.UUID) string {
	for _, v := range st.Vessels {
		if v.ID == id {
			return v.Name
		}
	}
	return "a vessel"
}
