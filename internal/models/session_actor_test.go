package models

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/testdb"
)

// A job's session names the job alongside its owner; a person's session
// names nobody but the person. Both read back through every session query.
func TestAgentSessionAttribution(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, userID := testTenantUser(t, ctx, pool)

	agent, err := CreateAgentSession(ctx, pool, tenantID, "C1", "job-"+uuid.NewString(), userID, "Morning briefing job")
	if err != nil {
		t.Fatal(err)
	}
	if agent.UserID != userID || !agent.BotInitiated || agent.ActorKind != ActorKindAgent || agent.ActorLabel != "Morning briefing job" {
		t.Fatalf("agent session = %+v", agent)
	}
	person, err := CreateSession(ctx, pool, tenantID, "C1", "t-"+uuid.NewString(), userID, false)
	if err != nil {
		t.Fatal(err)
	}
	if person.ActorKind != "" || person.ActorLabel != "" {
		t.Fatalf("person session carries an actor: %+v", person)
	}

	got, err := GetSession(ctx, pool, tenantID, agent.ID)
	if err != nil || got == nil || got.ActorLabel != "Morning briefing job" {
		t.Fatalf("GetSession = %+v, %v", got, err)
	}
	byThread, err := FindSessionByThread(ctx, pool, tenantID, "C1", agent.SlackThreadTS)
	if err != nil || byThread == nil || byThread.ActorKind != ActorKindAgent {
		t.Fatalf("FindSessionByThread = %+v, %v", byThread, err)
	}
	list, err := ListRecentSessionsForUser(ctx, pool, tenantID, userID, 10)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[uuid.UUID]string{}
	for _, s := range list {
		labels[s.ID] = s.ActorLabel
	}
	if labels[agent.ID] != "Morning briefing job" || labels[person.ID] != "" {
		t.Fatalf("listed labels = %v", labels)
	}
}
