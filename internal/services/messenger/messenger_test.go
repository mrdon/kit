package messenger

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

// fakeSlack records OpenConversation + PostMessageReturningTS calls and
// returns canned channel ids and timestamps. Implements SlackPoster.
type fakeSlack struct {
	mu             sync.Mutex
	imChannel      string
	postedTS       string
	openCalls      int
	postCalls      int
	openErr        error
	postErr        error
	lastChannel    string
	lastThreadTS   string
	lastBody       string
	lastOpenedUser string
}

func (f *fakeSlack) OpenConversation(_ context.Context, userID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openCalls++
	f.lastOpenedUser = userID
	if f.openErr != nil {
		return "", f.openErr
	}
	return f.imChannel, nil
}

func (f *fakeSlack) PostMessageReturningTS(_ context.Context, channel, threadTS, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.postCalls++
	f.lastChannel = channel
	f.lastThreadTS = threadTS
	f.lastBody = text
	if f.postErr != nil {
		return "", f.postErr
	}
	return f.postedTS, nil
}

type fixture struct {
	pool   *pgxpool.Pool
	tenant *models.Tenant
	user   *models.User
	slack  *fakeSlack
	m      *Default
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_msgr_" + uuid.NewString()
	slug := models.SanitizeSlug("msgr-test-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "msgr-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	user, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_alice_"+uuid.NewString()[:8], "Alice", "")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}

	slack := &fakeSlack{imChannel: "D_im_" + uuid.NewString()[:8], postedTS: "1700000000.000100"}

	m := New(pool, nil)
	m.SlackClientFor = func(_ context.Context, _ uuid.UUID) (SlackPoster, error) {
		return slack, nil
	}

	return &fixture{pool: pool, tenant: tenant, user: user, slack: slack, m: m}
}

func TestSend_HappyPath(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	sent, err := fx.m.Send(ctx, SendRequest{
		TenantID:   fx.tenant.ID,
		Channel:    "slack",
		Recipient:  Recipient{SlackUserID: fx.user.SlackUserID},
		Body:       "Hi Alice — are any of these times OK?",
		Origin:     "coordination",
		OriginRef:  "participant-uuid",
		AwaitReply: true,
		UserID:     fx.user.ID,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sent.SessionID == uuid.Nil {
		t.Fatalf("expected session id, got zero")
	}
	if sent.ChannelMessageID != fx.slack.postedTS {
		t.Errorf("ChannelMessageID = %q, want %q", sent.ChannelMessageID, fx.slack.postedTS)
	}
	if fx.slack.openCalls != 1 {
		t.Errorf("OpenConversation calls = %d, want 1", fx.slack.openCalls)
	}
	if fx.slack.postCalls != 1 {
		t.Errorf("PostMessage calls = %d, want 1", fx.slack.postCalls)
	}
	if fx.slack.lastThreadTS != "" {
		t.Errorf("expected top-level DM (empty thread_ts), got %q", fx.slack.lastThreadTS)
	}

	// Session was created with thread_ts="" anchored to the IM channel.
	session, err := models.FindSessionByThread(ctx, fx.pool, fx.tenant.ID, fx.slack.imChannel, "")
	if err != nil {
		t.Fatalf("FindSessionByThread: %v", err)
	}
	if session == nil || session.ID != sent.SessionID {
		t.Fatalf("session not found for IM channel; got %v want id=%s", session, sent.SessionID)
	}

	// One message_sent event recorded with routing metadata.
	events, err := models.GetSessionEvents(ctx, fx.pool, fx.tenant.ID, sent.SessionID)
	if err != nil {
		t.Fatalf("GetSessionEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].EventType != models.EventTypeMessageSent {
		t.Errorf("event type = %q, want message_sent", events[0].EventType)
	}
}

func TestSend_ReusesSession(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	first, err := fx.m.Send(ctx, SendRequest{
		TenantID: fx.tenant.ID, Channel: "slack",
		Recipient: Recipient{SlackUserID: fx.user.SlackUserID},
		Body:      "first", Origin: "coordination", AwaitReply: true,
		UserID: fx.user.ID,
	})
	if err != nil {
		t.Fatalf("first Send: %v", err)
	}
	second, err := fx.m.Send(ctx, SendRequest{
		TenantID: fx.tenant.ID, Channel: "slack",
		Recipient: Recipient{SlackUserID: fx.user.SlackUserID},
		Body:      "second", Origin: "coordination", AwaitReply: true,
		UserID: fx.user.ID,
	})
	if err != nil {
		t.Fatalf("second Send: %v", err)
	}
	if first.SessionID != second.SessionID {
		t.Errorf("session reused expected; got %s vs %s", first.SessionID, second.SessionID)
	}
	events, _ := models.GetSessionEvents(ctx, fx.pool, fx.tenant.ID, first.SessionID)
	if len(events) != 2 {
		t.Errorf("events = %d, want 2", len(events))
	}
}

func TestSend_RequiresFields(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	cases := []struct {
		name string
		req  SendRequest
		want string
	}{
		{"missing TenantID", SendRequest{Channel: "slack", Body: "x", Origin: "coordination"}, "TenantID"},
		{"missing Origin", SendRequest{TenantID: fx.tenant.ID, Channel: "slack", Body: "x"}, "Origin"},
		{"missing Body", SendRequest{TenantID: fx.tenant.ID, Channel: "slack", Origin: "coordination"}, "Body"},
		{"unknown channel", SendRequest{TenantID: fx.tenant.ID, Channel: "telepathy", Body: "x", Origin: "coordination"}, "unsupported"},
		{"missing slack user", SendRequest{TenantID: fx.tenant.ID, Channel: "slack", Body: "x", Origin: "coordination"}, "SlackUserID"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := fx.m.Send(ctx, c.req)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.want)
			}
			if !contains(err.Error(), c.want) {
				t.Errorf("error %q does not contain %q", err.Error(), c.want)
			}
		})
	}
}

func TestDispatch_NoSession(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	handled, err := fx.m.Dispatch(ctx, InboundEvent{
		TenantID:       fx.tenant.ID,
		Channel:        "slack",
		SlackChannelID: "D_unknown",
		SlackUserID:    fx.user.SlackUserID,
		UserID:         fx.user.ID,
		Body:           "hello?",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if handled {
		t.Errorf("expected fall-through (handled=false) for unknown session")
	}
}

func TestDispatch_NoAwaitReplyFallthrough(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	// Send without AwaitReply.
	_, err := fx.m.Send(ctx, SendRequest{
		TenantID: fx.tenant.ID, Channel: "slack",
		Recipient: Recipient{SlackUserID: fx.user.SlackUserID},
		Body:      "FYI", Origin: "coordination", AwaitReply: false,
		UserID: fx.user.ID,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	called := false
	fx.m.RegisterReplyHandler("coordination", func(_ context.Context, _ InboundMessage, _ string) (bool, error) {
		called = true
		return true, nil
	})

	handled, err := fx.m.Dispatch(ctx, InboundEvent{
		TenantID:       fx.tenant.ID,
		Channel:        "slack",
		SlackChannelID: fx.slack.imChannel,
		SlackUserID:    fx.user.SlackUserID,
		UserID:         fx.user.ID,
		Body:           "thanks",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if handled {
		t.Errorf("expected fall-through; AwaitReply was false")
	}
	if called {
		t.Errorf("handler should not have been called")
	}
}

func TestDispatch_HandlerCalled(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	sent, err := fx.m.Send(ctx, SendRequest{
		TenantID: fx.tenant.ID, Channel: "slack",
		Recipient: Recipient{SlackUserID: fx.user.SlackUserID},
		Body:      "Hi Alice, are these slots OK?", Origin: "coordination",
		OriginRef: "participant-42", AwaitReply: true,
		UserID: fx.user.ID,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var gotMsg InboundMessage
	var gotRef string
	fx.m.RegisterReplyHandler("coordination", func(_ context.Context, msg InboundMessage, ref string) (bool, error) {
		gotMsg = msg
		gotRef = ref
		return true, nil
	})

	handled, err := fx.m.Dispatch(ctx, InboundEvent{
		TenantID:       fx.tenant.ID,
		Channel:        "slack",
		SlackChannelID: fx.slack.imChannel,
		SlackUserID:    fx.user.SlackUserID,
		UserID:         fx.user.ID,
		Body:           "Tuesday 10am works",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !handled {
		t.Fatalf("expected handled=true")
	}
	if gotRef != "participant-42" {
		t.Errorf("origin ref = %q, want participant-42", gotRef)
	}
	if gotMsg.Body != "Tuesday 10am works" {
		t.Errorf("body = %q", gotMsg.Body)
	}
	if gotMsg.SessionID != sent.SessionID {
		t.Errorf("session id mismatch; got %s want %s", gotMsg.SessionID, sent.SessionID)
	}

	// One outbound + one inbound event recorded.
	events, _ := models.GetSessionEvents(ctx, fx.pool, fx.tenant.ID, sent.SessionID)
	if len(events) != 2 {
		t.Errorf("events = %d, want 2 (sent + received)", len(events))
	}
}

func TestDispatch_HandlerFallthrough(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	_, err := fx.m.Send(ctx, SendRequest{
		TenantID: fx.tenant.ID, Channel: "slack",
		Recipient: Recipient{SlackUserID: fx.user.SlackUserID},
		Body:      "scheduling DM", Origin: "coordination", AwaitReply: true,
		UserID: fx.user.ID,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Handler claims by returning false (e.g. parser said "unrelated").
	fx.m.RegisterReplyHandler("coordination", func(_ context.Context, _ InboundMessage, _ string) (bool, error) {
		return false, nil
	})

	handled, err := fx.m.Dispatch(ctx, InboundEvent{
		TenantID:       fx.tenant.ID,
		Channel:        "slack",
		SlackChannelID: fx.slack.imChannel,
		SlackUserID:    fx.user.SlackUserID,
		UserID:         fx.user.ID,
		Body:           "what's the PTO policy?",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if handled {
		t.Errorf("expected fall-through when handler returns false")
	}
}

func TestDispatch_NoHandlerRegistered(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	_, err := fx.m.Send(ctx, SendRequest{
		TenantID: fx.tenant.ID, Channel: "slack",
		Recipient: Recipient{SlackUserID: fx.user.SlackUserID},
		Body:      "scheduling DM", Origin: "ghost-app", AwaitReply: true,
		UserID: fx.user.ID,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	handled, err := fx.m.Dispatch(ctx, InboundEvent{
		TenantID:       fx.tenant.ID,
		Channel:        "slack",
		SlackChannelID: fx.slack.imChannel,
		SlackUserID:    fx.user.SlackUserID,
		UserID:         fx.user.ID,
		Body:           "any reply",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if handled {
		t.Errorf("expected fall-through; no handler registered for ghost-app")
	}
}

func TestRegisterReplyHandler_Replaces(t *testing.T) {
	m := New(nil, nil)
	first := false
	second := false
	m.RegisterReplyHandler("foo", func(context.Context, InboundMessage, string) (bool, error) {
		first = true
		return true, nil
	})
	m.RegisterReplyHandler("foo", func(context.Context, InboundMessage, string) (bool, error) {
		second = true
		return true, nil
	})
	h, ok := m.handlerFor("foo")
	if !ok {
		t.Fatalf("expected handler registered")
	}
	_, _ = h(context.Background(), InboundMessage{}, "")
	if first {
		t.Errorf("first handler should have been replaced")
	}
	if !second {
		t.Errorf("second handler should be the active one")
	}
}

func TestSendSlack_PostError(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	fx.slack.postErr = errors.New("rate limited")

	_, err := fx.m.Send(ctx, SendRequest{
		TenantID: fx.tenant.ID, Channel: "slack",
		Recipient: Recipient{SlackUserID: fx.user.SlackUserID},
		Body:      "x", Origin: "coordination", AwaitReply: true,
		UserID: fx.user.ID,
	})
	if err == nil {
		t.Fatalf("expected error; got nil")
	}
}

// contains is a substring helper to avoid importing strings just for tests.
func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
