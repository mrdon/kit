package cards

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/chat"
	"github.com/mrdon/kit/internal/services"
)

// A person passes the kit.chat capability check (it is member-level for
// people), but the device mounts must never run a person's turn as a
// device, or a device's turn without one.
func TestDeviceChatRefusesPeople(t *testing.T) {
	a := &CardsApp{chatLimiter: chat.NewExecuteLimiter()}
	for name, caller := range map[string]*services.Caller{
		"person": {Kind: services.CallerUser, TenantID: uuid.New(), UserID: uuid.New()},
		"none":   nil,
	} {
		req := httptest.NewRequest(http.MethodPost, "/x/api/device/chat/execute", strings.NewReader(`{"text":"hi","client_session_id":"s"}`))
		if caller != nil {
			req = req.WithContext(auth.WithCaller(req.Context(), caller))
		}
		rec := httptest.NewRecorder()
		a.handleDeviceChatExecute(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, rec.Code)
		}
	}
}

func TestDeviceChatLimitKey(t *testing.T) {
	actor, user := uuid.New(), uuid.New()
	if got := limitKey(&services.Caller{Kind: services.CallerDevice, ActorID: actor}); got != actor {
		t.Errorf("device key = %s, want actor id", got)
	}
	if got := limitKey(&services.Caller{Kind: services.CallerUser, UserID: user}); got != user {
		t.Errorf("person key = %s, want user id", got)
	}
}
