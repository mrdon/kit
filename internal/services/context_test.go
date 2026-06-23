package services

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/skills"
	"github.com/mrdon/kit/internal/testdb"
)

// TestKnowledgeContextHidesBuiltinsForWidget is the regression test for the
// audit finding: the anonymous website widget sets HideBuiltinSkills, and the
// prompt-injection path (BuildKnowledgeContext) must honour it — otherwise
// Kit's own product docs leak into public answers. buildSkillCatalog marks
// every built-in line with a "[builtin]" prefix, so its absence proves none
// were injected.
func TestKnowledgeContextHidesBuiltinsForWidget(t *testing.T) {
	if len(skills.VisibleBuiltins(false)) == 0 {
		t.Skip("no non-admin builtin skills to assert on")
	}

	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_test_widget_" + uuid.NewString()
	slug := models.SanitizeSlug("test-widget-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "widget-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	const marker = "[builtin]"

	widget := BuildKnowledgeContext(ctx, pool, &Caller{
		TenantID:          tenant.ID,
		Roles:             []string{models.RoleMember},
		HideBuiltinSkills: true,
	}, tenant)
	if strings.Contains(widget, marker) {
		t.Errorf("widget knowledge context leaked built-in skills:\n%s", widget)
	}

	member := BuildKnowledgeContext(ctx, pool, &Caller{
		TenantID: tenant.ID,
		Roles:    []string{models.RoleMember},
	}, tenant)
	if !strings.Contains(member, marker) {
		t.Errorf("non-widget context should include built-in skills, got:\n%s", member)
	}
}
