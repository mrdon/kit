package livegame

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/crypto"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// The end-of-night rating every live game asks for: one to five stars and an
// optional comment from each phone, posted to a Slack channel the workspace
// picks. Nothing is stored -- Slack is the record. Each game keeps its own
// channel setting; the asking, the checking and the posting are shared.

// MaxFeedbackRunes bounds the comment. A phone keyboard in a bar produces a
// sentence or two; anything past this is a paste, not feedback.
const MaxFeedbackRunes = 1000

// ErrBadFeedback is a rating a person can fix.
var ErrBadFeedback = errors.New("bad feedback")

// ErrFeedbackOff means the workspace has not picked a channel. It is an
// admin's choice rather than a failure, so callers log it quietly.
var ErrFeedbackOff = errors.New("feedback is not posted anywhere")

// ValidateFeedback checks the stars and cleans the comment.
func ValidateFeedback(stars int, comment string) (string, error) {
	if stars < 1 || stars > 5 {
		return "", fmt.Errorf("%w: a rating is 1 to 5 stars", ErrBadFeedback)
	}
	c := strings.TrimSpace(comment)
	if utf8.RuneCountInString(c) > MaxFeedbackRunes {
		return "", fmt.Errorf("%w: keep it under %d characters", ErrBadFeedback, MaxFeedbackRunes)
	}
	return c, nil
}

// FeedbackText is the Slack message: the stars first, so the channel can be
// read at a glance, then the comment as a quote.
func FeedbackText(title, who string, stars int, comment string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s  *%s* rated %s: %d/5",
		strings.Repeat("★", stars), strings.Repeat("☆", 5-stars),
		SlackEscape(who), SlackEscape(title), stars)
	if comment != "" {
		for line := range strings.SplitSeq(comment, "\n") {
			b.WriteString("\n> ")
			b.WriteString(SlackEscape(line))
		}
	}
	return b.String()
}

// SlackEscape neutralises the three characters Slack treats as markup. Names
// and comments are typed by strangers in a bar, and "<!channel>" should
// arrive as text rather than as a ping.
func SlackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// FeedbackChannel is where a game's ratings go. The zero value is "nowhere".
type FeedbackChannel struct {
	ID   string
	Name string
}

// Off reports that no channel is picked.
func (c FeedbackChannel) Off() bool { return c.ID == "" }

// FeedbackPoster sends one rating somewhere. Games hold one so tests can
// watch what would have been posted without a workspace.
type FeedbackPoster interface {
	Post(ctx context.Context, tenantID uuid.UUID, text string) error
}

// SlackFeedback posts with the workspace's own bot to the channel Channel
// says the workspace picked for this game.
type SlackFeedback struct {
	Pool    *pgxpool.Pool
	Enc     *crypto.Encryptor
	Channel func(ctx context.Context, tenantID uuid.UUID) (FeedbackChannel, error)
}

// Post sends one rating. ErrFeedbackOff when no channel is picked.
func (s *SlackFeedback) Post(ctx context.Context, tenantID uuid.UUID, text string) error {
	target, err := s.Channel(ctx, tenantID)
	if err != nil {
		return err
	}
	if target.Off() {
		return ErrFeedbackOff
	}
	client, err := kitslack.TenantClient(ctx, s.Pool, s.Enc, tenantID)
	if err != nil {
		return err
	}
	if err := client.PostMessage(ctx, target.ID, "", text); err != nil {
		return fmt.Errorf("posting to #%s: %w", target.Name, err)
	}
	return nil
}

// ChannelOption is one channel an admin may pick, as the console lists it.
type ChannelOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	BotIsMember bool   `json:"bot_is_member"`
	IsPrivate   bool   `json:"is_private"`
}

// ChannelOptions lists the workspace's channels for the picker, sorted by
// name. The string is a message for the admin when Slack cannot be reached.
func ChannelOptions(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor, tenantID uuid.UUID) ([]ChannelOption, string) {
	client, err := kitslack.TenantClient(ctx, pool, enc, tenantID)
	if err != nil {
		return nil, "Could not reach Slack: " + err.Error()
	}
	channels, err := client.ListChannels(ctx)
	if err != nil {
		return nil, "Could not list Slack channels: " + err.Error()
	}
	out := make([]ChannelOption, 0, len(channels))
	for _, c := range channels {
		out = append(out, ChannelOption{ID: c.ID, Name: c.Name, BotIsMember: c.IsMember, IsPrivate: c.IsPrivate})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, ""
}

// PickChannel resolves an admin's choice against the channels Kit can see.
// An empty id is "nowhere". The error is a sentence for the admin.
func PickChannel(options []ChannelOption, id string) (FeedbackChannel, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return FeedbackChannel{}, nil
	}
	for _, o := range options {
		if o.ID != id {
			continue
		}
		if !o.BotIsMember {
			return FeedbackChannel{}, fmt.Errorf("Kit is not in #%s yet. Invite it with /invite @Kit and try again.", o.Name) //nolint:staticcheck // shown to an admin as a sentence
		}
		return FeedbackChannel{ID: o.ID, Name: o.Name}, nil
	}
	return FeedbackChannel{}, errors.New("That channel is not one Kit can see.") //nolint:staticcheck // shown to an admin as a sentence
}

// ChannelPayload is what the console's "where ratings go" panel reads: the
// current choice, and every channel it could be.
type ChannelPayload struct {
	// ChannelID is the channel ratings go to, "" when they go nowhere.
	ChannelID   string          `json:"channel_id"`
	ChannelName string          `json:"channel_name"`
	Channels    []ChannelOption `json:"channels"`
	// ChannelsError is a sentence for the admin when Slack cannot be read.
	ChannelsError string `json:"channels_error,omitempty"`
}

// NewChannelPayload builds the panel's payload around the current choice.
func NewChannelPayload(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor, tenantID uuid.UUID, current FeedbackChannel) ChannelPayload {
	p := ChannelPayload{ChannelID: current.ID, ChannelName: current.Name}
	p.Channels, p.ChannelsError = ChannelOptions(ctx, pool, enc, tenantID)
	return p
}
