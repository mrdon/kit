package email

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"

	_ "github.com/emersion/go-message/charset" // register non-UTF-8 decoders
	"github.com/emersion/go-message/mail"
)

// extractPlainText parses a raw RFC822 message and returns the best
// human-readable plain-text body. It prefers text/plain parts; if none
// exist it strips tags from the text/html part. Attachments and other
// non-inline parts are ignored. On parse failure it falls back to a
// naive "strip outer headers" pass so callers always get *something*
// rather than an empty string for malformed messages.
func extractPlainText(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return fallbackBody(raw)
	}
	defer mr.Close()

	var plain, html strings.Builder
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			break
		}
		h, ok := p.Header.(*mail.InlineHeader)
		if !ok {
			continue
		}
		ct, _, _ := h.ContentType()
		body, err := io.ReadAll(p.Body)
		if err != nil {
			continue
		}
		switch ct {
		case "text/plain":
			if plain.Len() > 0 {
				plain.WriteByte('\n')
			}
			plain.Write(body)
		case "text/html":
			if html.Len() > 0 {
				html.WriteByte('\n')
			}
			html.Write(body)
		}
	}
	if plain.Len() > 0 {
		return strings.TrimSpace(plain.String())
	}
	if html.Len() > 0 {
		return strings.TrimSpace(stripHTML(html.String()))
	}
	return ""
}

var (
	htmlScriptStyleRE = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	htmlBlockRE       = regexp.MustCompile(`(?i)</?(p|div|br|li|tr|h[1-6])[^>]*>`)
	htmlTagRE         = regexp.MustCompile(`<[^>]+>`)
	htmlEntityRE      = regexp.MustCompile(`&(#\d+|[a-zA-Z]+);`)
)

// stripHTML turns HTML into approximate plain text. Not an HTML parser
// — it's a cheap pass good enough for giving the LLM a readable summary
// of an HTML-only marketing email.
func stripHTML(s string) string {
	s = htmlScriptStyleRE.ReplaceAllString(s, "")
	s = htmlBlockRE.ReplaceAllString(s, "\n")
	s = htmlTagRE.ReplaceAllString(s, "")
	s = htmlEntityRE.ReplaceAllStringFunc(s, decodeEntity)
	return s
}

func decodeEntity(e string) string {
	switch e {
	case "&nbsp;":
		return " "
	case "&amp;":
		return "&"
	case "&lt;":
		return "<"
	case "&gt;":
		return ">"
	case "&quot;":
		return "\""
	case "&apos;", "&#39;":
		return "'"
	}
	return ""
}

// fallbackBody is used when mail.CreateReader can't parse the input.
// Strips the first header block (up to the first blank line) and
// returns the rest, which is what `extractTextBody` used to do.
func fallbackBody(raw []byte) string {
	s := string(raw)
	if idx := strings.Index(s, "\r\n\r\n"); idx >= 0 {
		s = s[idx+4:]
	} else if idx := strings.Index(s, "\n\n"); idx >= 0 {
		s = s[idx+2:]
	}
	return strings.TrimSpace(s)
}
