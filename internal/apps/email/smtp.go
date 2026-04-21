package email

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/wneessen/go-mail"
	"github.com/yuin/goldmark"
)

// htmlTemplate wraps the sanitized HTML body with a minimal inlined-CSS
// shell. Most email clients strip <style> blocks, so inline styles on the
// <body> are the safest baseline.
const htmlTemplate = `<!doctype html>
<html>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif; font-size: 14px; line-height: 1.5; color: #222;">
%s
</body>
</html>`

// mdSanitizer is the HTML sanitizer applied to goldmark's output before it
// goes on the wire. UGCPolicy allows common formatting (links, lists,
// emphasis, blockquote) while stripping script/form/iframe/on* handlers
// and dangerous URL schemes. Initialized once; bluemonday policies are
// safe for concurrent use.
var mdSanitizer = bluemonday.UGCPolicy()

// renderHTML converts markdown to sanitized HTML wrapped in the email
// template. Returns empty string on empty input.
func renderHTML(md string) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := goldmark.Convert([]byte(md), &buf); err != nil {
		// Defensive: fall back to escaped plaintext so we still send
		// something sensible rather than error out of a send the user
		// already approved.
		return fmt.Sprintf(htmlTemplate, "<pre>"+mdSanitizer.Sanitize(md)+"</pre>")
	}
	clean := mdSanitizer.SanitizeBytes(buf.Bytes())
	return fmt.Sprintf(htmlTemplate, string(clean))
}

// renderText returns the markdown source as plain text. Markdown is
// legible in text-only clients (**bold**, bullet `-`, `[label](url)`), so
// v1 ships the raw source. If noise becomes a problem we can add a
// md→plain stripper without changing callers.
func renderText(md string) string {
	return md
}

// appendSignature returns body with the account signature appended after
// a blank line. Empty signature → body unchanged. The agent never sees
// or controls the signature; it's the user's configured disclosure +
// personal sign-off, stored on the integration row.
func appendSignature(body, signature string) string {
	sig := strings.TrimSpace(signature)
	if sig == "" {
		return body
	}
	trimmed := strings.TrimRight(body, "\n")
	if trimmed == "" {
		return sig
	}
	return trimmed + "\n\n" + sig
}

// smtpSend dials the account's SMTP host, authenticates, and delivers a
// multipart/alternative message (plain text + sanitized HTML, both from
// the same markdown body). Returns the Message-ID header assigned to the
// outgoing mail.
//
// UNEXPORTED: service.sendOnce is the only legitimate caller. See
// .claude/skills/gated-tools-guide.md on shadow-path discipline —
// send_email is a PolicyGate tool and must have exactly one entry point.
func smtpSend(ctx context.Context, acct *Account, args SendArgs) (string, error) {
	msg := mail.NewMsg()

	fromAddr := acct.EmailAddress
	if fromAddr == "" {
		fromAddr = acct.Username
	}
	if acct.FromName != "" {
		if err := msg.FromFormat(acct.FromName, fromAddr); err != nil {
			return "", fmt.Errorf("setting from: %w", err)
		}
	} else if err := msg.From(fromAddr); err != nil {
		return "", fmt.Errorf("setting from: %w", err)
	}

	if len(args.To) == 0 {
		return "", errors.New("at least one recipient required")
	}
	if err := msg.To(args.To...); err != nil {
		return "", fmt.Errorf("setting to: %w", err)
	}
	if len(args.Cc) > 0 {
		if err := msg.Cc(args.Cc...); err != nil {
			return "", fmt.Errorf("setting cc: %w", err)
		}
	}
	if len(args.Bcc) > 0 {
		if err := msg.Bcc(args.Bcc...); err != nil {
			return "", fmt.Errorf("setting bcc: %w", err)
		}
	}

	msg.Subject(args.Subject)
	if args.InReplyTo != "" {
		msg.SetGenHeader(mail.HeaderInReplyTo, args.InReplyTo)
	}
	if len(args.References) > 0 {
		msg.SetGenHeader(mail.HeaderReferences, strings.Join(args.References, " "))
	}

	msg.SetMessageID()
	body := appendSignature(args.Body, acct.Signature)
	msg.SetBodyString(mail.TypeTextPlain, renderText(body))
	if html := renderHTML(body); html != "" {
		msg.AddAlternativeString(mail.TypeTextHTML, html)
	}

	port := acct.SMTPPort
	if port == 0 {
		port = DefaultSMTPPort
	}
	username := acct.Username
	if username == "" {
		username = acct.EmailAddress
	}

	opts := []mail.Option{
		mail.WithPort(port),
		mail.WithUsername(username),
		mail.WithPassword(acct.Password),
	}

	// Resolve security mode. Explicit config wins; Auto falls back to the
	// well-known port mapping: 465 → implicit TLS, anything else →
	// STARTTLS-mandatory.
	sec := acct.SMTPSecurity
	if sec == SecurityAuto {
		if port == 465 {
			sec = SecurityTLS
		} else {
			sec = SecuritySTARTTLS
		}
	}

	switch sec { //nolint:exhaustive // SecurityAuto resolved above
	case SecurityTLS:
		opts = append(opts, mail.WithSSLPort(false), mail.WithSMTPAuth(mail.SMTPAuthPlain))
	case SecuritySTARTTLS:
		opts = append(opts, mail.WithTLSPortPolicy(mail.TLSMandatory), mail.WithSMTPAuth(mail.SMTPAuthPlain))
	case SecurityNone:
		// Plaintext SMTP — for self-hosted localhost + test servers only.
		// go-mail's auth mechanisms refuse to run over an unencrypted
		// connection unless explicitly allowed. Use PLAIN-NOENC or skip
		// auth entirely if no password is set.
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
		if acct.Password != "" {
			opts = append(opts, mail.WithSMTPAuth(mail.SMTPAuthPlainNoEnc))
		} else {
			opts = append(opts, mail.WithSMTPAuth(mail.SMTPAuthNoAuth))
		}
	default:
		return "", fmt.Errorf("unknown smtp security mode %q", sec)
	}

	client, err := mail.NewClient(acct.SMTPHost, opts...)
	if err != nil {
		return "", fmt.Errorf("smtp client: %w", err)
	}

	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return "", fmt.Errorf("smtp send: %w", err)
	}

	ids := msg.GetGenHeader(mail.HeaderMessageID)
	if len(ids) == 0 {
		return "", errors.New("smtp send: no Message-ID header on sent message")
	}
	return ids[0], nil
}
