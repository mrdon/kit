package trivia

import "testing"

// Delete-all is the reset between nights, and it must be a reset for ONE
// workspace: two fixtures share the test database, so the other tenant's
// game is the canary.
func TestDeleteAllGamesClearsOnlyThisWorkspace(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	f.newGame(defaultSettings(), topicSet())
	f.newGame(defaultSettings(), topicSet())

	other := newFixture(t)
	other.seedBank(topicSet(), 4)
	keep := other.newGame(defaultSettings(), topicSet())

	n, err := DeleteAllGames(f.ctx, f.pool, f.tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("deleted %d games, want 2", n)
	}
	left, err := ListGames(f.ctx, f.pool, f.tenant.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("%d games survived a delete-all", len(left))
	}
	if _, err := GetGame(other.ctx, other.pool, other.tenant.ID, keep.ID); err != nil {
		t.Fatalf("the other workspace's game went with them: %v", err)
	}
}
