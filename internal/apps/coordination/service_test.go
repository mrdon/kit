package coordination

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// Service-level test: Service.Start writes the rows we expect, arms
// participants for the cron sweep, and returns a usable Coordination.

func TestService_Start_WritesRows(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_coord_" + uuid.NewString()
	slug := models.SanitizeSlug("coord-test-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "coord-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	organizer, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_org_"+uuid.NewString()[:8], "Organizer")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}

	app := &CoordinationApp{pool: pool}
	svc := newService(pool, app)

	caller := &services.Caller{TenantID: tenant.ID, UserID: organizer.ID, Identity: organizer.SlackUserID}

	startTime := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	endTime := startTime.Add(7 * 24 * time.Hour)
	slot1 := Slot{Start: startTime.Add(14 * time.Hour), End: startTime.Add(14*time.Hour + 30*time.Minute)}
	slot2 := Slot{Start: startTime.Add(38 * time.Hour), End: startTime.Add(38*time.Hour + 30*time.Minute)}

	coord, err := svc.Start(ctx, caller, StartInput{
		Title:           "Q2 review",
		DurationMinutes: 30,
		StartDate:       startTime,
		EndDate:         endTime,
		CandidateSlots:  []Slot{slot1, slot2},
		Participants:    []string{"U_alice_X", "U_bob_X"},
		AutoApprove:     true,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if coord.ID == uuid.Nil {
		t.Fatalf("expected coordination id")
	}
	if coord.Status != StatusActive {
		t.Errorf("status = %s, want active", coord.Status)
	}
	if coord.DeadlineAt == nil {
		t.Errorf("deadline_at should be set (default 7 days)")
	}

	parts, err := ListParticipants(ctx, pool, tenant.ID, coord.ID)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("participants = %d, want 2", len(parts))
	}
	for _, p := range parts {
		if p.Status != ParticipantPending {
			t.Errorf("participant %s status = %s, want pending", p.Identifier, p.Status)
		}
		if p.NextNudgeAt == nil {
			t.Errorf("participant %s should have next_nudge_at set so the cron sweep picks them up", p.Identifier)
		}
	}
}

func TestService_Start_ValidatesInput(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_coord_" + uuid.NewString()
	slug := models.SanitizeSlug("coord-test-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "coord-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	user, _ := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_x", "")
	app := &CoordinationApp{pool: pool}
	svc := newService(pool, app)
	caller := &services.Caller{TenantID: tenant.ID, UserID: user.ID}

	cases := []struct {
		name string
		in   StartInput
		want string
	}{
		{"no title", StartInput{DurationMinutes: 30, CandidateSlots: []Slot{{}}, Participants: []string{"a", "b"}}, "title"},
		{"no duration", StartInput{Title: "x", CandidateSlots: []Slot{{}}, Participants: []string{"a", "b"}}, "duration"},
		{"no slots", StartInput{Title: "x", DurationMinutes: 30, Participants: []string{"a", "b"}}, "candidate_slots"},
		{"no participants", StartInput{Title: "x", DurationMinutes: 30, CandidateSlots: []Slot{{}}, Participants: []string{}}, "at least one participant"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.Start(ctx, caller, c.in)
			if err == nil {
				t.Fatalf("expected error containing %q", c.want)
			}
		})
	}
}

func TestService_Cancel_RequiresOrganizer(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_coord_" + uuid.NewString()
	slug := models.SanitizeSlug("coord-test-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "coord-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	organizer, _ := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_org", "Org")
	other, _ := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_other", "Other")

	app := &CoordinationApp{pool: pool}
	svc := newService(pool, app)
	orgCaller := &services.Caller{TenantID: tenant.ID, UserID: organizer.ID}
	otherCaller := &services.Caller{TenantID: tenant.ID, UserID: other.ID}

	startTime := time.Now()
	coord, err := svc.Start(ctx, orgCaller, StartInput{
		Title: "x", DurationMinutes: 30,
		StartDate: startTime, EndDate: startTime.Add(7 * 24 * time.Hour),
		CandidateSlots: []Slot{{Start: startTime.Add(time.Hour), End: startTime.Add(2 * time.Hour)}},
		Participants:   []string{"a", "b"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Other user tries to cancel — should fail.
	if err := svc.Cancel(ctx, otherCaller, coord.ID); err == nil {
		t.Errorf("expected non-organizer to be blocked from cancelling")
	}

	// Organizer cancels — should succeed.
	if err := svc.Cancel(ctx, orgCaller, coord.ID); err != nil {
		t.Errorf("organizer cancel failed: %v", err)
	}

	got, _ := GetCoordination(ctx, pool, tenant.ID, coord.ID)
	if got == nil || got.Status != StatusCancelled {
		t.Errorf("expected status=cancelled; got %v", got)
	}
}
