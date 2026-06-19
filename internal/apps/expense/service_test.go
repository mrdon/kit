package expense

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// fixture spins up an isolated tenant with three users and two roles.
type fixture struct {
	pool   *pgxpool.Pool
	svc    *ExpenseService
	tenant *models.Tenant
	alice  *models.User // member only
	bob    *models.User // member + founders
	carol  *models.User // member only (an unrelated teammate)
	admin  *models.User // admin
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_exp_test_" + uuid.NewString()
	slug := models.SanitizeSlug("exp-test-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "exp-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	if _, err := models.CreateRole(ctx, pool, tenant.ID, models.RoleMember, "member"); err != nil {
		t.Fatalf("member role: %v", err)
	}
	if _, err := models.CreateRole(ctx, pool, tenant.ID, models.RoleAdmin, "admin"); err != nil {
		t.Fatalf("admin role: %v", err)
	}
	if _, err := models.CreateRole(ctx, pool, tenant.ID, "founders", "founders"); err != nil {
		t.Fatalf("founders role: %v", err)
	}
	alice, _ := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_alice", "Alice", "")
	bob, _ := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_bob", "Bob", "")
	carol, _ := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_carol", "Carol", "")
	admin, _ := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_admin", "Admin", "")
	for _, u := range []*models.User{alice, bob, carol, admin} {
		if err := models.AssignRole(ctx, pool, tenant.ID, u.ID, models.RoleMember); err != nil {
			t.Fatalf("assign member: %v", err)
		}
	}
	_ = models.AssignRole(ctx, pool, tenant.ID, admin.ID, models.RoleAdmin)
	_ = models.AssignRole(ctx, pool, tenant.ID, bob.ID, "founders")

	return &fixture{pool: pool, svc: &ExpenseService{pool: pool}, tenant: tenant, alice: alice, bob: bob, carol: carol, admin: admin}
}

func (f *fixture) caller(u *models.User) *services.Caller {
	ctx := context.Background()
	roleIDs, _ := models.GetUserRoleIDs(ctx, f.pool, f.tenant.ID, u.ID)
	roleNames, _ := models.GetUserRoleNames(ctx, f.pool, f.tenant.ID, u.ID)
	return &services.Caller{
		TenantID: f.tenant.ID,
		UserID:   u.ID,
		Identity: u.SlackUserID,
		Roles:    roleNames,
		RoleIDs:  roleIDs,
		IsAdmin:  slices.Contains(roleNames, models.RoleAdmin),
	}
}

// draftWithItem creates a draft report owned by alice's member role and adds
// one line item, returning the report id.
func (f *fixture) draftWithItem(t *testing.T) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	r, err := f.svc.Create(ctx, f.caller(f.alice), CreateInput{Title: "Trip"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.svc.AddItem(ctx, f.caller(f.alice), r.ID, AddItemInput{Vendor: "Hotel", AmountCents: 1000}); err != nil {
		t.Fatalf("add item: %v", err)
	}
	return r.ID
}

func TestLifecycleAndTotal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.draftWithItem(t)

	if _, err := f.svc.AddItem(ctx, f.caller(f.alice), id, AddItemInput{Vendor: "Taxi", AmountCents: 500}); err != nil {
		t.Fatalf("add second item: %v", err)
	}
	r, _, err := f.svc.Get(ctx, f.caller(f.alice), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if r.TotalCents != 1500 {
		t.Fatalf("total = %d, want 1500", r.TotalCents)
	}
	if len(r.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(r.Items))
	}

	if _, err := f.svc.SubmitReport(ctx, f.caller(f.alice), id); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// No policy configured → approval defaults to admins. bob (member) can't
	// even see it, let alone approve.
	if _, err := f.svc.ApproveReport(ctx, f.caller(f.bob), id); !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-admin default approver, got %v", err)
	}
	// admin approves and reimburses.
	r, err = f.svc.ApproveReport(ctx, f.caller(f.admin), id)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if r.Status != StatusApproved {
		t.Fatalf("status = %s, want approved", r.Status)
	}
	r, err = f.svc.MarkReimbursed(ctx, f.caller(f.admin), id)
	if err != nil {
		t.Fatalf("reimburse: %v", err)
	}
	if r.Status != StatusReimbursed {
		t.Fatalf("status = %s, want reimbursed", r.Status)
	}
}

// TestPolicyRoleApproval: with an approver-role policy, a member of that role
// (not the submitter) can approve, and the report is visible to them but not to
// unrelated users.
func TestPolicyRoleApproval(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.SetPolicy(ctx, f.caller(f.admin), SetPolicyInput{ApproverRole: "founders"}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	id := f.draftWithItem(t)
	if _, err := f.svc.SubmitReport(ctx, f.caller(f.alice), id); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// bob is in founders → can approve; visibility follows.
	if _, _, err := f.svc.Get(ctx, f.caller(f.bob), id); err != nil {
		t.Fatalf("approver should see report: %v", err)
	}
	// carol (member only, not the approver role, not submitter) must NOT see it.
	if _, _, err := f.svc.Get(ctx, f.caller(f.carol), id); !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("non-approver should not see report, got %v", err)
	}
	mine, err := f.svc.List(ctx, f.caller(f.carol), ReportFilters{})
	if err != nil {
		t.Fatalf("carol list: %v", err)
	}
	if len(mine) != 0 {
		t.Fatalf("carol should see no reports, got %d", len(mine))
	}
	r, err := f.svc.ApproveReport(ctx, f.caller(f.bob), id)
	if err != nil {
		t.Fatalf("founders approve: %v", err)
	}
	if r.Status != StatusApproved {
		t.Fatalf("status = %s, want approved", r.Status)
	}
}

// TestSetPolicyRequiresAdmin: non-admins can't change the policy.
func TestSetPolicyRequiresAdmin(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.SetPolicy(ctx, f.caller(f.alice), SetPolicyInput{ApproverRole: "founders"}); !errors.Is(err, services.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestSelfApprovalBlocked(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.draftWithItem(t)
	if _, err := f.svc.SubmitReport(ctx, f.caller(f.alice), id); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := f.svc.ApproveReport(ctx, f.caller(f.alice), id); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("expected ErrSelfApproval, got %v", err)
	}
}

func TestSubmitRequiresItems(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r, err := f.svc.Create(ctx, f.caller(f.alice), CreateInput{Title: "Empty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.svc.SubmitReport(ctx, f.caller(f.alice), r.ID); !errors.Is(err, ErrNoItems) {
		t.Fatalf("expected ErrNoItems, got %v", err)
	}
}

func TestItemsImmutableAfterSubmit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.draftWithItem(t)
	if _, err := f.svc.SubmitReport(ctx, f.caller(f.alice), id); err != nil {
		t.Fatalf("submit: %v", err)
	}
	_, err := f.svc.AddItem(ctx, f.caller(f.alice), id, AddItemInput{Vendor: "Late", AmountCents: 100})
	if !errors.Is(err, ErrNotEditable) {
		t.Fatalf("expected ErrNotEditable, got %v", err)
	}
}

func TestRejectThenReopen(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.draftWithItem(t)
	if _, err := f.svc.SubmitReport(ctx, f.caller(f.alice), id); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// Default approver is admins; admin rejects.
	r, err := f.svc.RejectReport(ctx, f.caller(f.admin), id, "missing receipt")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if r.Status != StatusRejected || r.RejectionReason != "missing receipt" {
		t.Fatalf("unexpected rejected report: %+v", r)
	}
	// Submitter reopens to fix, then can edit again.
	if _, err := f.svc.ReopenReport(ctx, f.caller(f.alice), id); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := f.svc.AddItem(ctx, f.caller(f.alice), id, AddItemInput{Vendor: "Fixed", AmountCents: 200}); err != nil {
		t.Fatalf("add after reopen: %v", err)
	}
}

func TestDeleteDraftAndGuards(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.draftWithItem(t)

	// Submitter deletes their draft → gone.
	if err := f.svc.Delete(ctx, f.caller(f.alice), id); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	if _, _, err := f.svc.Get(ctx, f.caller(f.alice), id); !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("report should be gone, got %v", err)
	}

	// A submitted report can't be deleted.
	id2 := f.draftWithItem(t)
	if _, err := f.svc.SubmitReport(ctx, f.caller(f.alice), id2); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := f.svc.Delete(ctx, f.caller(f.alice), id2); !errors.Is(err, ErrNotDeletable) {
		t.Fatalf("expected ErrNotDeletable, got %v", err)
	}
}

func TestTenantIsolation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.draftWithItem(t)
	// A caller from a different tenant must not see the report.
	foreign := &services.Caller{TenantID: uuid.New(), UserID: f.alice.ID}
	if _, _, err := f.svc.Get(ctx, foreign, id); !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("expected ErrNotFound across tenants, got %v", err)
	}
}

func TestNonRoleMemberCannotRead(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// bob owns a report in the founders role; alice (member only) can't read.
	r, err := f.svc.Create(ctx, f.caller(f.bob), CreateInput{Title: "Founders dinner", RoleName: "founders"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := f.svc.Get(ctx, f.caller(f.alice), r.ID); !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-member, got %v", err)
	}
	// Admin can read anything.
	if _, _, err := f.svc.Get(ctx, f.caller(f.admin), r.ID); err != nil {
		t.Fatalf("admin read: %v", err)
	}
}
