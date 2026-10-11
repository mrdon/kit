package posters

import (
	"strings"
	"testing"
)

// Every key a chat prompt mentions must be present in its data: a missing
// one is a panic in the request, not a blank. Render with the full set
// and with the optional ones empty.
func TestChatPromptsRenderWithAndWithoutOptionalKeys(t *testing.T) {
	base := map[string]any{
		"Title": "Trivia", "PosterID": "p", "EventLine": "Trivia night", "Format": "portrait",
		"Grounds": "ink, paper", "Accents": "amber", "DisplayFont": "A", "TextFont": "B", "MonoFont": "C",
		"StockAllowed": false, "RenderBudget": chatRenderBudget, "TemplatePrompt": "",
	}
	out := mustRender("system_poster_chat.tmpl", base)
	if strings.Contains(out, "own guidance") {
		t.Fatal("an empty template prompt was rendered")
	}
	base["TemplatePrompt"] = "Lead with the photo."
	base["StockAllowed"] = true
	out = mustRender("system_poster_chat.tmpl", base)
	if !strings.Contains(out, "Lead with the photo.") || !strings.Contains(out, "find_stock_photos") {
		t.Fatalf("prompt missing optional parts:\n%s", out)
	}
	tmpl := map[string]any{
		"Name": "Pair", "TemplateID": "t", "Status": "draft", "Builtin": false, "Grounds": "ink", "Accents": "amber",
		"DisplayFont": "A", "TextFont": "B", "MonoFont": "C", "ImageInstructions": "look",
	}
	if out := mustRender("system_template_chat.tmpl", tmpl); !strings.Contains(out, "meta.prompt") && !strings.Contains(out, "`prompt`") {
		t.Fatalf("template chat prompt does not mention the prompt field:\n%s", out)
	}
}
