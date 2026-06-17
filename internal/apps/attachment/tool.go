package attachment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps"
	store "github.com/mrdon/kit/internal/attachment"
	"github.com/mrdon/kit/internal/ingest"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// attachmentTools is the shared metadata for the agent + MCP surfaces.
var attachmentTools = []services.ToolMeta{
	{
		Name: "read_attachment",
		Description: `Read the contents of a file the user attached to this conversation.

The conversation lists available attachments inline (filename, type, id).
Call this with the attachment's id to actually read it — its bytes are NOT
in your context until you do.

For images (receipts, screenshots, photos) this returns a faithful text
transcription plus a brief description. For PDFs and documents it returns
the extracted text. Pass 'instructions' to target what you need (e.g.
"extract the vendor, date, total and tax" for a receipt) — otherwise you
get a full transcription.`,
		Schema: services.PropsReq(map[string]any{
			"attachment_id": services.Field("string", "The attachment id shown in the conversation."),
			"instructions":  services.Field("string", "Optional: what to extract or focus on (e.g. 'vendor, date, total'). Omit for a full transcription."),
		}, "attachment_id"),
	},
}

// readAttachment is the single materialization point for every surface and
// file type: image -> vision transcription, text-extractable doc ->
// extracted text, otherwise a metadata note. It always returns text so it
// fits the existing string-based tool plumbing.
func readAttachment(ctx context.Context, svc *store.Service, sender anthropic.Sender, tenantID, id uuid.UUID, instructions string) (string, error) {
	if svc == nil {
		return "", errors.New("attachment store unavailable")
	}
	meta, raw, err := svc.Load(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", errors.New("attachment not found")
		}
		return "", err
	}

	switch {
	case strings.HasPrefix(meta.Mime, "image/"):
		if sender == nil {
			return "", errors.New("vision is not configured")
		}
		return anthropic.DescribeImage(ctx, sender, raw, meta.Mime, instructions)
	case isExtractable(meta.Filename, meta.Mime):
		text, err := ingest.ExtractText(raw, meta.Filename, meta.Mime)
		if err != nil {
			return "", fmt.Errorf("extracting text: %w", err)
		}
		if strings.TrimSpace(text) == "" {
			return metadataNote(meta.Filename, meta.Mime, meta.Size), nil
		}
		return text, nil
	default:
		return metadataNote(meta.Filename, meta.Mime, meta.Size), nil
	}
}

func metadataNote(filename, mime string, size int) string {
	return fmt.Sprintf("Attachment %q (%s, %d bytes): no text could be extracted from this file type.", filename, mime, size)
}

// isExtractable reports whether ingest.ExtractText will produce meaningful
// text (rather than returning raw binary as a string).
func isExtractable(filename, mime string) bool {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".pdf", ".docx", ".txt", ".md", ".csv":
		return true
	}
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	switch mime {
	case "application/pdf",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return true
	}
	return false
}

// registerAgentTool wires read_attachment into the agent registry. The
// store + sender are resolved from the app at call time so startup
// init/configure ordering doesn't matter.
func registerAgentTool(r *tools.Registry, a *App) {
	for _, meta := range attachmentTools {
		r.Register(tools.Def{
			Name:          meta.Name,
			Description:   meta.Description,
			Schema:        meta.Schema,
			DefaultPolicy: tools.PolicyAllow,
			Handler: func(ec *tools.ExecContext, raw json.RawMessage) (string, error) {
				var inp struct {
					AttachmentID string `json:"attachment_id"`
					Instructions string `json:"instructions"`
				}
				if err := json.Unmarshal(raw, &inp); err != nil {
					return "", fmt.Errorf("parsing args: %w", err)
				}
				caller := ec.Caller()
				if caller == nil {
					return "", errors.New("no caller")
				}
				id, err := uuid.Parse(inp.AttachmentID)
				if err != nil {
					return "", fmt.Errorf("invalid attachment_id: %w", err)
				}
				return readAttachment(ec.Ctx, a.service(), a.sender(), caller.TenantID, id, inp.Instructions)
			},
		})
	}
}

func buildMCPTools(a *App) []mcpserver.ServerTool {
	var result []mcpserver.ServerTool
	for _, meta := range attachmentTools {
		result = append(result, apps.MCPToolFromMeta(meta, mcpReadAttachment(a)))
	}
	return result
}

func mcpReadAttachment(a *App) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		idStr, _ := req.RequireString("attachment_id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid attachment_id: %v", err)), nil
		}
		text, err := readAttachment(ctx, a.service(), a.sender(), caller.TenantID, id, req.GetString("instructions", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(text), nil
	})
}
