package tools

import (
	"context"
	"testing"

	"github.com/mrdon/kit/internal/models"
)

func TestClassifyTaskModel_NilClientFallsBackToHaiku(t *testing.T) {
	got := ClassifyTaskModel(context.Background(), nil, "draft personalized emails for every open todo")
	if got != models.JobModelHaiku {
		t.Fatalf("nil client should fall back to haiku, got %q", got)
	}
}

func TestClassifyTaskModel_EmptyDescriptionFallsBackToHaiku(t *testing.T) {
	got := ClassifyTaskModel(context.Background(), nil, "   ")
	if got != models.JobModelHaiku {
		t.Fatalf("empty description should fall back to haiku, got %q", got)
	}
}

func TestPreviewDescription(t *testing.T) {
	short := "already short"
	if got := previewDescription(short); got != short {
		t.Errorf("short: want %q, got %q", short, got)
	}
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	got := previewDescription(string(long))
	if n := len([]rune(got)); n != 120 {
		t.Errorf("truncated rune count: want 120 (119 chars + ellipsis), got %d", n)
	}
	if got[len(got)-len("…"):] != "…" {
		t.Errorf("truncated suffix: want ellipsis, got %q", got)
	}
}
