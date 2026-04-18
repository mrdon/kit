package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"

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
	var slackUserID, text, channel, threadTS string
	var files []kitslack.File
	switch eventType {
	case "message":
		var msg kitslack.MessageEvent
		if err := json.Unmarshal(rawEvent, &msg); err != nil {
			slog.Error("parsing message event", "error", err)
			return
		}
		if !msg.ShouldProcess() {
			slog.Info("message filtered by ShouldProcess", "subtype", msg.SubType, "bot_id", msg.BotID, "user", msg.User, "channel", msg.Channel)
			return
		}
		slackUserID = msg.User
		text = msg.Text
		channel = msg.Channel
		threadTS = msg.ThreadTimestamp()

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

	// Get or create session
	session, err := models.GetOrCreateSession(ctx, a.Pool, tenant.ID, channel, threadTS, user.ID)
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
