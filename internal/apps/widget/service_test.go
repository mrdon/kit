package widget

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/tools"
)

// TestAllowedToolsConfined asserts that the widget's allowlist is the
// only thing the agent sees: every restricted tool is excluded from
// DefinitionsFor and refused by ExecuteWithResult. The build of the
// allowlist is the surface that gets reviewed for safety, so we lock
// it down with a test.
func TestAllowedToolsConfined(t *testing.T) {
	// The widget-mode caller defaults match Service.Chat — member role,
	// no admin, builtin + job filters on.
	ec := &tools.ExecContext{
		Ctx:    context.Background(),
		Tenant: &models.Tenant{ID: uuid.New(), Timezone: "UTC"},
		// Session is needed because the agent appends events on it;
		// but for these tests we never reach the handler, so a stub is
		// fine — registry filtering happens before any session use.
		Session:    &models.Session{ID: uuid.New(), SlackChannelID: models.WidgetChannelID},
		WidgetMode: true,
	}
	caller := ec.Caller()
	if caller.IsAdmin {
		t.Fatal("widget caller should never be admin")
	}
	if !slices.Contains(caller.Roles, models.RoleMember) {
		t.Fatal("widget caller must be in the member role")
	}
	if !caller.HideBuiltinSkills || !caller.HideJobReferencedSkills {
		t.Fatal("widget caller must hide both builtin and job-referenced skills")
	}

	reg := tools.NewRegistry(ec.Ctx, caller, false)
	reg.DropGatedTools()
	reg.RestrictToTools(AllowedTools)

	defs := reg.DefinitionsFor(caller)
	got := make(map[string]bool, len(defs))
	for _, d := range defs {
		got[d.Name] = true
	}

	// Every name in AllowedTools that was actually registered should
	// be visible. (calendar tools come from an app — if the app isn't
	// initialised in this test binary, those are silently absent.)
	for _, want := range AllowedTools {
		if got[want] {
			delete(got, want)
		}
	}
	if len(got) > 0 {
		t.Errorf("registry exposes non-allowlisted tools: %v", got)
	}

	// Spot-check the mutation tools we explicitly want out of reach:
	// even if a malicious prompt convinces the LLM to call them, the
	// registry rejects with "unknown tool" because RestrictToTools
	// deletes their handler from the dispatch map.
	emptyArgs, _ := json.Marshal(map[string]any{})
	for _, banned := range []string{"create_task", "resolve_decision", "save_memory", "create_widget_token"} {
		_, err := reg.Execute(ec, banned, emptyArgs)
		if err == nil {
			t.Errorf("banned tool %q should be refused; got nil error", banned)
		}
	}
}
