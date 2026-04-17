package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func skillMCPHandler(name string, _ *pgxpool.Pool, svc *services.Services) mcpserver.ToolHandlerFunc {
	switch name {
	case "search_skills":
		return mcpSearchSkills(svc)
	case "load_skill":
		return mcpLoadSkill(svc)
	case "load_skill_file":
		return mcpLoadSkillFile(svc)
	case "list_skills":
		return mcpListSkills(svc)
	case "create_skill":
		return mcpCreateSkill(svc)
	case "update_skill":
		return mcpUpdateSkill(svc)
	case "delete_skill":
		return mcpDeleteSkill(svc)
	case "add_skill_file":
		return mcpAddSkillFile(svc)
	case "list_skill_files":
		return mcpListSkillFiles(svc)
	case "delete_skill_file":
		return mcpDeleteSkillFile(svc)
	default:
		return nil
	}
}

func mcpSearchSkills(svc *services.Services) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		query, _ := req.RequireString("query")
		results, err := svc.Skills.Search(ctx, caller, query)
		if err != nil {
			return nil, err
		}
		if len(results) == 0 {
			return mcp.NewToolResultText("No matching skills found."), nil
		}
		var b strings.Builder
		for _, s := range results {
			fmt.Fprintf(&b, "- [%s] %s — %s\n", s.ID, s.Name, s.Description)
		}
		return mcp.NewToolResultText(b.String()), nil
	})
}

func mcpLoadSkill(svc *services.Services) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("skill_id")
		skillID, err := uuid.Parse(idStr)
		if err != nil {
			// Not a UUID — try as a built-in skill name.
			skill, berr := svc.Skills.LoadByName(idStr)
			if errors.Is(berr, services.ErrNotFound) {
				return mcp.NewToolResultError("Skill not found."), nil
			}
			if berr != nil {
				return nil, berr
			}
			return mcp.NewToolResultText(skill.ToSKILLMD()), nil
		}
		skill, files, err := svc.Skills.Load(ctx, caller, skillID)
		if errors.Is(err, services.ErrNotFound) {
			return mcp.NewToolResultError("Skill not found."), nil
		}
		if errors.Is(err, services.ErrForbidden) {
			return mcp.NewToolResultError("Access denied."), nil
		}
		if err != nil {
			return nil, err
		}
		var b strings.Builder
		b.WriteString(skill.ToSKILLMD())
		if len(files) > 0 {
			b.WriteString("\n\n## Files\n")
			for _, f := range files {
				fmt.Fprintf(&b, "- [%s] %s\n", f.ID, f.Filename)
			}
		}
		return mcp.NewToolResultText(b.String()), nil
	})
}

func mcpLoadSkillFile(svc *services.Services) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("file_id")
		fileID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid file ID."), nil
		}
		ref, err := svc.Skills.LoadFile(ctx, caller, fileID)
		if errors.Is(err, services.ErrNotFound) {
			return mcp.NewToolResultError("File not found."), nil
		}
		if errors.Is(err, services.ErrForbidden) {
			return mcp.NewToolResultError("Access denied."), nil
		}
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(fmt.Sprintf("# %s\n\n%s", ref.Filename, ref.Content)), nil
	})
}

func mcpListSkills(svc *services.Services) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		search := req.GetString("search", "")
		skills, err := svc.Skills.List(ctx, caller, search)
		if err != nil {
			return nil, err
		}
		if len(skills) == 0 {
			return mcp.NewToolResultText("No skills found."), nil
		}
		return mcp.NewToolResultText(services.FormatSkillSummaries(skills)), nil
	})
}

func mcpCreateSkill(svc *services.Services) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		name, _ := req.RequireString("name")
		desc, _ := req.RequireString("description")
		content, _ := req.RequireString("content")
		scope := req.GetString("scope", "tenant")
		skill, err := svc.Skills.Create(ctx, caller, name, desc, content, "mcp", scope)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(fmt.Sprintf("Skill '%s' created (ID: %s).", skill.Name, skill.ID)), nil
	})
}

func mcpUpdateSkill(svc *services.Services) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("skill_id")
		skillID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid skill ID."), nil
		}
		args := req.GetArguments()
		var name, desc, content *string
		if v, ok := args["name"].(string); ok {
			name = &v
		}
		if v, ok := args["description"].(string); ok {
			desc = &v
		}
		if v, ok := args["content"].(string); ok {
			content = &v
		}
		if err := svc.Skills.Update(ctx, caller, skillID, name, desc, content); err != nil {
			return nil, err
		}
		return mcp.NewToolResultText("Skill updated."), nil
	})
}

func mcpDeleteSkill(svc *services.Services) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("skill_id")
		skillID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid skill ID."), nil
		}
		if err := svc.Skills.Delete(ctx, caller, skillID); err != nil {
			return nil, err
		}
		return mcp.NewToolResultText("Skill deleted."), nil
	})
}

func mcpAddSkillFile(svc *services.Services) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("skill_id")
		skillID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid skill ID."), nil
		}
		filename, _ := req.RequireString("filename")
		content, _ := req.RequireString("content")
		f, err := svc.Skills.AddFile(ctx, caller, skillID, filename, content)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(fmt.Sprintf("File '%s' attached (ID: %s).", f.Filename, f.ID)), nil
	})
}

func mcpListSkillFiles(svc *services.Services) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("skill_id")
		skillID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid skill ID."), nil
		}
		files, err := svc.Skills.ListFiles(ctx, caller, skillID)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return mcp.NewToolResultText("No files attached."), nil
		}
		var b strings.Builder
		for _, f := range files {
			fmt.Fprintf(&b, "- [%s] %s\n", f.ID, f.Filename)
		}
		return mcp.NewToolResultText(b.String()), nil
	})
}

func mcpDeleteSkillFile(svc *services.Services) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("file_id")
		fileID, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError("Invalid file ID."), nil
		}
		if err := svc.Skills.DeleteFile(ctx, caller, fileID); err != nil {
			return nil, err
		}
		return mcp.NewToolResultText("File deleted."), nil
	})
}
