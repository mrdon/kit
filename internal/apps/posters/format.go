package posters

import (
	"errors"
	"fmt"
	"strings"
)

func errorsAs(err error, target any) bool { return errors.As(err, target) }

// FormatPoster renders a poster and its current version for a transcript.
func FormatPoster(p *Poster, v *Version) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (poster %s), %s\n", strOr(p.Title, "untitled"), p.ID, posterStatus(p))
	if p.EventID != nil {
		fmt.Fprintf(&b, "  event: %s\n", *p.EventID)
	}
	fmt.Fprintf(&b, "  current version: %s (%s, %s)\n", v.ID, v.Author, v.CreatedAt.Format("2 Jan 15:04"))
	if v.TemplateID != nil {
		fmt.Fprintf(&b, "  template: %s\n", *v.TemplateID)
	}
	if v.Ground != "" {
		fmt.Fprintf(&b, "  ground: %s, canvas: %s\n", v.Ground, v.Format)
	}
	if v.Content.Title != "" {
		fmt.Fprintf(&b, "  content: %s\n", jsonIndent(v.Content))
	}
	if len(v.Photos) > 0 {
		fmt.Fprintf(&b, "  photos: %s\n", jsonIndent(v.Photos))
	}
	if len(v.Problems) > 0 {
		fmt.Fprintf(&b, "  last problems: %s\n", strings.Join(v.Problems, "; "))
	}
	if v.Instruction != "" {
		fmt.Fprintf(&b, "  last change: %s\n", v.Instruction)
	}
	fmt.Fprintf(&b, "\nSource:\n```tsx\n%s\n```\n", v.Source)
	return b.String()
}

// FormatTemplate is one template as a listing entry.
func FormatTemplate(t *Template) string {
	var b strings.Builder
	kind := "tenant"
	if t.Builtin() {
		kind = "built-in"
		if t.Hidden {
			kind += ", hidden here"
		}
	}
	fmt.Fprintf(&b, "- %s (%s) %s, %s, origin %s", t.Name, t.ID, t.Status, kind, t.Origin)
	fmt.Fprintf(&b, "; photos %d to %d; needs %s", t.Meta.Photos.Min, t.Meta.Photos.Max, strings.Join(t.Meta.Needs, ", "))
	if t.Picks > 0 {
		fmt.Fprintf(&b, "; picked %d times", t.Picks)
	}
	if t.Description != "" {
		fmt.Fprintf(&b, "\n  %s", t.Description)
	}
	b.WriteString("\n")
	return b.String()
}

// FormatPhoto is one photo as a search hit.
func FormatPhoto(p *Photo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s (%s, %s/%s, focus %.2f,%.2f)", p.ID, p.Orientation, p.Folder, p.Filename, p.FocusX, p.FocusY)
	if p.Description != "" {
		fmt.Fprintf(&b, ": %s", p.Description)
	}
	if len(p.Tags) > 0 {
		fmt.Fprintf(&b, " [%s]", strings.Join(p.Tags, ", "))
	}
	if p.Notes != "" {
		fmt.Fprintf(&b, " (notes: %s)", p.Notes)
	}
	b.WriteString("\n")
	return b.String()
}
