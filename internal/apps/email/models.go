package email

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotConfigured is returned by LoadAccount when the current user has no
// email integration row. Agent handlers translate this into a friendly
// setup hint rather than a stack trace.
var ErrNotConfigured = errors.New("email account not configured")

// Provider + AuthType uniquely identify the email integration TypeSpec in
// the integrations registry.
const (
	Provider = "email"
	AuthType = "imap_smtp"
)

// Default ports used when the user's config doesn't override them. Kept
// here so service + client agree on the fallbacks.
const (
	DefaultIMAPPort = 993
	DefaultSMTPPort = 587
)

// Security is the wire-level encryption mode for an IMAP or SMTP dial.
// Empty string means "auto": pick based on the well-known port mapping
// (993/465 → TLS, 143/587 → STARTTLS).
type Security string

const (
	SecurityAuto     Security = ""         // heuristic by port
	SecurityTLS      Security = "tls"      // implicit TLS (IMAPS 993, SMTPS 465)
	SecuritySTARTTLS Security = "starttls" // plain connect, upgrade via STARTTLS
	SecurityNone     Security = "none"     // plaintext, no encryption — self-hosted localhost / test servers ONLY
)

// Account holds the decrypted credentials and endpoints for a single
// user's mailbox. The Password field carries plaintext only for the
// duration of an in-flight tool call; nothing serializes it back out.
type Account struct {
	EmailAddress string
	IMAPHost     string
	IMAPPort     int
	IMAPSecurity Security
	SMTPHost     string
	SMTPPort     int
	SMTPSecurity Security
	Username     string
	Password     string
	FromName     string
	// Signature is appended verbatim to every outgoing message body after
	// a blank line. Empty string means "no signature" — the setup form
	// defaults this to an AI-disclosure line with the user's first name,
	// and the user can edit or clear it.
	Signature string
}

// Summary is one row in a list/search result. Snippet is a short excerpt
// from the body (first ~200 chars, stripped of excessive whitespace).
type Summary struct {
	UID     uint32
	From    string
	Subject string
	Date    time.Time
	Snippet string
	Unread  bool
}

// Message is a fully-fetched message with body and attachment filenames.
type Message struct {
	UID         uint32
	From        string
	To          []string
	Cc          []string
	Subject     string
	Date        time.Time
	MessageID   string
	InReplyTo   string
	References  []string
	Body        string
	Attachments []string
}

// SendArgs is the input shape of the send_email tool.
type SendArgs struct {
	To         stringList `json:"to"`
	Cc         stringList `json:"cc,omitempty"`
	Bcc        stringList `json:"bcc,omitempty"`
	Subject    string     `json:"subject"`
	Body       string     `json:"body"`
	InReplyTo  string     `json:"in_reply_to,omitempty"`
	References stringList `json:"references,omitempty"`
}

// stringList accepts either a JSON array of strings or a single bare string.
// The send_email schema declares these fields as arrays, but models
// occasionally emit a single recipient as a plain string; coercing here
// avoids failing an already-approved send on a trivial shape mismatch.
type stringList []string

func (s *stringList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*s = nil
		return nil
	}
	if trimmed[0] == '[' {
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		*s = arr
		return nil
	}
	var one string
	if err := json.Unmarshal(data, &one); err != nil {
		return err
	}
	*s = []string{one}
	return nil
}
