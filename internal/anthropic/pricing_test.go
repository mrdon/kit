package anthropic

import (
	"math"
	"testing"
)

func TestEstimateCost(t *testing.T) {
	u := Usage{InputTokens: 10_000, OutputTokens: 1_000, CacheReadInputTokens: 20_000, CacheCreationInputTokens: 4_000}
	got, ok := EstimateCost("claude-sonnet-5-5", u)
	if !ok {
		t.Fatal("sonnet not priced")
	}
	// 10k*3 + 4k*3*1.25 + 20k*3*0.1 + 1k*15, per million.
	want := (10_000*3 + 4_000*3*1.25 + 20_000*3*0.1 + 1_000*15) / 1e6
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("cost = %v, want %v", got, want)
	}
	if _, ok := EstimateCost("something-else", u); ok {
		t.Fatal("unknown model priced")
	}
}
