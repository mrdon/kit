package menu

import (
	"testing"
	"time"
)

func TestHappyHourAlertOncePerFailure(t *testing.T) {
	then := time.Now().Add(-time.Minute)
	never := &HappyHourState{}
	okOn := &HappyHourState{SyncedAt: &then, SyncOK: true, SyncedHash: "on:a"}
	failedOn := &HappyHourState{SyncedAt: &then, SyncedHash: "on:a"}
	failedOff := &HappyHourState{SyncedAt: &then, SyncedHash: "off"}

	cases := []struct {
		name string
		prev *HappyHourState
		key  string
		ok   bool
		want hhAlert
	}{
		{"first push fails", never, "on:a", false, hhAlertFailed},
		{"first push works", never, "on:a", true, hhAlertNone},
		{"working push breaks", okOn, "on:a", false, hhAlertFailed},
		{"retry of the same failure", failedOn, "on:a", false, hhAlertNone},
		{"changed setting still fails", failedOn, "on:b", false, hhAlertFailed},
		{"retry succeeds", failedOn, "on:a", true, hhAlertRecovered},
		{"fixed setting succeeds", failedOn, "on:b", true, hhAlertRecovered},
		{"happy hour ends while broken", failedOn, "off", true, hhAlertNone},
		{"removal finally works", failedOff, "off", true, hhAlertRecovered},
		{"still working", okOn, "on:a", true, hhAlertNone},
	}
	for _, c := range cases {
		if got := hhAlertFor(c.prev, c.key, c.ok); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
