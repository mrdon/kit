package email

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
)

// LoadAccount reads the integrations row for (tenant_id, user_id,
// provider="email", auth_type="imap_smtp") and returns a decrypted Account
// ready for use. Returns ErrNotConfigured when there's no row — callers
// translate that into a setup-hint string for the agent. The decrypted
// password lives only on the returned struct for the duration of the
// caller's in-flight work; LoadAccount never logs it.
func LoadAccount(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor, tenantID, userID uuid.UUID) (*Account, error) {
	uid := userID
	integ, err := models.GetIntegration(ctx, pool, tenantID, Provider, AuthType, &uid)
	if err != nil {
		return nil, fmt.Errorf("loading email integration: %w", err)
	}
	if integ == nil {
		return nil, ErrNotConfigured
	}

	primaryEnc, _, err := models.GetIntegrationTokens(ctx, pool, tenantID, integ.ID)
	if err != nil {
		return nil, fmt.Errorf("loading email credentials: %w", err)
	}
	if primaryEnc == "" {
		return nil, fmt.Errorf("email integration %s has no password set", integ.ID)
	}
	password, err := enc.Decrypt(primaryEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypting email password: %w", err)
	}

	acct := &Account{
		EmailAddress: integ.Username,
		Username:     integ.Username,
		Password:     password,
		IMAPHost:     strFromConfig(integ.Config, "imap_host"),
		SMTPHost:     strFromConfig(integ.Config, "smtp_host"),
		FromName:     strFromConfig(integ.Config, "from_name"),
		Signature:    strFromConfig(integ.Config, "signature"),
	}
	acct.IMAPPort = portFromConfig(integ.Config, "imap_port", DefaultIMAPPort)
	acct.SMTPPort = portFromConfig(integ.Config, "smtp_port", DefaultSMTPPort)
	acct.IMAPSecurity = parseSecurity(strFromConfig(integ.Config, "imap_security"))
	acct.SMTPSecurity = parseSecurity(strFromConfig(integ.Config, "smtp_security"))

	if acct.IMAPHost == "" {
		return nil, fmt.Errorf("email integration %s missing imap_host", integ.ID)
	}
	if acct.SMTPHost == "" {
		return nil, fmt.Errorf("email integration %s missing smtp_host", integ.ID)
	}
	return acct, nil
}

// SearchSince returns envelope-only summaries in the given folder received
// on/after since, newest-first up to limit. It is the discovery primitive
// for scheduled callers (e.g. the task app's email-intake sweep) that own a
// watermark and want the candidate set without pulling bodies. Returns
// ErrNotConfigured when the user has no email integration, so callers can
// skip that user cleanly.
func SearchSince(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor, tenantID, userID uuid.UUID, folder string, since time.Time, limit int) ([]Summary, error) {
	acct, err := LoadAccount(ctx, pool, enc, tenantID, userID)
	if err != nil {
		return nil, err
	}
	return imapSearch(ctx, acct, "", folder, since, false, limit)
}

// sentFolderNames are the common IMAP sent-folder names, tried in order. IMAP
// has no standard name for the sent mailbox, so the well-known variants are
// probed until one selects (Gmail exposes "[Gmail]/Sent Mail", most others
// "Sent" or "Sent Items").
var sentFolderNames = []string{"Sent", "[Gmail]/Sent Mail", "Sent Items", "Sent Messages"}

// SearchSentSince returns envelope-only summaries from the user's sent mailbox
// received on/after since. It exists so a scheduled triage can tell whether the
// user already replied to an inbound thread. Because sent-folder naming isn't
// standardized, it tries the common names and uses the first that selects;
// returns an empty slice (no error) if none can be opened.
func SearchSentSince(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor, tenantID, userID uuid.UUID, since time.Time, limit int) ([]Summary, error) {
	acct, err := LoadAccount(ctx, pool, enc, tenantID, userID)
	if err != nil {
		return nil, err
	}
	for _, f := range sentFolderNames {
		sums, serr := imapSearch(ctx, acct, "", f, since, false, limit)
		if serr == nil {
			return sums, nil
		}
	}
	return nil, nil
}

// sendOnce is the idempotency boundary for send_email. It stamps a claim
// row BEFORE calling SMTP so a crash between claim and send results in a
// legitimate retry on the next approve. On SMTP error the claim row is
// removed so the user's re-approve can try again cleanly. A row with a
// non-empty message_id means the send already happened — return the
// cached id without re-sending.
func sendOnce(
	ctx context.Context,
	pool *pgxpool.Pool,
	resolveTok, tenantID, userID uuid.UUID,
	acct *Account,
	args SendArgs,
) (string, error) {
	if resolveTok == uuid.Nil {
		return "", errors.New("send_email requires an approval token")
	}

	// Upsert-probe: either insert a new empty claim or return the existing
	// message_id. Returning clause fires in both cases because the UPDATE
	// target is the row we collide with.
	var existingMsgID string
	err := pool.QueryRow(ctx, `
		INSERT INTO app_email_sent_messages
		  (resolve_token, tenant_id, user_id, message_id)
		VALUES ($1, $2, $3, '')
		ON CONFLICT (resolve_token) DO UPDATE
		  SET resolve_token = EXCLUDED.resolve_token
		RETURNING message_id`,
		resolveTok, tenantID, userID,
	).Scan(&existingMsgID)
	if err != nil {
		return "", fmt.Errorf("claiming send_email idempotency row: %w", err)
	}
	if existingMsgID != "" {
		return existingMsgID, nil
	}

	msgID, sendErr := smtpSend(ctx, acct, args)
	if sendErr != nil {
		// Delete the claim so the user's re-approve can retry. We scope
		// the DELETE to resolve_token + empty message_id so a
		// race-concurrent retry that already succeeded isn't stomped.
		_, _ = pool.Exec(ctx, `
			DELETE FROM app_email_sent_messages
			WHERE resolve_token = $1 AND message_id = ''`,
			resolveTok)
		return "", sendErr
	}
	_, err = pool.Exec(ctx, `
		UPDATE app_email_sent_messages
		SET message_id = $1
		WHERE resolve_token = $2`,
		msgID, resolveTok,
	)
	if err != nil {
		// The email actually went out. A retry will find an empty-message_id
		// row and re-send — not ideal, but rare (would need a DB error
		// between two small statements). Surface the error so the operator
		// knows something's off, while still returning the id so the card
		// resolves as sent.
		return msgID, fmt.Errorf("recording send_email message id: %w", err)
	}
	return msgID, nil
}

// parseSecurity normalizes the config-JSON string into a Security enum.
// Unknown values fall back to SecurityAuto so a typo doesn't lock the
// user out — the port-heuristic will likely still do the right thing.
func parseSecurity(s string) Security {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "tls", "ssl", "implicit":
		return SecurityTLS
	case "starttls", "start_tls":
		return SecuritySTARTTLS
	case "none", "plain", "insecure":
		return SecurityNone
	default:
		return SecurityAuto
	}
}

func strFromConfig(cfg map[string]any, key string) string {
	v, ok := cfg[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func portFromConfig(cfg map[string]any, key string, fallback int) int {
	v, ok := cfg[key]
	if !ok {
		return fallback
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return fallback
		}
		n, err := strconv.Atoi(t)
		if err != nil || n <= 0 || n > 65535 {
			return fallback
		}
		return n
	case float64: // JSON numbers land here
		n := int(t)
		if n <= 0 || n > 65535 {
			return fallback
		}
		return n
	case int:
		if t <= 0 || t > 65535 {
			return fallback
		}
		return t
	}
	return fallback
}
