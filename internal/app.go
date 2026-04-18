package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/ingest"
	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
	"github.com/mrdon/kit/internal/web"
)

// App is the core application that ties together Slack events, the agent, and the database.
type App struct {
	Pool      *pgxpool.Pool
	Encryptor *crypto.Encryptor
	Agent     *agent.Agent
	LLM       *anthropic.Client
	Fetcher   *web.Fetcher
}

// NewApp creates a new App with all dependencies.
func NewApp(pool *pgxpool.Pool, enc *crypto.Encryptor, apiKey string, rdb *redis.Client) *App {
	llm := anthropic.NewClient(apiKey)
	fetcher := web.NewFetcher(rdb)
	return &App{
		Pool:      pool,
		Encryptor: enc,
		Fetcher:   fetcher,
		Agent:     agent.NewAgent(pool, llm, fetcher),
		LLM:       llm,
	}
}

// slackEvent is the event-type-agnostic payload extracted from either a
// message or an app_mention Slack event.
type slackEvent struct {
	SlackUserID string
	Text        string
	Channel     string
	ThreadTS    string
	TriggerTS   string
	Files       []kitslack.File
}

// HandleSlackEvent is called by the Slack handler when a message or app_mention is received.
func (a *App) HandleSlackEvent(teamID string, rawEvent json.RawMessage, eventType string) {
	ctx := context.Background()

	// Captured for the panic handler so we can best-effort notify the user.
	var dmClient *kitslack.Client
	var dmUserID string

	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in event handler", "panic", r, "team_id", teamID, "stack", string(debug.Stack()))
			if dmClient != nil && dmUserID != "" {
				if ch, err := dmClient.OpenConversation(ctx, dmUserID); err == nil {
					_ = dmClient.PostMessage(ctx, ch, "", "Something went wrong processing your message. We're looking into it.")
				}
			}
		}
	}()

	tenant, client, ok := a.resolveTenantAndClient(ctx, teamID)
	if !ok {
		return
	}
	dmClient = client

	evt, ok := a.parseSlackEvent(ctx, tenant.ID, rawEvent, eventType)
	if !ok {
		return
	}
	dmUserID = evt.SlackUserID

	// Get or create user — fetch display name from Slack on first contact
	displayName := ""
	if info, err := client.GetUserInfo(ctx, evt.SlackUserID); err == nil {
		displayName = info.DisplayName
	}
	user, err := models.GetOrCreateUser(ctx, a.Pool, tenant.ID, evt.SlackUserID, displayName, false)
	if err != nil {
		slog.Error("resolving user", "error", err)
		return
	}

	if !tenant.SetupComplete && !user.IsAdmin {
		slog.Info("setup incomplete, suppressing response", "tenant_id", tenant.ID, "user_id", user.ID)
		_ = client.PostMessage(ctx, evt.Channel, evt.ThreadTS,
			"I'm still being set up! Please ask your admin to finish setting me up.")
		return
	}

	session, err := a.resolveSession(ctx, client, tenant.ID, user.ID, evt.Channel, evt.ThreadTS, evt.TriggerTS)
	if err != nil {
		slog.Error("resolving session", "error", err)
		return
	}

	slog.Info("processing message",
		"tenant_id", tenant.ID,
		"user_id", user.ID,
		"session_id", session.ID,
		"files", len(evt.Files),
	)

	text := evt.Text
	if len(evt.Files) > 0 && user.IsAdmin {
		text = a.ingestFiles(ctx, client, tenant.ID, evt.Channel, evt.ThreadTS, text, evt.Files)
	}

	if err := a.Agent.Run(ctx, agent.RunInput{
		Slack:    client,
		Tenant:   tenant,
		User:     user,
		Session:  session,
		Channel:  evt.Channel,
		ThreadTS: evt.ThreadTS,
		UserText: text,
	}); err != nil {
		slog.Error("agent run failed", "error", err, "session_id", session.ID)
	}
}

// resolveTenantAndClient looks up the tenant for teamID and returns a Slack
// client with the decrypted bot token. Returns ok=false on any miss/failure
// (errors already logged).
func (a *App) resolveTenantAndClient(ctx context.Context, teamID string) (*models.Tenant, *kitslack.Client, bool) {
	tenant, err := models.GetTenantBySlackTeamID(ctx, a.Pool, teamID)
	if err != nil {
		slog.Error("looking up tenant", "team_id", teamID, "error", err)
		return nil, nil, false
	}
	if tenant == nil {
		slog.Warn("unknown team", "team_id", teamID)
		return nil, nil, false
	}
	botToken, err := a.Encryptor.Decrypt(tenant.BotToken)
	if err != nil {
		slog.Error("decrypting bot token", "tenant_id", tenant.ID, "error", err)
		return nil, nil, false
	}
	return tenant, kitslack.NewClient(botToken), true
}

// parseSlackEvent normalizes the message/app_mention payload into a slackEvent,
// also applying the message-vs-mention "should we respond?" gates. Returns
// ok=false when the event should be ignored.
func (a *App) parseSlackEvent(ctx context.Context, tenantID uuid.UUID, rawEvent json.RawMessage, eventType string) (*slackEvent, bool) {
	switch eventType {
	case "message":
		return a.parseMessageEvent(ctx, tenantID, rawEvent)
	case "app_mention":
		return parseMentionEvent(rawEvent)
	default:
		return nil, false
	}
}

func (a *App) parseMessageEvent(ctx context.Context, tenantID uuid.UUID, rawEvent json.RawMessage) (*slackEvent, bool) {
	var msg kitslack.MessageEvent
	if err := json.Unmarshal(rawEvent, &msg); err != nil {
		slog.Error("parsing message event", "error", err)
		return nil, false
	}
	if !msg.ShouldProcess() {
		return nil, false
	}
	// Outside of 1:1 DMs, only respond when Kit started the thread
	// (scheduled task, onboarding). Human-started threads require an
	// explicit @mention (routed as app_mention, not here).
	if msg.ChannelType != "im" {
		if msg.ThreadTS == "" {
			return nil, false
		}
		existing, err := models.FindSessionByThread(ctx, a.Pool, tenantID, msg.Channel, msg.ThreadTS)
		if err != nil {
			slog.Error("looking up session by thread", "error", err)
			return nil, false
		}
		if existing == nil || !existing.BotInitiated {
			return nil, false
		}
	}
	return &slackEvent{
		SlackUserID: msg.User,
		Text:        msg.Text,
		Channel:     msg.Channel,
		ThreadTS:    msg.ThreadTimestamp(),
		TriggerTS:   msg.Timestamp,
		Files:       msg.Files,
	}, true
}

func parseMentionEvent(rawEvent json.RawMessage) (*slackEvent, bool) {
	var mention kitslack.AppMentionEvent
	if err := json.Unmarshal(rawEvent, &mention); err != nil {
		slog.Error("parsing app_mention event", "error", err)
		return nil, false
	}
	return &slackEvent{
		SlackUserID: mention.User,
		Text:        mention.Text,
		Channel:     mention.Channel,
		ThreadTS:    mention.ThreadTimestamp(),
		TriggerTS:   mention.Timestamp,
		Files:       mention.Files,
	}, true
}

// ingestFiles processes uploaded files through the ingester and returns the
// user text augmented with a note about any skills created. Failures post a
// per-file message back to Slack and do not abort the agent run.
func (a *App) ingestFiles(ctx context.Context, client *kitslack.Client, tenantID uuid.UUID, channel, threadTS, text string, files []kitslack.File) string {
	ing := ingest.NewIngester(a.Pool, a.LLM, client)
	var createdSkills []string
	for _, f := range files {
		names, err := ing.ProcessFile(ctx, tenantID, f)
		if err != nil {
			slog.Error("file ingestion failed", "filename", f.Name, "error", err)
			_ = client.PostMessage(ctx, channel, threadTS,
				fmt.Sprintf("I had trouble processing `%s`: %s", f.Name, err.Error()))
			continue
		}
		createdSkills = append(createdSkills, names...)
	}
	if len(createdSkills) > 0 {
		text += fmt.Sprintf("\n\n[System: The following skills were just created from uploaded files: %s. Acknowledge this to the user.]",
			strings.Join(createdSkills, ", "))
	}
	return text
}

// HandlePostInstall is called after a successful OAuth install to start onboarding.
func (a *App) HandlePostInstall(ctx context.Context, tenant *models.Tenant, installerSlackID string) {
	botToken, err := a.Encryptor.Decrypt(tenant.BotToken)
	if err != nil {
		slog.Error("decrypting bot token for onboarding", "error", err)
		return
	}
	client := kitslack.NewClient(botToken)

	// Open DM with installer
	dmChannel, err := client.OpenConversation(ctx, installerSlackID)
	if err != nil {
		slog.Error("opening DM for onboarding", "error", err)
		return
	}

	// Get the admin user
	user, err := models.GetUserBySlackID(ctx, a.Pool, tenant.ID, installerSlackID)
	if err != nil || user == nil {
		slog.Error("finding installer user", "error", err)
		return
	}

	// Create a session for the onboarding DM
	session, err := models.GetOrCreateSession(ctx, a.Pool, tenant.ID, dmChannel, "", user.ID)
	if err != nil {
		slog.Error("creating onboarding session", "error", err)
		return
	}

	// Run the agent with an onboarding prompt
	onboardingPrompt := "I just installed Kit. Let's get it set up."
	if err := a.Agent.Run(ctx, agent.RunInput{
		Slack:    client,
		Tenant:   tenant,
		User:     user,
		Session:  session,
		Channel:  dmChannel,
		UserText: onboardingPrompt,
	}); err != nil {
		slog.Error("onboarding agent run failed", "error", err)
	}
}

// resolveSession finds an existing session for the thread or creates one.
// On fresh creation in a Slack thread that already has messages, it seeds
// the session with the thread's prior messages so the agent has context.
func (a *App) resolveSession(ctx context.Context, client *kitslack.Client, tenantID, userID uuid.UUID, channel, threadTS, triggerTS string) (*models.Session, error) {
	session, err := models.FindSessionByThread(ctx, a.Pool, tenantID, channel, threadTS)
	if err != nil {
		return nil, fmt.Errorf("finding session: %w", err)
	}
	if session != nil {
		return session, nil
	}
	session, err = models.CreateSession(ctx, a.Pool, tenantID, channel, threadTS, userID, false)
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}
	if threadTS != "" && threadTS != triggerTS {
		bootstrapThreadHistory(ctx, a.Pool, client, tenantID, session.ID, channel, threadTS, triggerTS)
	}
	return session, nil
}

// bootstrapThreadHistory seeds a fresh session's event log with the Slack
// thread's prior messages so the agent has context when @-mentioned mid-thread.
// The triggering message is skipped because agent.Run records it separately.
func bootstrapThreadHistory(ctx context.Context, pool *pgxpool.Pool, client *kitslack.Client, tenantID uuid.UUID, sessionID uuid.UUID, channel, threadTS, triggerTS string) {
	msgs, err := client.GetThreadReplies(ctx, channel, threadTS)
	if err != nil {
		slog.Warn("fetching thread replies for bootstrap", "error", err, "channel", channel, "thread_ts", threadTS)
		return
	}
	for _, m := range msgs {
		if m.Timestamp == triggerTS || m.Text == "" {
			continue
		}
		if m.BotID != "" {
			_ = models.AppendSessionEvent(ctx, pool, tenantID, sessionID, models.EventTypeAssistantTurn, map[string]any{
				"content": []map[string]any{{"type": "text", "text": m.Text}},
			})
		} else {
			_ = models.AppendSessionEvent(ctx, pool, tenantID, sessionID, models.EventTypeMessageReceived, map[string]any{
				"text":    m.Text,
				"channel": channel,
			})
		}
	}
}
