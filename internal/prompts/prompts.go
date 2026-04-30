// Package prompts loads embedded text/template prompt files and renders
// them with caller-supplied data. It exists so substantial LLM-bound
// prose lives next to its package as a .tmpl file instead of inline
// backtick literals in *.go source.
//
// Convention: files are embedded at package scope via //go:embed and
// parsed once with MustParse. Files are named by role:
//
//	system_*.tmpl  — content destined for an anthropic.SystemBlock
//	user_*.tmpl    — content destined for a user-message body
//
// The template name passed to Render is the file's basename including
// the .tmpl extension (e.g. "system_draft_message.tmpl") — that's how
// text/template's ParseFS keys templates.
package prompts

import (
	"fmt"
	"io/fs"
	"strings"
	"text/template"
)

// MustParse parses every template in fsys matching glob into one set,
// keyed by file basename without extension. Panics on malformed
// templates so the failure surfaces at startup, not on first LLM call.
// missingkey=error makes a typo'd field reference fail loudly instead
// of silently rendering "<no value>" into a prompt.
func MustParse(fsys fs.FS, glob string) *template.Template {
	return template.Must(
		template.New("").Option("missingkey=error").ParseFS(fsys, glob),
	)
}

// Render executes the named template with data and returns the rendered
// string with leading/trailing whitespace trimmed.
func Render(set *template.Template, name string, data any) (string, error) {
	var buf strings.Builder
	if err := set.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("rendering %s: %w", name, err)
	}
	return strings.TrimSpace(buf.String()), nil
}
