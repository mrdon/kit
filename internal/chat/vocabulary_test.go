package chat

import (
	"strings"
	"testing"
)

func TestVocabularyHint(t *testing.T) {
	got := buildVocabularyHint([]string{"crowler-fill", "drawer-close-out"})
	if !strings.HasPrefix(got, hintLead+", ") || !strings.HasSuffix(got, "crowler fill, drawer close out.") {
		t.Fatalf("hint = %q", got)
	}

	many := make([]string, 200)
	for i := range many {
		many[i] = "some-long-skill-name"
	}
	long := buildVocabularyHint(many)
	if n := len([]rune(long)); n > hintMaxRunes+1 {
		t.Fatalf("hint is %d runes, cap %d", n, hintMaxRunes)
	}
	if !strings.HasPrefix(long, hintLead) {
		t.Fatalf("lead dropped: %q", long[:40])
	}
}
