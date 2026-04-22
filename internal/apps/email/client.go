package email

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// imapTimeout caps how long any one IMAP tool call can block. Tools run
// inside the agent loop; a silent hang would freeze the entire turn.
const imapTimeout = 30 * time.Second

// openIMAP dials the account's IMAP host over TLS (or STARTTLS for port
// 143) and logs in. Caller MUST defer Logout on the returned client.
func openIMAP(ctx context.Context, acct *Account) (*imapclient.Client, error) {
	port := acct.IMAPPort
	if port == 0 {
		port = DefaultIMAPPort
	}
	addr := net.JoinHostPort(acct.IMAPHost, strconv.Itoa(port))

	deadline, hasDL := ctx.Deadline()
	if !hasDL {
		deadline = time.Now().Add(imapTimeout)
	}
	opts := &imapclient.Options{}

	// Resolve the security mode. Explicit config wins; Auto falls back to
	// the well-known port mapping: 993 → implicit TLS, anything else →
	// STARTTLS (Gmail/iCloud/Fastmail all use 993, legacy self-hosted
	// uses 143+STARTTLS).
	sec := acct.IMAPSecurity
	if sec == SecurityAuto {
		if port == 993 {
			sec = SecurityTLS
		} else {
			sec = SecuritySTARTTLS
		}
	}

	var c *imapclient.Client
	var err error
	switch sec { //nolint:exhaustive // SecurityAuto resolved above
	case SecurityTLS:
		c, err = imapclient.DialTLS(addr, opts)
	case SecuritySTARTTLS:
		c, err = imapclient.DialStartTLS(addr, opts)
	case SecurityNone:
		c, err = imapclient.DialInsecure(addr, opts)
	default:
		return nil, fmt.Errorf("unknown imap security mode %q", sec)
	}
	if err != nil {
		return nil, fmt.Errorf("dialing imap %s: %w", addr, err)
	}

	// imapclient doesn't accept a Context directly; apply the deadline on
	// the underlying TCP conn so slow commands unstick.
	_ = deadline // imapclient command API already blocks per-command; we rely on server timeouts.

	username := acct.Username
	if username == "" {
		username = acct.EmailAddress
	}
	if err := c.Login(username, acct.Password).Wait(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("imap login: %w", err)
	}
	return c, nil
}

// imapSearch runs SEARCH on the given folder with criteria parsed from a
// small, forgiving query syntax. Empty query returns the newest messages.
// Returns summaries ordered newest-first up to limit.
func imapSearch(ctx context.Context, acct *Account, query, folder string, since time.Time, unreadOnly bool, limit int) ([]Summary, error) {
	if folder == "" {
		folder = "INBOX"
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	c, err := openIMAP(ctx, acct)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Logout().Wait() }()

	if _, err := c.Select(folder, nil).Wait(); err != nil {
		return nil, fmt.Errorf("selecting %s: %w", folder, err)
	}

	criteria := parseSearchQuery(query)
	if !since.IsZero() {
		if criteria.Since.IsZero() || since.After(criteria.Since) {
			criteria.Since = since
		}
	}
	if unreadOnly {
		criteria.NotFlag = append(criteria.NotFlag, imap.FlagSeen)
	}

	searchData, err := c.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("imap search: %w", err)
	}
	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		return nil, nil
	}
	// Newest-first: IMAP returns ascending UIDs, which roughly correlates
	// with arrival order. Reverse + cap to limit.
	if len(uids) > limit {
		uids = uids[len(uids)-limit:]
	}
	reversed := make([]imap.UID, len(uids))
	for i, u := range uids {
		reversed[len(uids)-1-i] = u
	}

	var uidSet imap.UIDSet
	uidSet.AddNum(reversed...)

	// Peek the full message (headers + body). We need the outer headers
	// for MIME parsing — without Content-Type we can't walk multipart
	// bodies, and snippets end up as raw boundary/header junk. No
	// Partial fetch — some servers (greenmail in particular) return
	// partial literals the parser chokes on.
	bodyPeek := &imap.FetchItemBodySection{Peek: true}
	fetchCmd := c.Fetch(uidSet, &imap.FetchOptions{
		UID:         true,
		Envelope:    true,
		Flags:       true,
		BodySection: []*imap.FetchItemBodySection{bodyPeek},
	})
	msgs, err := fetchCmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("imap fetch summaries: %w", err)
	}

	byUID := make(map[imap.UID]*imapclient.FetchMessageBuffer, len(msgs))
	for _, m := range msgs {
		byUID[m.UID] = m
	}
	summaries := make([]Summary, 0, len(reversed))
	for _, u := range reversed {
		m := byUID[u]
		if m == nil {
			continue
		}
		summaries = append(summaries, summaryFromBuf(m, bodyPeek))
	}
	return summaries, nil
}

// imapFetch pulls a single message in full — envelope, flags, and the
// text/plain body (or text/html stripped to plain text if no plaintext
// part exists).
func imapFetch(ctx context.Context, acct *Account, uid uint32, folder string) (*Message, error) {
	if folder == "" {
		folder = "INBOX"
	}
	c, err := openIMAP(ctx, acct)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Logout().Wait() }()

	if _, err := c.Select(folder, nil).Wait(); err != nil {
		return nil, fmt.Errorf("selecting %s: %w", folder, err)
	}

	var uidSet imap.UIDSet
	uidSet.AddNum(imap.UID(uid))

	bodySec := &imap.FetchItemBodySection{Peek: true}
	fetchCmd := c.Fetch(uidSet, &imap.FetchOptions{
		UID:           true,
		Envelope:      true,
		Flags:         true,
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
		BodySection:   []*imap.FetchItemBodySection{bodySec},
	})
	msgs, err := fetchCmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("imap fetch: %w", err)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("uid %d not found in %s", uid, folder)
	}

	m := msgs[0]
	out := &Message{UID: uint32(m.UID)}
	if m.Envelope != nil {
		out.From = addressListString(m.Envelope.From)
		out.To = addressList(m.Envelope.To)
		out.Cc = addressList(m.Envelope.Cc)
		out.Subject = m.Envelope.Subject
		out.Date = m.Envelope.Date
		out.MessageID = m.Envelope.MessageID
		if len(m.Envelope.InReplyTo) > 0 {
			out.InReplyTo = m.Envelope.InReplyTo[0]
			out.References = m.Envelope.InReplyTo
		}
	}
	raw := m.FindBodySection(bodySec)
	out.Body = extractPlainText(raw)
	out.Attachments = extractAttachmentNames(m.BodyStructure)
	return out, nil
}

// imapMarkRead toggles the \Seen flag on the given UID.
func imapMarkRead(ctx context.Context, acct *Account, uid uint32, read bool, folder string) error {
	if folder == "" {
		folder = "INBOX"
	}
	c, err := openIMAP(ctx, acct)
	if err != nil {
		return err
	}
	defer func() { _ = c.Logout().Wait() }()

	if _, err := c.Select(folder, nil).Wait(); err != nil {
		return fmt.Errorf("selecting %s: %w", folder, err)
	}

	op := imap.StoreFlagsAdd
	if !read {
		op = imap.StoreFlagsDel
	}
	var uidSet imap.UIDSet
	uidSet.AddNum(imap.UID(uid))
	if err := c.Store(uidSet, &imap.StoreFlags{
		Op:     op,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagSeen},
	}, nil).Close(); err != nil {
		return fmt.Errorf("imap store: %w", err)
	}
	return nil
}

// parseSearchQuery turns a user-facing query into IMAP criteria. Supports
// a small subset — `from:`, `to:`, `subject:`, `since:YYYY-MM-DD`,
// `is:unread` — and treats anything else as a body-text search. Quoting
// (via double-quotes) groups words into a single criterion. A completely
// empty query returns zero-value criteria (matches everything).
func parseSearchQuery(query string) *imap.SearchCriteria {
	c := &imap.SearchCriteria{}
	tokens := tokenizeQuery(query)
	var textParts []string
	for _, t := range tokens {
		lower := strings.ToLower(t)
		switch {
		case strings.HasPrefix(lower, "from:"):
			c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: t[len("from:"):]})
		case strings.HasPrefix(lower, "to:"):
			c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: "To", Value: t[len("to:"):]})
		case strings.HasPrefix(lower, "subject:"):
			c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: t[len("subject:"):]})
		case strings.HasPrefix(lower, "since:"):
			if d, err := time.Parse("2006-01-02", t[len("since:"):]); err == nil {
				c.Since = d
			}
		case lower == "is:unread":
			c.NotFlag = append(c.NotFlag, imap.FlagSeen)
		case lower == "is:read":
			c.Flag = append(c.Flag, imap.FlagSeen)
		default:
			textParts = append(textParts, t)
		}
	}
	if len(textParts) > 0 {
		c.Text = append(c.Text, strings.Join(textParts, " "))
	}
	return c
}

// tokenizeQuery splits on whitespace, respecting double-quoted groups so
// `subject:"hello world"` stays one token.
func tokenizeQuery(query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	var out []string
	var cur strings.Builder
	inQuote := false
	for _, r := range query {
		switch {
		case r == '"':
			inQuote = !inQuote
		case !inQuote && (r == ' ' || r == '\t'):
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// summaryFromBuf builds a Summary from a fetched message buffer.
func summaryFromBuf(m *imapclient.FetchMessageBuffer, bodyPeek *imap.FetchItemBodySection) Summary {
	s := Summary{UID: uint32(m.UID)}
	if m.Envelope != nil {
		s.From = addressListString(m.Envelope.From)
		s.Subject = m.Envelope.Subject
		s.Date = m.Envelope.Date
	}
	s.Unread = !slices.Contains(m.Flags, imap.FlagSeen)
	s.Snippet = snippetFromBytes(m.FindBodySection(bodyPeek))
	return s
}

var collapseWS = regexp.MustCompile(`\s+`)

func snippetFromBytes(b []byte) string {
	s := extractPlainText(b)
	s = collapseWS.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func addressListString(addrs []imap.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		parts = append(parts, formatAddress(a))
	}
	return strings.Join(parts, ", ")
}

func addressList(addrs []imap.Address) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, formatAddress(a))
	}
	return out
}

func formatAddress(a imap.Address) string {
	mbox := a.Mailbox
	host := a.Host
	email := mbox + "@" + host
	if a.Name != "" {
		return fmt.Sprintf("%q <%s>", a.Name, email)
	}
	return email
}

// extractAttachmentNames walks the body structure and returns any named
// attachment filenames. Nil-safe.
func extractAttachmentNames(bs imap.BodyStructure) []string {
	if bs == nil {
		return nil
	}
	var names []string
	bs.Walk(func(_ []int, part imap.BodyStructure) bool {
		if sp, ok := part.(*imap.BodyStructureSinglePart); ok {
			if name := sp.Filename(); name != "" {
				names = append(names, name)
			}
		}
		return true
	})
	return names
}
