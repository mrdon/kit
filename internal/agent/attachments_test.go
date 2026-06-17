package agent

import (
	"strings"
	"testing"
)

func TestAttachmentManifest_Empty(t *testing.T) {
	if got := attachmentManifest(nil); got != "" {
		t.Errorf("empty refs should yield empty manifest, got %q", got)
	}
}

func TestAttachmentManifest(t *testing.T) {
	refs := []AttachmentRef{
		{ID: "abc", Filename: "receipt.jpg", Mime: "image/jpeg", Size: 245760},
		{ID: "def", Filename: "invoice.pdf", Mime: "application/pdf", Size: 1153434},
	}
	got := attachmentManifest(refs)
	for _, want := range []string{"read_attachment", "receipt.jpg", "image/jpeg", "id=abc", "invoice.pdf", "id=def"} {
		if !strings.Contains(got, want) {
			t.Errorf("manifest missing %q:\n%s", want, got)
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int]string{
		512:     "512 B",
		245760:  "240 KB",
		1153434: "1.1 MB",
	}
	for in, want := range cases {
		if got := humanSize(in); got != want {
			t.Errorf("humanSize(%d) = %q; want %q", in, got, want)
		}
	}
}
