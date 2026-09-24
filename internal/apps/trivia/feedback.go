package trivia

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// maxFeedbackRunes bounds the comment. A phone keyboard in a bar produces a
// sentence or two; anything past this is a paste, not feedback.
const maxFeedbackRunes = 1000

// feedbackPoster is the Slack half, behind an interface so tests can watch
// what would have been posted without a workspace.
type feedbackPoster interface {
	post(ctx context.Context, tenantID uuid.UUID, text string) error
}

// validateFeedback checks the stars and cleans the comment.
func validateFeedback(stars int, comment string) (string, error) {
	if stars < 1 || stars > 5 {
		return "", fmt.Errorf("%w: a rating is 1 to 5 stars", ErrBadRequest)
	}
	c := strings.TrimSpace(comment)
	if utf8.RuneCountInString(c) > maxFeedbackRunes {
		return "", fmt.Errorf("%w: keep it under %d characters", ErrBadRequest, maxFeedbackRunes)
	}
	return c, nil
}

// SendFeedback posts a table's rating to the workspace's chosen channel. It
// is not stored: Slack is the record.
//
// A rating that does not reach Slack, for want of a channel or because Slack
// failed, is logged in full and otherwise dropped. The table is never told:
// it can do nothing about either, and the end of the night is not the moment
// to hand it an error.
func (a *App) SendFeedback(ctx context.Context, game *Game, team *Team, stars int, comment string) error {
	if game.Phase != PhasePodium {
		return fmt.Errorf("%w: the game is not over yet", ErrClosed)
	}
	comment, err := validateFeedback(stars, comment)
	if err != nil {
		return err
	}
	if a.feedback == nil {
		return nil
	}
	text := feedbackText(gameLabel(game), team.Name, stars, comment)
	err = a.feedback.post(ctx, game.TenantID, text)
	switch {
	case errors.Is(err, errFeedbackOff):
		slog.Info("trivia feedback not posted, no channel picked", "game_id", game.ID, "team_id", team.ID, "text", text)
	case err != nil:
		slog.Warn("posting trivia feedback to slack", "game_id", game.ID, "team_id", team.ID, "text", text, "error", err)
	}
	return nil
}

func gameLabel(game *Game) string {
	if game.Title != "" {
		return game.Title
	}
	return game.Name
}

// feedbackText is the Slack message: the stars first, so the channel can be
// read at a glance, then the comment as a quote.
func feedbackText(title, team string, stars int, comment string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s  *%s* rated %s: %d/5",
		strings.Repeat("★", stars), strings.Repeat("☆", 5-stars),
		slackEscape(team), slackEscape(title), stars)
	if comment != "" {
		for line := range strings.SplitSeq(comment, "\n") {
			b.WriteString("\n> ")
			b.WriteString(slackEscape(line))
		}
	}
	return b.String()
}

// slackEscape neutralises the three characters Slack treats as markup. The
// team name and comment are typed by strangers in a bar, and "<!channel>"
// should arrive as text rather than as a ping.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// errFeedbackOff is an admin's choice rather than a failure, so it is not
// logged as one.
var errFeedbackOff = errors.New("trivia feedback is not posted anywhere")

// slackFeedback posts with the workspace's own bot, to the channel the
// workspace chose in Admin.
type slackFeedback struct {
	pool *pgxpool.Pool
	enc  *crypto.Encryptor
}

func (s *slackFeedback) client(ctx context.Context, tenantID uuid.UUID) (*kitslack.Client, error) {
	return tenantSlackClient(ctx, s.pool, s.enc, tenantID)
}

// tenantSlackClient builds a client with the workspace's bot token.
func tenantSlackClient(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor, tenantID uuid.UUID) (*kitslack.Client, error) {
	if enc == nil {
		return nil, errors.New("slack is not configured on this deployment")
	}
	tenant, err := models.GetTenantByID(ctx, pool, tenantID)
	if err != nil {
		return nil, fmt.Errorf("loading tenant: %w", err)
	}
	if tenant == nil {
		return nil, fmt.Errorf("tenant %s not found", tenantID)
	}
	botToken, err := enc.Decrypt(tenant.BotToken)
	if err != nil {
		return nil, fmt.Errorf("decrypting bot token: %w", err)
	}
	return kitslack.NewClient(botToken), nil
}

// post sends to the channel the workspace picked.
func (s *slackFeedback) post(ctx context.Context, tenantID uuid.UUID, text string) error {
	target, err := GetFeedbackChannel(ctx, s.pool, tenantID)
	if err != nil {
		return err
	}
	if target.Off() {
		return errFeedbackOff
	}
	client, err := s.client(ctx, tenantID)
	if err != nil {
		return err
	}
	if err := client.PostMessage(ctx, target.ID, "", text); err != nil {
		return fmt.Errorf("posting to #%s: %w", target.Name, err)
	}
	return nil
}
