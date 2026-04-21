package email

import (
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
	To         []string `json:"to"`
	Cc         []string `json:"cc,omitempty"`
	Bcc        []string `json:"bcc,omitempty"`
	Subject    string   `json:"subject"`
	Body       string   `json:"body"`
	InReplyTo  string   `json:"in_reply_to,omitempty"`
	References []string `json:"references,omitempty"`
}
