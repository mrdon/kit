package task

import "testing"

func TestValidPriority(t *testing.T) {
	// The storable set is exactly migration 058's CHECK constraint.
	for _, p := range []string{PriorityBlocker, PriorityHigh, PriorityNormal} {
		if !ValidPriority(p) {
			t.Errorf("ValidPriority(%q) = false, want true", p)
		}
	}
	// The pre-058 scale (and any typo) must be rejected so a bad value
	// surfaces as a clean 400 instead of a raw DB CHECK violation (500).
	for _, p := range []string{"low", "medium", "urgent", "", "High", "blocker "} {
		if ValidPriority(p) {
			t.Errorf("ValidPriority(%q) = true, want false", p)
		}
	}
}

func TestDefaultPriorityIsValid(t *testing.T) {
	if !ValidPriority(DefaultPriority) {
		t.Fatalf("DefaultPriority %q is not in the valid set", DefaultPriority)
	}
}
