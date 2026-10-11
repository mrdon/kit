package posters

import (
	"strings"
	"testing"
)

func TestApplyPatches(t *testing.T) {
	src := "a\nconst ground = \"paper\";\nb\nb\n"
	out, err := applyPatches(src, []patchEdit{{Find: `const ground = "paper";`, Replace: `const ground = "ink";`}})
	if err != nil || !strings.Contains(out, `"ink"`) || strings.Contains(out, `"paper"`) {
		t.Fatalf("single edit: %v %q", err, out)
	}
	// Edits apply in order, and the second sees the first's result.
	out, err = applyPatches(src, []patchEdit{{Find: "a\n", Replace: "A\n"}, {Find: "A\nconst", Replace: "A\n// note\nconst"}})
	if err != nil || !strings.HasPrefix(out, "A\n// note\nconst") {
		t.Fatalf("ordered edits: %v %q", err, out)
	}
	if _, err := applyPatches(src, []patchEdit{{Find: "b\n", Replace: "c\n"}}); err == nil || !strings.Contains(err.Error(), "appears 2 times") {
		t.Fatalf("ambiguous find not refused: %v", err)
	}
	if _, err := applyPatches(src, []patchEdit{{Find: "zzz", Replace: ""}}); err == nil || !strings.Contains(err.Error(), "not in the source") {
		t.Fatalf("missing find not refused: %v", err)
	}
	if _, err := applyPatches(src, nil); err == nil {
		t.Fatal("empty edits accepted")
	}
}
