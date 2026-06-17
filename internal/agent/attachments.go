package agent

import (
	"fmt"
	"strings"
)

// AttachmentRef is the lightweight reference to a file the user attached to
// a turn. Bytes are never carried here — only enough metadata to build the
// manifest the model sees. The model fetches the actual contents via the
// read_attachment tool (internal/apps/attachment).
type AttachmentRef struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	Size     int    `json:"size"`
}

// attachmentManifest renders a cheap, human/LLM-readable list of attachments
// to append to the user message. It is intentionally text (not image bytes)
// so it persists in session history and replays for free; the model calls
// read_attachment to actually read any entry.
func attachmentManifest(refs []AttachmentRef) string {
	if len(refs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Attached files — call the read_attachment tool with an id to read one:")
	for _, r := range refs {
		fmt.Fprintf(&b, "\n- %s (%s, %s) id=%s", r.Filename, r.Mime, humanSize(r.Size), r.ID)
	}
	b.WriteString("]")
	return b.String()
}

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
