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

	// Resolve tenant
	tenant, err := models.GetTenantBySlackTeamID(ctx, a.Pool, teamID)
	if err != nil {
		slog.Error("looking up tenant", "team_id", teamID, "error", err)
		return
	}
	if tenant == nil {
		slog.Warn("unknown team", "team_id", teamID)
		return
	}

	// Decrypt bot token
	botToken, err := a.Encryptor.Decrypt(tenant.BotToken)
	if err != nil {
		slog.Error("decrypting bot token", "tenant_id", tenant.ID, "error", err)
		return
	}
	client := kitslack.NewClient(botToken)
	dmClient = client

	// Parse the event based on type
	var slackUserID, text, channel, threadTS, triggerTS string
	var files []kitslack.File
	switch eventType {
	case "message":
		var msg kitslack.MessageEvent
		if err := json.Unmarshal(rawEvent, &msg); err != nil {
			slog.Error("parsing message event", "error", err)
			return
		}
		if !msg.ShouldProcess() {
			return
		}
		// Outside of 1:1 DMs, only respond when Kit started the thread
		// (scheduled task, onboarding). Human-started threads require an
		// explicit @mention (routed as app_mention, not here).
		if msg.ChannelType != "im" {
			if msg.ThreadTS == "" {
				return
			}
			existing, err := models.FindSessionByThread(ctx, a.Pool, tenant.ID, msg.Channel, msg.ThreadTS)
			if err != nil {
				slog.Error("looking up session by thread", "error", err)
				return
			}
			if existing == nil || !existing.BotInitiated {
				return
			}
		}
		slackUserID = msg.User
		text = msg.Text
		channel = msg.Channel
		threadTS = msg.ThreadTimestamp()
		triggerTS = msg.Timestamp

		files = msg.Files

	case "app_mention":
		var mention kitslack.AppMentionEvent
		if err := json.Unmarshal(rawEvent, &mention); err != nil {
			slog.Error("parsing app_mention event", "error", err)
			return
		}
		slackUserID = mention.User
		text = mention.Text
		channel = mention.Channel
		threadTS = mention.ThreadTimestamp()
		triggerTS = mention.Timestamp

		files = mention.Files

	default:
		return
	}
	dmUserID = slackUserID

	// Get or create user — fetch display name from Slack on first contact
	displayName := ""
	if info, err := client.GetUserInfo(ctx, slackUserID); err == nil {
		displayName = info.DisplayName
	}
	user, err := models.GetOrCreateUser(ctx, a.Pool, tenant.ID, slackUserID, displayName, false)
	if err != nil {
		slog.Error("resolving user", "error", err)
		return
	}

	// Check if setup is incomplete and user is not admin
	if !tenant.SetupComplete && !user.IsAdmin {
		slog.Info("setup incomplete, suppressing response", "tenant_id", tenant.ID, "user_id", user.ID)
		_ = client.PostMessage(ctx, channel, threadTS,
			"I'm still being set up! Please ask your admin to finish setting me up.")
		return
	}

	session, err := a.resolveSession(ctx, client, tenant.ID, user.ID, channel, threadTS, triggerTS)
	if err != nil {
		slog.Error("resolving session", "error", err)
		return
	}

	slog.Info("processing message",
		"tenant_id", tenant.ID,
		"user_id", user.ID,
		"session_id", session.ID,
		"files", len(files),
	)

	// Process file uploads (admin only)
	if len(files) > 0 && user.IsAdmin {
		ing := ingest.NewIngester(a.Pool, a.LLM, client)
		var createdSkills []string
		for _, f := range files {
			names, err := ing.ProcessFile(ctx, tenant.ID, f)
			if err != nil {
				slog.Error("file ingestion failed", "filename", f.Name, "error", err)
				_ = client.PostMessage(ctx, channel, threadTS,
					fmt.Sprintf("I had trouble processing `%s`: %s", f.Name, err.Error()))
				continue
			}
			createdSkills = append(createdSkills, names...)
		}
		if len(createdSkills) > 0 {
			// Augment the text with info about created skills
			text += fmt.Sprintf("\n\n[System: The following skills were just created from uploaded files: %s. Acknowledge this to the user.]",
				strings.Join(createdSkills, ", "))
		}
	}

	// Run the agent loop
	if err := a.Agent.Run(ctx, client, tenant, user, session, channel, threadTS, text, nil); err != nil {
		slog.Error("agent run failed", "error", err, "session_id", session.ID)
	}
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
	if err := a.Agent.Run(ctx, client, tenant, user, session, dmChannel, "", onboardingPrompt, nil); err != nil {
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
			_ = models.AppendSessionEvent(ctx, pool, tenantID, sessionID, "assistant_turn", map[string]any{
				"content": []map[string]any{{"type": "text", "text": m.Text}},
			})
		} else {
			_ = models.AppendSessionEvent(ctx, pool, tenantID, sessionID, "message_received", map[string]any{
				"text":    m.Text,
				"channel": channel,
			})
		}
	}
}
