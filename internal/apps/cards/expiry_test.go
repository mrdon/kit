// Tests for card expiry: the sweep that archives cards past expires_at, and
// the retention purge that deletes archived cards once they're old enough.
package cards

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

// newExpiryTenant creates an isolated tenant, which is also what each sweep
// is pointed at. Assertions still read back specific card ids rather than
// counting rows, so a scoping regression shows up as a wrong state rather
// than a wrong total.
func newExpiryTenant(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	team := "T_exp_" + uuid.NewString()
	slug := models.SanitizeSlug("exp-"+uuid.NewString(), team)
	tenant, err := models.UpsertTenant(ctx, pool, team, "expiry", "encrypted", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })
	return tenant.ID
}

// insertCardRow writes a bare app_cards row. It bypasses createCardTx because
// these tests care only about the parent row's state/expires_at/terminal_at,
// and the child rows would just be noise.
func insertCardRow(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID, state CardState, expiresAt, terminalAt *time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO app_cards (id, tenant_id, kind, title, body, state, expires_at, terminal_at)
		VALUES ($1, $2, 'briefing', 'test', '', $3, $4, $5)`,
		id, tenantID, state, expiresAt, terminalAt,
	)
	if err != nil {
		t.Fatalf("inserting card: %v", err)
	}
	return id
}

func cardState(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (CardState, bool) {
	t.Helper()
	var state CardState
	err := pool.QueryRow(context.Background(), `SELECT state FROM app_cards WHERE id = $1`, id).Scan(&state)
	if err != nil {
		return "", false
	}
	return state, true
}

func ptr(t time.Time) *time.Time { return &t }

// TestSweepExpiredCardsArchivesOnlyPastDuePending is the core contract: a
// pending card past its deadline is archived, and everything else is left
// exactly where it was.
func TestSweepExpiredCardsArchivesOnlyPastDuePending(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID := newExpiryTenant(t, pool)

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(24 * time.Hour)

	expired := insertCardRow(t, pool, tenantID, CardStatePending, &past, nil)
	notYetDue := insertCardRow(t, pool, tenantID, CardStatePending, &future, nil)
	noExpiry := insertCardRow(t, pool, tenantID, CardStatePending, nil, nil)
	// A gated tool is mid-flight here; expiry must not steal the card out
	// from under sweepStuckResolvingCards.
	resolving := insertCardRow(t, pool, tenantID, CardStateResolving, &past, nil)
	// Already terminal with a stale deadline: expiry must not overwrite a
	// real user outcome with 'archived'.
	dismissed := insertCardRow(t, pool, tenantID, CardStateDismissed, &past, ptr(time.Now()))

	if _, err := sweepExpiredCards(ctx, pool, tenantID); err != nil {
		t.Fatalf("sweepExpiredCards: %v", err)
	}

	for _, tc := range []struct {
		name string
		id   uuid.UUID
		want CardState
	}{
		{"past-due pending", expired, CardStateArchived},
		{"not yet due", notYetDue, CardStatePending},
		{"no expiry set", noExpiry, CardStatePending},
		{"resolving", resolving, CardStateResolving},
		{"already dismissed", dismissed, CardStateDismissed},
	} {
		got, ok := cardState(t, pool, tc.id)
		if !ok {
			t.Errorf("%s: card disappeared", tc.name)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: state = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestSweepExpiredCardsStampsTerminalAt guards the link between the two
// sweeps: the purge ages cards off terminal_at, so expiry has to set it or
// expired cards would never be reclaimed.
func TestSweepExpiredCardsStampsTerminalAt(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID := newExpiryTenant(t, pool)

	id := insertCardRow(t, pool, tenantID, CardStatePending, ptr(time.Now().Add(-time.Hour)), nil)
	if _, err := sweepExpiredCards(ctx, pool, tenantID); err != nil {
		t.Fatalf("sweepExpiredCards: %v", err)
	}

	var terminalAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT terminal_at FROM app_cards WHERE id = $1`, id).Scan(&terminalAt); err != nil {
		t.Fatalf("reading terminal_at: %v", err)
	}
	if terminalAt == nil {
		t.Fatal("terminal_at not stamped; the retention purge would never reclaim this card")
	}
}

// TestPurgeArchivedCardsRespectsRetentionAndState checks the purge deletes
// only archived cards past the retention window. Other terminal states are
// deliberate user outcomes and keep their audit value.
func TestPurgeArchivedCardsRespectsRetentionAndState(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID := newExpiryTenant(t, pool)

	old := time.Now().Add(-100 * 24 * time.Hour)
	recent := time.Now().Add(-1 * 24 * time.Hour)

	oldArchived := insertCardRow(t, pool, tenantID, CardStateArchived, nil, &old)
	recentArchived := insertCardRow(t, pool, tenantID, CardStateArchived, nil, &recent)
	oldDismissed := insertCardRow(t, pool, tenantID, CardStateDismissed, nil, &old)
	oldResolved := insertCardRow(t, pool, tenantID, CardStateResolved, nil, &old)

	if _, err := purgeArchivedCards(ctx, pool, tenantID, 90*24*time.Hour); err != nil {
		t.Fatalf("purgeArchivedCards: %v", err)
	}

	if _, ok := cardState(t, pool, oldArchived); ok {
		t.Error("archived card past retention should have been deleted")
	}
	for _, tc := range []struct {
		name string
		id   uuid.UUID
	}{
		{"archived within retention", recentArchived},
		{"dismissed past retention", oldDismissed},
		{"resolved past retention", oldResolved},
	} {
		if _, ok := cardState(t, pool, tc.id); !ok {
			t.Errorf("%s should have survived the purge", tc.name)
		}
	}
}

// TestPurgeArchivedCardsFallsBackToUpdatedAt covers rows predating the
// terminal_at stamp. Without the COALESCE they'd have a NULL age and never
// satisfy the cutoff, making them immortal.
func TestPurgeArchivedCardsFallsBackToUpdatedAt(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID := newExpiryTenant(t, pool)

	id := insertCardRow(t, pool, tenantID, CardStateArchived, nil, nil)
	if _, err := pool.Exec(ctx, `UPDATE app_cards SET updated_at = $2 WHERE id = $1`, id, time.Now().Add(-100*24*time.Hour)); err != nil {
		t.Fatalf("backdating updated_at: %v", err)
	}

	if _, err := purgeArchivedCards(ctx, pool, tenantID, 90*24*time.Hour); err != nil {
		t.Fatalf("purgeArchivedCards: %v", err)
	}
	if _, ok := cardState(t, pool, id); ok {
		t.Error("archived card with NULL terminal_at should age off updated_at")
	}
}

// TestExpiryFromTTLDays covers the days-to-deadline conversion, including
// the non-positive case that means "no expiry".
func TestExpiryFromTTLDays(t *testing.T) {
	if got := ExpiryFromTTLDays(0); got != nil {
		t.Errorf("ttl 0 = %v, want nil", got)
	}
	if got := ExpiryFromTTLDays(-3); got != nil {
		t.Errorf("negative ttl = %v, want nil", got)
	}
	got := ExpiryFromTTLDays(3)
	if got == nil {
		t.Fatal("ttl 3 returned nil")
	}
	want := time.Now().Add(72 * time.Hour)
	if diff := got.Sub(want); diff > time.Minute || diff < -time.Minute {
		t.Errorf("ttl 3 = %v, want within a minute of %v", got, want)
	}
	// Fractional days matter: a same-day card wants hours, not days.
	half := ExpiryFromTTLDays(0.5)
	if half == nil {
		t.Fatal("ttl 0.5 returned nil")
	}
	if diff := half.Sub(time.Now().Add(12 * time.Hour)); diff > time.Minute || diff < -time.Minute {
		t.Errorf("ttl 0.5 = %v, want ~12h out", half)
	}
}

// TestApplyTTLDaysTriState pins the three-way distinction the nullable
// column needs: leave alone, set, and clear.
func TestApplyTTLDaysTriState(t *testing.T) {
	var u CardUpdates
	applyTTLDays(&u, nil)
	if u.ExpiresAt != nil || u.ClearExpiresAt {
		t.Error("nil ttl should leave expiry untouched")
	}

	u = CardUpdates{}
	days := 3.0
	applyTTLDays(&u, &days)
	if u.ExpiresAt == nil || u.ClearExpiresAt {
		t.Error("positive ttl should set ExpiresAt and not clear")
	}

	u = CardUpdates{}
	zero := 0.0
	applyTTLDays(&u, &zero)
	if !u.ClearExpiresAt || u.ExpiresAt != nil {
		t.Error("zero ttl should clear expiry")
	}
}

// TestInfoBriefingsGetDefaultExpiry pins the rule that keeps the stack from
// filling with disposable progress notes: an info briefing that names no
// shelf life gets one anyway, and nothing else does. The three negative
// cases are the ways an author says "this one stays" — pick a severity above
// info, or name your own deadline.
func TestInfoBriefingsGetDefaultExpiry(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID := newExpiryTenant(t, pool)
	svc := NewService(pool)

	explicit := time.Now().Add(30 * 24 * time.Hour)
	cases := []struct {
		name     string
		severity BriefingSeverity
		expires  *time.Time
		want     func(*time.Time) bool
		describe string
	}{
		{
			name:     "info with no ttl gets the default",
			severity: BriefingSeverityInfo,
			want: func(got *time.Time) bool {
				if got == nil {
					return false
				}
				// Wide tolerance: the point is "roughly three days out",
				// not a timestamp the test computed twice.
				delta := time.Until(*got) - DefaultInfoBriefingTTL
				return delta > -time.Minute && delta < time.Minute
			},
			describe: "a deadline about " + DefaultInfoBriefingTTL.String() + " out",
		},
		{
			name:     "notable stays until acked",
			severity: BriefingSeverityNotable,
			want:     func(got *time.Time) bool { return got == nil },
			describe: "no deadline",
		},
		{
			name:     "important stays until acked",
			severity: BriefingSeverityImportant,
			want:     func(got *time.Time) bool { return got == nil },
			describe: "no deadline",
		},
		{
			name:     "an explicit ttl wins over the default",
			severity: BriefingSeverityInfo,
			expires:  &explicit,
			want: func(got *time.Time) bool {
				return got != nil && got.Sub(explicit).Abs() < time.Second
			},
			describe: "the caller's own deadline",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card, err := svc.CreateSystemBriefing(ctx, tenantID, CardCreateInput{
				Title:     tc.name,
				Body:      "body",
				ExpiresAt: tc.expires,
				Briefing:  &BriefingCreateInput{Severity: tc.severity},
			})
			if err != nil {
				t.Fatalf("CreateSystemBriefing: %v", err)
			}
			if !tc.want(card.ExpiresAt) {
				t.Fatalf("expected %s, got expires_at=%v", tc.describe, card.ExpiresAt)
			}
		})
	}
}

// TestDefaultExpiryLandsInTheSweep closes the loop: the default deadline is
// worth nothing unless the existing sweep acts on it. An info briefing dated
// into the past leaves the stack without anyone touching it.
func TestDefaultExpiryLandsInTheSweep(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID := newExpiryTenant(t, pool)
	svc := NewService(pool)

	card, err := svc.CreateSystemBriefing(ctx, tenantID, CardCreateInput{
		Title:    "created 3 tasks",
		Body:     "routine progress note",
		Briefing: &BriefingCreateInput{Severity: BriefingSeverityInfo},
	})
	if err != nil {
		t.Fatalf("CreateSystemBriefing: %v", err)
	}
	// Age it past its own deadline rather than waiting three days.
	past := time.Now().Add(-time.Hour)
	if _, err := pool.Exec(ctx,
		`UPDATE app_cards SET expires_at = $1 WHERE tenant_id = $2 AND id = $3`,
		past, tenantID, card.ID); err != nil {
		t.Fatalf("ageing card: %v", err)
	}

	if _, err := sweepExpiredCards(ctx, pool, tenantID); err != nil {
		t.Fatalf("sweepExpiredCards: %v", err)
	}
	state, ok := cardState(t, pool, card.ID)
	if !ok {
		t.Fatalf("card disappeared instead of being archived")
	}
	if state != CardStateArchived {
		t.Fatalf("expected archived, got %s", state)
	}
}
