package todo

import (
	"embed"
	"fmt"

	"github.com/mrdon/kit/internal/prompts"
)

//go:embed prompts/*.tmpl
var promptFS embed.FS

var promptSet = prompts.MustParse(promptFS, "prompts/*.tmpl")

func mustRender(name string, data any) string {
	out, err := prompts.Render(promptSet, name, data)
	if err != nil {
		panic(fmt.Errorf("todo: %w", err))
	}
	return out
}
