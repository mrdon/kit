package events

import (
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Trivia Night":                   "trivia-night",
		"Golden Mosaic is Back!":         "golden-mosaic-is-back",
		"  leading and trailing  ":       "leading-and-trailing",
		"Multiple---Hyphens":             "multiple-hyphens",
		"Live Music: The D'Arc Brothers": "live-music-the-d-arc-brothers",
		"2026 Anniversary Party":         "2026-anniversary-party",
		"UPPER CASE":                     "upper-case",
		"under_scores":                   "under-scores",
	} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// A slug becomes a public URL, so it must never contain anything that would
// need escaping.
func TestSlugifyProducesURLSafeOutput(t *testing.T) {
	for _, in := range []string{
		"Oktoberfest — Bräu & Prost!",
		"50% off?? #hashtag",
		"a/b\\c",
		"emoji 🍺 night",
		"tabs\tand\nnewlines",
	} {
		got := Slugify(in)
		for _, r := range got {
			isLower := r >= 'a' && r <= 'z'
			isDigit := r >= '0' && r <= '9'
			if !isLower && !isDigit && r != '-' {
				t.Errorf("Slugify(%q) = %q contains unsafe rune %q", in, got, r)
			}
		}
		if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
			t.Errorf("Slugify(%q) = %q has a stray edge hyphen", in, got)
		}
		if strings.Contains(got, "--") {
			t.Errorf("Slugify(%q) = %q has a doubled hyphen", in, got)
		}
	}
}

// Titles that carry no ASCII letters slugify to nothing; the caller must fall
// back rather than write an empty slug.
func TestSlugifyEmptyForNonLatinTitles(t *testing.T) {
	for _, in := range []string{"", "   ", "🍺🍺🍺", "日本語", "---", "!!!"} {
		if got := Slugify(in); got != "" {
			t.Errorf("Slugify(%q) = %q, want empty so the caller falls back", in, got)
		}
	}
}

func TestSlugifyCapsLengthAtWordBoundary(t *testing.T) {
	long := "The Annual Gravity Brewing Oktoberfest Celebration And Anniversary Party Extravaganza"
	got := Slugify(long)
	if len(got) > maxSlugLen {
		t.Fatalf("Slugify produced %d chars, cap is %d: %q", len(got), maxSlugLen, got)
	}
	if strings.HasSuffix(got, "-") {
		t.Errorf("truncation left a trailing hyphen: %q", got)
	}
	// Cutting at a word boundary means the last segment is a whole word.
	if !strings.HasPrefix(long, strings.ReplaceAll(got, "-", " ")[:len(got)-strings.Count(got, "-")]) {
		// Loose check: the slug should be a prefix of the title's words.
		if !strings.Contains(strings.ToLower(long), strings.ReplaceAll(got, "-", " ")) {
			t.Errorf("truncated slug %q is not a word-boundary prefix of the title", got)
		}
	}
}

func TestRandomSlugIsUsable(t *testing.T) {
	a, b := randomSlug(), randomSlug()
	if a == b {
		t.Fatal("randomSlug returned the same value twice")
	}
	for _, s := range []string{a, b} {
		if !strings.HasPrefix(s, "event") {
			t.Errorf("randomSlug() = %q, want an 'event' prefix", s)
		}
		if got := Slugify(s); got != s {
			t.Errorf("randomSlug() = %q is not already slug-safe (Slugify gives %q)", s, got)
		}
	}
}
