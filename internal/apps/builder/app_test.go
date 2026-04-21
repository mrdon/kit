package builder

import (
	"context"
	"testing"

	"github.com/mrdon/kit/internal/apps"
)

// TestAppRegistered verifies that importing the builder package triggers
// self-registration, so cmd/kit's blank-import is enough to make the app
// appear in apps.All().
func TestAppRegistered(t *testing.T) {
	var found bool
	for _, a := range apps.All() {
		if a.Name() == "builder" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("builder app not present in apps.All(); got %d apps", len(apps.All()))
	}
}

// TestAppPhase4Contract pins the Phase 4 surface: builder now exposes a
// stable set of meta-tools (create_app, list_apps, get_app, delete_app,
// purge_app_data) with AdminOnly=true. Later subtasks will extend this list;
// this test asserts only that every tool is marked admin-only so no future
// wiring accidentally exposes a meta-tool to non-admins.
func TestAppPhase4Contract(t *testing.T) {
	a := &App{}
	if got := a.Name(); got != "builder" {
		t.Errorf("Name() = %q, want %q", got, "builder")
	}
	if got := a.SystemPrompt(); got != "" {
		t.Errorf("SystemPrompt() = %q, want empty", got)
	}
	metas := a.ToolMetas()
	if len(metas) == 0 {
		t.Fatal("ToolMetas() returned empty, want Phase 4 meta-tools")
	}
	for _, m := range metas {
		if !m.AdminOnly {
			t.Errorf("meta-tool %q has AdminOnly=false; all meta-tools must be admin-only", m.Name)
		}
	}
	if jobs := a.CronJobs(); jobs != nil {
		t.Errorf("CronJobs() = %v, want nil", jobs)
	}
	// RegisterAgentTools and RegisterRoutes must not panic on nil args or
	// for a non-admin caller.
	a.RegisterAgentTools(context.TODO(), nil, nil, false)
	a.RegisterRoutes(nil)
}

// TestAppMetaToolNames locks the Phase 4a + 4b meta-tool list so later
// subtasks notice if one gets dropped by accident. 4c/4d/4e will extend.
func TestAppMetaToolNames(t *testing.T) {
	want := map[string]bool{
		"create_app":          false,
		"list_apps":           false,
		"get_app":             false,
		"delete_app":          false,
		"purge_app_data":      false,
		"create_script":       false,
		"update_script":       false,
		"list_scripts":        false,
		"get_script":          false,
		"run_script":          false,
		"rollback_script_run": false,
		"script_logs":         false,
		"script_stats":        false,
	}
	for _, m := range (&App{}).ToolMetas() {
		if _, ok := want[m.Name]; ok {
			want[m.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing meta-tool %q", name)
		}
	}
}
