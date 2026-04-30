package voting

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// DB-backed test: Service.StartVote writes the vote + participant rows
// we expect, with no organizer auto-add (organizer is the asker, not a
// voter) and no NextNudgeAt-equivalent (voting doesn't nudge).

func TestService_StartVote_WritesRows(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_vote_" + uuid.NewString()
	slug := models.SanitizeSlug("vote-test-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "vote-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	organizer, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_org_"+uuid.NewString()[:8], "Organizer", "")
	if err != nil {
		t.Fatalf("creating organizer: %v", err)
	}

	// Empty CardService — service.StartVote will log a card-surface error
	// but won't fail; what we care about here is the row writes.
	app := &VotingApp{pool: pool}
	svc := newService(pool, app)

	caller := &services.Caller{TenantID: tenant.ID, UserID: organizer.ID, Identity: organizer.SlackUserID}

	v, err := svc.StartVote(ctx, caller, StartVoteInput{
		Title:        "Adopt new linter settings",
		ProposalText: "Bump golangci-lint to v1.62 and add the import-order check.",
		Participants: []string{"U_alice_X", "U_bob_X"},
	})
	if err != nil {
		t.Fatalf("StartVote: %v", err)
	}
	if v.ID == uuid.Nil {
		t.Fatalf("expected vote id")
	}
	if v.Status != StatusActive {
		t.Errorf("status = %s, want active", v.Status)
	}
	if v.DeadlineAt.IsZero() {
		t.Errorf("deadline_at should be set (default 48h)")
	}

	parts, err := ListParticipants(ctx, pool, tenant.ID, v.ID)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("participants = %d, want 2 (organizer is asker, not voter)", len(parts))
	}
	for _, p := range parts {
		if p.Verdict != "" {
			t.Errorf("participant %s: verdict = %q, want empty", p.Identifier, p.Verdict)
		}
		if p.UserID == nil {
			t.Errorf("participant %s: UserID should be auto-populated by ensureKitUser", p.Identifier)
		}
	}
}

func TestService_StartVote_RequiresParticipants(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_vote_" + uuid.NewString()
	slug := models.SanitizeSlug("vote-test-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "vote-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	organizer, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_org_"+uuid.NewString()[:8], "Organizer", "")
	if err != nil {
		t.Fatalf("creating organizer: %v", err)
	}

	app := &VotingApp{pool: pool}
	svc := newService(pool, app)
	caller := &services.Caller{TenantID: tenant.ID, UserID: organizer.ID, Identity: organizer.SlackUserID}

	_, err = svc.StartVote(ctx, caller, StartVoteInput{
		Title:        "Empty",
		ProposalText: "Nobody to vote.",
		Participants: []string{},
	})
	if err == nil {
		t.Fatalf("expected error for empty participant list")
	}

	// Filtering the organizer leaves zero — should also error.
	_, err = svc.StartVote(ctx, caller, StartVoteInput{
		Title:        "Self-vote",
		ProposalText: "Just me.",
		Participants: []string{organizer.SlackUserID},
	})
	if err == nil {
		t.Fatalf("expected error when only the organizer was passed")
	}
}
