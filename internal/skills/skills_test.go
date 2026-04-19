package skills

import (
	"testing"
)

// TestBuiltinsIncludeBuilderAdminGuide verifies the builder admin guide is
// picked up from the embedded FS at init time.
func TestBuiltinsIncludeBuilderAdminGuide(t *testing.T) {
	s := GetBuiltin("builder-admin-guide")
	if s == nil {
		t.Fatalf("builder-admin-guide builtin not found")
	}
	if !s.AdminOnly {
		t.Fatalf("builder-admin-guide should be admin_only")
	}
	if s.Description == "" {
		t.Fatalf("builder-admin-guide has no description")
	}
	if len(s.Content) < 500 {
		t.Fatalf("builder-admin-guide content seems too short: %d chars", len(s.Content))
	}
}

// TestVisibleBuiltins_HidesAdminOnlyFromNonAdmins verifies admin-only
// builtins don't surface to non-admin callers while remaining visible to
// admins.
func TestVisibleBuiltins_HidesAdminOnlyFromNonAdmins(t *testing.T) {
	asAdmin := VisibleBuiltins(true)
	asUser := VisibleBuiltins(false)

	foundAdmin := false
	for _, s := range asAdmin {
		if s.Name == "builder-admin-guide" {
			foundAdmin = true
			break
		}
	}
	if !foundAdmin {
		t.Fatalf("admin should see builder-admin-guide")
	}

	for _, s := range asUser {
		if s.Name == "builder-admin-guide" {
			t.Fatalf("non-admin should NOT see admin-only builder-admin-guide")
		}
	}

	// User-guide (not admin-only) should remain visible to everyone.
	foundUserAsUser := false
	for _, s := range asUser {
		if s.Name == "user-guide" {
			foundUserAsUser = true
			break
		}
	}
	if !foundUserAsUser {
		t.Fatalf("non-admin should see public user-guide")
	}
}
