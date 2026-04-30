package prompts_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mrdon/kit/internal/prompts"
)

func TestRender(t *testing.T) {
	fsys := fstest.MapFS{
		"system_greet.tmpl": &fstest.MapFile{
			Data: []byte("\n\nHello {{ .Name }}!\n\n"),
		},
		"user_static.tmpl": &fstest.MapFile{
			Data: []byte("static body\n"),
		},
	}
	set := prompts.MustParse(fsys, "*.tmpl")

	t.Run("renders with data and trims surrounding whitespace", func(t *testing.T) {
		got, err := prompts.Render(set, "system_greet.tmpl", map[string]any{"Name": "Kit"})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		want := "Hello Kit!"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("missing key fails loudly", func(t *testing.T) {
		_, err := prompts.Render(set, "system_greet.tmpl", map[string]any{"WrongField": "x"})
		if err == nil {
			t.Fatal("expected error for missing key, got nil")
		}
		if !strings.Contains(err.Error(), "rendering system_greet.tmpl") {
			t.Fatalf("error should wrap with template name: %v", err)
		}
	})

	t.Run("static template ignores data", func(t *testing.T) {
		got, err := prompts.Render(set, "user_static.tmpl", nil)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if got != "static body" {
			t.Fatalf("got %q, want %q", got, "static body")
		}
	})

	t.Run("unknown template name errors", func(t *testing.T) {
		_, err := prompts.Render(set, "missing", nil)
		if err == nil {
			t.Fatal("expected error for unknown template, got nil")
		}
	})
}

func TestMustParsePanicsOnMalformed(t *testing.T) {
	fsys := fstest.MapFS{
		"broken.tmpl": &fstest.MapFile{Data: []byte("{{ .Unclosed")},
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on malformed template, got none")
		}
	}()
	_ = prompts.MustParse(fsys, "*.tmpl")
}
