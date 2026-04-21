package email

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrdon/kit/internal/tools"
	"github.com/mrdon/kit/internal/tools/approval"
)

func registerEmailAgentTools(r *tools.Registry, a *App) {
	for _, meta := range emailTools {
		def := tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     emailAgentHandler(meta.Name, a),
		}
		if meta.Name == "send_email" {
			def.DefaultPolicy = tools.PolicyGate
			def.GateCardPreview = sendEmailGatePreview
		}
		r.Register(def)
	}
}

// sendEmailGatePreview turns a send_email tool call into the human
// framing on the approval card. The detailed email contents are
// rendered by SendEmailPreview on the card option itself, so the card
// title + body focus on the "who/why" and the option labels swap
// "Approve/Skip" for the verbs the user expects.
func sendEmailGatePreview(input json.RawMessage) tools.GateCardPreview {
	// Intentionally empty Body: the tool preview on the card face
	// already shows To/Subject/body, so an extra "Kit drafted this
	// email" paragraph just eats vertical space above the preview.
	preview := tools.GateCardPreview{
		Title:        "Send email?",
		ApproveLabel: "Send",
		SkipLabel:    "Don't send",
	}
	var args SendArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return preview
	}
	switch {
	case args.Subject != "":
		preview.Title = "Send email: " + truncate(args.Subject, 70)
	case len(args.To) == 1:
		preview.Title = "Send email to " + args.To[0] + "?"
	case len(args.To) > 1:
		preview.Title = fmt.Sprintf("Send email to %s and %d others?", args.To[0], len(args.To)-1)
	}
	return preview
}

// truncate returns s shortened to n runes with an ellipsis. Used to
// keep the gate card title at a glanceable length.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func emailAgentHandler(name string, a *App) tools.HandlerFunc {
	switch name {
	case "search_emails":
		return handleSearchEmails(a)
	case "read_email":
		return handleReadEmail(a)
	case "mark_read":
		return handleMarkRead(a)
	case "send_email":
		return handleSendEmail(a)
	default:
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown email tool: %s", name)
		}
	}
}

const notConfiguredMsg = "Your email account isn't configured. Ask me to set it up with configure_integration(provider=\"email\", auth_type=\"imap_smtp\")."

func handleSearchEmails(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			Query      string `json:"query"`
			Folder     string `json:"folder"`
			Since      string `json:"since"`
			UnreadOnly bool   `json:"unread_only"`
			Limit      int    `json:"limit"`
		}
		if len(input) > 0 {
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", fmt.Errorf("parsing input: %w", err)
			}
		}
		caller := ec.Caller()
		acct, err := LoadAccount(ec.Ctx, a.pool, a.enc, caller.TenantID, caller.UserID)
		if errors.Is(err, ErrNotConfigured) {
			return notConfiguredMsg, nil
		}
		if err != nil {
			return "", err
		}
		var since time.Time
		if inp.Since != "" {
			d, err := time.Parse("2006-01-02", inp.Since)
			if err != nil {
				return "Invalid 'since' date. Use YYYY-MM-DD.", nil
			}
			since = d
		}
		summaries, err := imapSearch(ec.Ctx, acct, inp.Query, inp.Folder, since, inp.UnreadOnly, inp.Limit)
		if err != nil {
			return "", err
		}
		if len(summaries) == 0 {
			return "No messages matched.", nil
		}
		return formatSummaries(summaries), nil
	}
}

func handleReadEmail(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			UID    uint32 `json:"uid"`
			Folder string `json:"folder"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		if inp.UID == 0 {
			return "uid is required.", nil
		}
		caller := ec.Caller()
		acct, err := LoadAccount(ec.Ctx, a.pool, a.enc, caller.TenantID, caller.UserID)
		if errors.Is(err, ErrNotConfigured) {
			return notConfiguredMsg, nil
		}
		if err != nil {
			return "", err
		}
		msg, err := imapFetch(ec.Ctx, acct, inp.UID, inp.Folder)
		if err != nil {
			return "", err
		}
		return formatMessage(msg), nil
	}
}

func handleMarkRead(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			UID    uint32 `json:"uid"`
			Read   *bool  `json:"read"`
			Folder string `json:"folder"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		if inp.UID == 0 {
			return "uid is required.", nil
		}
		read := true
		if inp.Read != nil {
			read = *inp.Read
		}
		caller := ec.Caller()
		acct, err := LoadAccount(ec.Ctx, a.pool, a.enc, caller.TenantID, caller.UserID)
		if errors.Is(err, ErrNotConfigured) {
			return notConfiguredMsg, nil
		}
		if err != nil {
			return "", err
		}
		if err := imapMarkRead(ec.Ctx, acct, inp.UID, read, inp.Folder); err != nil {
			return "", err
		}
		if read {
			return fmt.Sprintf("Marked uid %d as read.", inp.UID), nil
		}
		return fmt.Sprintf("Marked uid %d as unread.", inp.UID), nil
	}
}

func handleSendEmail(a *App) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		// Registry.Execute has already verified the approval token (we'd
		// never reach this handler without it, because PolicyGate without
		// a token creates a card instead).
		_, resolveTok, ok := approval.FromCtx(ec.Ctx)
		if !ok || resolveTok == [16]byte{} {
			// Defensive — shouldn't happen, but never silently send without
			// a token since it's the dedupe key.
			return "", errors.New("send_email: no approval token on context")
		}

		var args SendArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}
		if len(args.To) == 0 {
			return "At least one recipient is required.", nil
		}
		if strings.TrimSpace(args.Subject) == "" {
			return "Subject is required.", nil
		}

		caller := ec.Caller()
		acct, err := LoadAccount(ec.Ctx, a.pool, a.enc, caller.TenantID, caller.UserID)
		if errors.Is(err, ErrNotConfigured) {
			return notConfiguredMsg, nil
		}
		if err != nil {
			return "", err
		}

		msgID, err := sendOnce(ec.Ctx, a.pool, resolveTok, caller.TenantID, caller.UserID, acct, args)
		if err != nil {
			return "", err
		}
		return "Sent. message-id=" + msgID, nil
	}
}

func formatSummaries(s []Summary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d messages:\n", len(s))
	for _, m := range s {
		flag := ""
		if m.Unread {
			flag = " (unread)"
		}
		date := ""
		if !m.Date.IsZero() {
			date = m.Date.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(&b, "- uid=%d %s | %s | %s%s\n", m.UID, date, m.From, m.Subject, flag)
		if m.Snippet != "" {
			fmt.Fprintf(&b, "    %s\n", m.Snippet)
		}
	}
	return b.String()
}

func formatMessage(m *Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\n", m.From)
	if len(m.To) > 0 {
		fmt.Fprintf(&b, "To: %s\n", strings.Join(m.To, ", "))
	}
	if len(m.Cc) > 0 {
		fmt.Fprintf(&b, "Cc: %s\n", strings.Join(m.Cc, ", "))
	}
	fmt.Fprintf(&b, "Subject: %s\n", m.Subject)
	if !m.Date.IsZero() {
		fmt.Fprintf(&b, "Date: %s\n", m.Date.Format(time.RFC3339))
	}
	if m.MessageID != "" {
		fmt.Fprintf(&b, "Message-ID: %s\n", m.MessageID)
	}
	if m.InReplyTo != "" {
		fmt.Fprintf(&b, "In-Reply-To: %s\n", m.InReplyTo)
	}
	if len(m.Attachments) > 0 {
		fmt.Fprintf(&b, "Attachments: %s\n", strings.Join(m.Attachments, ", "))
	}
	b.WriteString("\n")
	b.WriteString(m.Body)
	return b.String()
}
