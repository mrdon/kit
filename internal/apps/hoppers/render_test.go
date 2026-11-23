package hoppers

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The TV script is assembled from the shared ES modules and the TV's own
// files. If node is around, prove the result parses: a syntax error here is
// a blank wall, and nothing else would notice before a bar did.
func TestDisplayScriptParses(t *testing.T) {
	js, err := displayJS()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(js, "\nexport ") || strings.Contains(js, "\nimport ") {
		t.Fatal("module syntax survived flattening")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	path := filepath.Join(t.TempDir(), "tv.js")
	if err := os.WriteFile(path, []byte(js), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("the TV script does not parse:\n%s", out)
	}
}

func TestFlattenModuleRefusesWhatItCannotFlatten(t *testing.T) {
	for _, src := range []string{"export default 1;\n", "export { a };\n", "const a = 1;\n  export const b = 2;\n"} {
		if _, err := flattenModule(src); err == nil {
			t.Errorf("%q flattened without complaint", src)
		}
	}
	got, err := flattenModule("import { a } from './a.js';\nexport function f() {}\nexport const x = 1;\n")
	if err != nil || got != "function f() {}\nconst x = 1;\n" {
		t.Fatalf("flattenModule = %q, %v", got, err)
	}
}
