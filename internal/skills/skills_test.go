package skills

import (
	"slices"
	"testing"
)

// withBuiltins swaps the package-level cache for the duration of a test.
// The visibility rules are what's under test, not which skills happen to
// ship — so the fixtures are synthetic rather than a real builtin.
func withBuiltins(t *testing.T, s []BuiltinSkill) {
	t.Helper()
	prev := builtinCache
	builtinCache = s
	t.Cleanup(func() { builtinCache = prev })
}

// TestBuiltinsDiscovered verifies the embedded FS is scanned at init time
// and yields usable skills — a name, a description, and real content.
func TestBuiltinsDiscovered(t *testing.T) {
	s := GetBuiltin("user-guide")
	if s == nil {
		t.Fatal("user-guide builtin not found")
	}
	if s.Description == "" {
		t.Error("user-guide has no description")
	}
	if len(s.Content) < 500 {
		t.Errorf("user-guide content seems too short: %d chars", len(s.Content))
	}
}

// TestVisibleBuiltins_HidesAdminOnlyFromNonAdmins verifies admin-only
// builtins don't surface to non-admin callers while remaining visible to
// admins, and that public ones stay visible to both.
func TestVisibleBuiltins_HidesAdminOnlyFromNonAdmins(t *testing.T) {
	withBuiltins(t, []BuiltinSkill{
		{Name: "secret", Description: "admin eyes only", Content: "x", AdminOnly: true},
		{Name: "public", Description: "everyone", Content: "x"},
	})

	asAdmin := names(VisibleBuiltins(true))
	if !slices.Contains(asAdmin, "secret") || !slices.Contains(asAdmin, "public") {
		t.Errorf("admin should see both, got %v", asAdmin)
	}

	asUser := names(VisibleBuiltins(false))
	if slices.Contains(asUser, "secret") {
		t.Errorf("non-admin should NOT see admin-only skill, got %v", asUser)
	}
	if !slices.Contains(asUser, "public") {
		t.Errorf("non-admin should see public skill, got %v", asUser)
	}
}

// TestVisibleMatchBuiltins_AppliesAdminFilter verifies search results go
// through the same visibility rule as the full listing.
func TestVisibleMatchBuiltins_AppliesAdminFilter(t *testing.T) {
	withBuiltins(t, []BuiltinSkill{
		{Name: "secret-report", Description: "admin eyes only", Content: "x", AdminOnly: true},
		{Name: "public-report", Description: "everyone", Content: "x"},
	})

	if got := names(VisibleMatchBuiltins("report", true)); len(got) != 2 {
		t.Errorf("admin search = %v, want both", got)
	}
	got := names(VisibleMatchBuiltins("report", false))
	if len(got) != 1 || got[0] != "public-report" {
		t.Errorf("non-admin search = %v, want [public-report]", got)
	}
}

func names(list []BuiltinSkill) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.Name)
	}
	return out
}
