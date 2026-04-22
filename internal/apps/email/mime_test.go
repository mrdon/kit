package email

import (
	"strings"
	"testing"
)

func TestExtractPlainText_SinglePartPlain(t *testing.T) {
	raw := []byte("From: a@example.com\r\n" +
		"Subject: Hi\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Hello world.\r\n")
	got := extractPlainText(raw)
	if got != "Hello world." {
		t.Fatalf("got %q, want %q", got, "Hello world.")
	}
}

func TestExtractPlainText_MultipartAlternativePrefersPlain(t *testing.T) {
	raw := []byte("From: a@example.com\r\n" +
		"Subject: Hi\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=\"BOUND\"\r\n" +
		"\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Plain version.\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<p>HTML version.</p>\r\n" +
		"--BOUND--\r\n")
	got := extractPlainText(raw)
	if got != "Plain version." {
		t.Fatalf("got %q, want %q", got, "Plain version.")
	}
}

func TestExtractPlainText_HTMLOnlyStripsTags(t *testing.T) {
	raw := []byte("From: a@example.com\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<html><body><p>Hello <b>world</b>.</p><p>Second.</p></body></html>\r\n")
	got := extractPlainText(raw)
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "world") || !strings.Contains(got, "Second.") {
		t.Fatalf("expected stripped text to contain Hello/world/Second, got %q", got)
	}
	if strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Fatalf("tags should have been stripped, got %q", got)
	}
}

func TestExtractPlainText_DecodesQuotedPrintable(t *testing.T) {
	raw := []byte("From: a@example.com\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"\r\n" +
		"Hello =E2=98=83 snowman.\r\n")
	got := extractPlainText(raw)
	if !strings.Contains(got, "☃") {
		t.Fatalf("expected decoded snowman, got %q", got)
	}
}

func TestExtractPlainText_EmptyInput(t *testing.T) {
	if got := extractPlainText(nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestExtractPlainText_MalformedFallsBackToBody(t *testing.T) {
	// No Content-Type header; mail.CreateReader may still succeed but
	// with no walkable inline parts. Either way the fallback or the
	// reader should surface the body text.
	raw := []byte("garbage line\r\n\r\nactual body\r\n")
	got := extractPlainText(raw)
	if !strings.Contains(got, "actual body") {
		t.Fatalf("expected fallback to surface body, got %q", got)
	}
}

func TestSnippetFromBytes_UsesPlainText(t *testing.T) {
	raw := []byte("From: a@example.com\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=\"B\"\r\n" +
		"\r\n" +
		"--B\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Clean snippet.\r\n" +
		"--B--\r\n")
	got := snippetFromBytes(raw)
	if got != "Clean snippet." {
		t.Fatalf("got %q, want %q", got, "Clean snippet.")
	}
}
