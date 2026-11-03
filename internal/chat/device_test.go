package chat

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
	"github.com/mrdon/kit/internal/tools"
)

func deviceChatFixture(t *testing.T) (*tools.ExecContext, *models.Actor) {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()
	teamID := "T_devchat_" + uuid.NewString()
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "devchat-test", "enc-placeholder", models.SanitizeSlug("devchat-"+uuid.NewString(), teamID), nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM tenants WHERE id = $1", tenant.ID) })
	if _, err := models.GetOrCreateRole(ctx, pool, tenant.ID, models.RoleMember, "member"); err != nil {
		t.Fatalf("creating member role: %v", err)
	}
	device, err := models.CreateActor(ctx, pool, tenant.ID, models.ActorKindDevice, "Bar iPad", []string{"kit.chat"}, nil)
	if err != nil {
		t.Fatalf("creating device: %v", err)
	}
	if err := models.AssignActorRole(ctx, pool, tenant.ID, device.ID, models.RoleMember); err != nil {
		t.Fatalf("assigning role: %v", err)
	}
	ec := &tools.ExecContext{
		Ctx:     ctx,
		Pool:    pool,
		Tenant:  tenant,
		Session: &models.Session{ID: uuid.New(), SlackChannelID: SentinelChannel},
		Device:  device,
	}
	return ec, device
}

// A device chat acts as the device, with its roles, and the registry it
// gets can only look things up and reply: nothing at the screen can
// create, change or send.
func TestDeviceChatConfined(t *testing.T) {
	ec, device := deviceChatFixture(t)
	caller := ec.Caller()
	if caller.Kind != services.CallerDevice || caller.ActorID != device.ID || caller.IsAdmin {
		t.Fatalf("caller = %+v, want the device, not admin", caller)
	}
	if !slices.Equal(caller.Roles, []string{models.RoleMember}) || !caller.HideBuiltinSkills {
		t.Fatalf("caller roles = %v hideBuiltins = %v", caller.Roles, caller.HideBuiltinSkills)
	}

	reg := tools.NewRegistry(ec.Ctx, caller, false)
	reg.DropGatedTools()
	reg.RestrictToTools(DeviceAllowedTools)
	for _, d := range reg.DefinitionsFor(caller) {
		if !slices.Contains(DeviceAllowedTools, d.Name) {
			t.Errorf("registry exposes non-allowlisted tool %q", d.Name)
		}
	}
	emptyArgs, _ := json.Marshal(map[string]any{})
	for _, banned := range []string{"create_task", "save_memory", "send_email", "assign_role", "set_menu_86", "list_secrets"} {
		if _, err := reg.Execute(ec, banned, emptyArgs); err == nil {
			t.Errorf("banned tool %q should be refused", banned)
		}
	}
}

// The session has no user and records the device; a follow-up on the same
// sheet reuses it.
func TestDeviceChatSession(t *testing.T) {
	ec, device := deviceChatFixture(t)
	thread := "chat-device-" + device.ID.String() + "-s1"
	first, err := models.GetOrCreateDeviceSession(ec.Ctx, ec.Pool, ec.Tenant.ID, SentinelChannel, thread, device.Label)
	if err != nil {
		t.Fatalf("GetOrCreateDeviceSession: %v", err)
	}
	again, err := models.GetOrCreateDeviceSession(ec.Ctx, ec.Pool, ec.Tenant.ID, SentinelChannel, thread, device.Label)
	if err != nil {
		t.Fatalf("GetOrCreateDeviceSession again: %v", err)
	}
	if first.ID != again.ID {
		t.Fatalf("follow-up got a new session: %s vs %s", first.ID, again.ID)
	}
	var userID *uuid.UUID
	var kind, label string
	if err := ec.Pool.QueryRow(ec.Ctx, `SELECT user_id, actor_kind, actor_label FROM sessions WHERE tenant_id = $1 AND id = $2`,
		ec.Tenant.ID, first.ID).Scan(&userID, &kind, &label); err != nil {
		t.Fatalf("reading session: %v", err)
	}
	if userID != nil || kind != models.ActorKindDevice || label != "Bar iPad" {
		t.Fatalf("session user=%v kind=%q label=%q", userID, kind, label)
	}
}

func TestDeviceSystemPromptNamesTheDevice(t *testing.T) {
	ec, device := deviceChatFixture(t)
	prompt := agent.BuildDeviceSystemPrompt(ec.Ctx, ec.Pool, ec.Tenant, device)
	if !strings.Contains(prompt, `"Bar iPad"`) || !strings.Contains(prompt, "User roles: member (admin: false)") {
		t.Fatalf("prompt missing device label or roles:\n%s", prompt)
	}
}
