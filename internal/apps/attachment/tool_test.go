package attachment

import (
	"strings"
	"testing"
)

func TestIsExtractable(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		mime     string
		raw      []byte
		want     bool
	}{
		{"plain text", "notes.txt", "text/plain", []byte("hello world"), true},
		{"json labelled application", "data.json", "application/json", []byte(`{"a":1}`), true},
		{"yaml no recognised mime", "config.yaml", "application/octet-stream", []byte("a: 1\nb: 2\n"), true},
		{"source code octet-stream", "main.go", "application/octet-stream", []byte("package main\n"), true},
		{"pdf by mime", "r.pdf", "application/pdf", []byte("%PDF-1.4 binary\x00stuff"), true},
		{"docx by extension", "r.docx", "application/octet-stream", []byte("PK\x03\x04"), true},
		{"binary with NUL", "blob.bin", "application/octet-stream", []byte("ab\x00cd"), false},
		{"empty", "empty.txt", "text/plain", nil, false},
		{"utf8 multibyte", "u.txt", "text/plain", []byte("café ☕ über"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isExtractable(tc.filename, tc.mime, tc.raw); got != tc.want {
				t.Fatalf("isExtractable(%q,%q,…) = %v, want %v", tc.filename, tc.mime, got, tc.want)
			}
		})
	}
}

func TestLooksLikeTextTruncatedMultibyte(t *testing.T) {
	// A long text blob whose sniff boundary lands mid-rune must still be
	// recognised as text (trailing partial rune trimmed before validation).
	body := strings.Repeat("a", 8190) + "☕" // multibyte rune straddles the 8192 cut
	if !looksLikeText([]byte(body)) {
		t.Fatal("expected text with rune straddling sniff boundary to be detected as text")
	}
}
