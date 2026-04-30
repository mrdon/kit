package voting

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/cards"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// DB-backed test exercising the resolve path end-to-end without the
// CardService — feeds a participant row by hand, calls
// resolveParticipantVoteCard, and verifies verdict + reason land in
// the DB.

func TestResolveParticipantVoteCard_Approve(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_vote_resolve_" + uuid.NewString()
	slug := models.SanitizeSlug("vote-resolve-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "vote-resolve-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	organizer, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_org_"+uuid.NewString()[:8], "Organizer", "")
	if err != nil {
		t.Fatalf("creating organizer: %v", err)
	}
	alice, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_alice_"+uuid.NewString()[:8], "Alice", "")
	if err != nil {
		t.Fatalf("creating alice: %v", err)
	}

	v := &Vote{
		TenantID:     tenant.ID,
		OrganizerID:  organizer.ID,
		Title:        "Q2 plan",
		ProposalText: "Ship X by end of quarter.",
		Status:       StatusActive,
		DeadlineAt:   time.Now().Add(48 * time.Hour),
	}
	if err := CreateVote(ctx, pool, v); err != nil {
		t.Fatalf("CreateVote: %v", err)
	}

	p := &Participant{
		TenantID:   tenant.ID,
		VoteID:     v.ID,
		Identifier: alice.SlackUserID,
		UserID:     &alice.ID,
	}
	if err := CreateParticipant(ctx, pool, p); err != nil {
		t.Fatalf("CreateParticipant: %v", err)
	}

	app := &VotingApp{pool: pool}
	app.engine = newEngine(pool, app)

	msg, err := app.resolveParticipantVoteCard(ctx, v, actionApprove, p.ID.String())
	if err != nil {
		t.Fatalf("resolveParticipantVoteCard: %v", err)
	}
	if msg == "" {
		t.Errorf("expected ack message")
	}

	got, err := GetParticipant(ctx, pool, tenant.ID, p.ID)
	if err != nil {
		t.Fatalf("GetParticipant: %v", err)
	}
	if got.Verdict != VerdictApprove {
		t.Errorf("verdict = %q, want %q", got.Verdict, VerdictApprove)
	}
	if got.Reason != "" {
		t.Errorf("reason = %q, want empty (no card-chat session)", got.Reason)
	}
	if got.RespondedAt == nil {
		t.Errorf("responded_at should be set")
	}
}

func TestResolveParticipantVoteCard_Idempotent(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_vote_idem_" + uuid.NewString()
	slug := models.SanitizeSlug("vote-idem-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "vote-idem-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	organizer, _ := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_org_"+uuid.NewString()[:8], "", "")
	alice, _ := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_alice_"+uuid.NewString()[:8], "", "")

	v := &Vote{
		TenantID:     tenant.ID,
		OrganizerID:  organizer.ID,
		Title:        "Test",
		ProposalText: "Test",
		Status:       StatusActive,
		DeadlineAt:   time.Now().Add(48 * time.Hour),
	}
	_ = CreateVote(ctx, pool, v)
	p := &Participant{
		TenantID:   tenant.ID,
		VoteID:     v.ID,
		Identifier: alice.SlackUserID,
		UserID:     &alice.ID,
	}
	_ = CreateParticipant(ctx, pool, p)

	app := &VotingApp{pool: pool}
	app.engine = newEngine(pool, app)

	// First resolve records the verdict.
	if _, err := app.resolveParticipantVoteCard(ctx, v, actionApprove, p.ID.String()); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	// Second resolve with a different action is a no-op (already
	// recorded) — we don't want late swipes to overwrite.
	msg, err := app.resolveParticipantVoteCard(ctx, v, actionObject, p.ID.String())
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if msg != "Vote already recorded." {
		t.Errorf("expected 'Vote already recorded.', got %q", msg)
	}

	got, _ := GetParticipant(ctx, pool, tenant.ID, p.ID)
	if got.Verdict != VerdictApprove {
		t.Errorf("verdict mutated to %q on second resolve, expected stays %q", got.Verdict, VerdictApprove)
	}
}

// TestUpdateParticipantVerdict_TOCTOU verifies the DB-level
// `WHERE verdict IS NULL` guard: a second call returns recorded=false
// instead of clobbering the first verdict. Closes the race window
// between two concurrent resolve callers (swipe + MCP).
func TestUpdateParticipantVerdict_TOCTOU(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_vote_toctou_" + uuid.NewString()
	slug := models.SanitizeSlug("vote-toctou-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "vote-toctou-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	organizer, _ := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_org_"+uuid.NewString()[:8], "", "")
	alice, _ := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_alice_"+uuid.NewString()[:8], "", "")

	v := &Vote{
		TenantID: tenant.ID, OrganizerID: organizer.ID,
		Title: "Test", ProposalText: "Test", Status: StatusActive,
		DeadlineAt: time.Now().Add(48 * time.Hour),
	}
	_ = CreateVote(ctx, pool, v)
	p := &Participant{TenantID: tenant.ID, VoteID: v.ID, Identifier: alice.SlackUserID, UserID: &alice.ID}
	_ = CreateParticipant(ctx, pool, p)

	first, err := UpdateParticipantVerdict(ctx, pool, tenant.ID, p.ID, VerdictApprove, "first", time.Now())
	if err != nil {
		t.Fatalf("first update: %v", err)
	}
	if !first {
		t.Errorf("first update should report recorded=true")
	}
	second, err := UpdateParticipantVerdict(ctx, pool, tenant.ID, p.ID, VerdictObject, "second", time.Now())
	if err != nil {
		t.Fatalf("second update: %v", err)
	}
	if second {
		t.Errorf("second update should report recorded=false (already had a verdict)")
	}
	got, _ := GetParticipant(ctx, pool, tenant.ID, p.ID)
	if got.Verdict != VerdictApprove {
		t.Errorf("verdict was clobbered: got %q, want %q", got.Verdict, VerdictApprove)
	}
	if got.Reason != "first" {
		t.Errorf("reason was clobbered: got %q, want %q", got.Reason, "first")
	}
}

// TestBroadcastVoteResult_Sanitizes is the load-bearing privacy
// guarantee: verbatim objection reasons land on the organizer's digest
// card, but the per-participant briefing must NOT contain them.
// Bob's secret reason should not appear in any briefing card body.
func TestBroadcastVoteResult_Sanitizes(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_vote_sanitize_" + uuid.NewString()
	slug := models.SanitizeSlug("vote-sanitize-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "vote-sanitize-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	organizer, _ := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_org_"+uuid.NewString()[:8], "Organizer", "")
	alice, _ := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_alice_"+uuid.NewString()[:8], "Alice", "")
	bob, _ := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_bob_"+uuid.NewString()[:8], "Bob", "")

	v := &Vote{
		TenantID:     tenant.ID,
		OrganizerID:  organizer.ID,
		Title:        "Adopt linter",
		ProposalText: "Bump golangci-lint",
		Status:       StatusActive,
		DeadlineAt:   time.Now().Add(48 * time.Hour),
	}
	if err := CreateVote(ctx, pool, v); err != nil {
		t.Fatalf("CreateVote: %v", err)
	}

	const secret = "EMBARRASSING_REASON_THAT_MUST_NOT_LEAK"
	pAlice := &Participant{TenantID: tenant.ID, VoteID: v.ID, Identifier: alice.SlackUserID, UserID: &alice.ID}
	pBob := &Participant{TenantID: tenant.ID, VoteID: v.ID, Identifier: bob.SlackUserID, UserID: &bob.ID}
	_ = CreateParticipant(ctx, pool, pAlice)
	_ = CreateParticipant(ctx, pool, pBob)
	_, _ = UpdateParticipantVerdict(ctx, pool, tenant.ID, pAlice.ID, VerdictApprove, "", time.Now())
	_, _ = UpdateParticipantVerdict(ctx, pool, tenant.ID, pBob.ID, VerdictObject, secret, time.Now())

	// Wire a real CardService so broadcastVoteResult writes briefings.
	cardSvc := cards.NewService(pool)
	app := &VotingApp{pool: pool, cards: cardSvc}
	app.engine = newEngine(pool, app)

	v.Outcome = &Outcome{Tally: Tally{Approve: 1, Object: 1}}
	if err := app.broadcastVoteResult(ctx, v, actionAcceptAndShare); err != nil {
		t.Fatalf("broadcastVoteResult: %v", err)
	}

	// Verify both participants got a briefing AND that the secret
	// reason isn't anywhere in any of those bodies.
	for _, u := range []*models.User{alice, bob} {
		caller := &services.Caller{TenantID: tenant.ID, UserID: u.ID, Identity: u.SlackUserID}
		list, err := cardSvc.ListBriefings(ctx, caller, cards.CardFilters{})
		if err != nil {
			t.Fatalf("ListBriefings for %s: %v", u.ID, err)
		}
		if len(list) != 1 {
			t.Fatalf("expected 1 briefing for %s, got %d", u.SlackUserID, len(list))
		}
		c := list[0]
		if strings.Contains(c.Body, secret) || strings.Contains(c.Title, secret) {
			t.Errorf("briefing for %s leaked the verbatim objection reason: title=%q body=%q",
				u.SlackUserID, c.Title, c.Body)
		}
		if !strings.Contains(c.Body, "accepted") {
			t.Errorf("briefing for %s missing outcome word: body=%q", u.SlackUserID, c.Body)
		}
	}
}
