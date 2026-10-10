package posters

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/chat"
	"github.com/mrdon/kit/internal/posterrender"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/sse"
)

// Poster chat and template chat: the shared widget talks to these over
// SSE exactly as card chat does, but the subject is a poster or a
// template, so the session is keyed on it and the agent is clamped to the
// poster tools. Kit's agent always uses Sonnet here.

func registerChatRoutes(mux apps.Mux, a *App) {
	route := func(h http.HandlerFunc) http.Handler { return console.JSON(a.pool, a.signer, h) }
	mux.Handle("POST /{slug}/api/posters/{id}/chat/execute", route(a.handlePosterChat))
	mux.Handle("POST /{slug}/api/posters/templates/{id}/chat/execute", route(a.handleTemplateChat))
}

const maxChatTextBytes = 8 * 1024

func (a *App) handlePosterChat(w http.ResponseWriter, r *http.Request) {
	a.handleChat(w, r, "poster", func(ctx context.Context, caller *services.Caller, id uuid.UUID) (string, error) {
		return a.posterChatSuffix(ctx, caller, id)
	})
}

func (a *App) handleTemplateChat(w http.ResponseWriter, r *http.Request) {
	a.handleChat(w, r, "template", func(ctx context.Context, caller *services.Caller, id uuid.UUID) (string, error) {
		return a.templateChatSuffix(ctx, caller, id)
	})
}

type suffixFn func(ctx context.Context, caller *services.Caller, id uuid.UUID) (string, error)

// handleChat is the body of both endpoints: parse before the stream opens,
// then run one agent turn scoped to the subject.
func (a *App) handleChat(w http.ResponseWriter, r *http.Request, kind string, suffix suffixFn) {
	caller := auth.CallerFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	req, attachments, ok := chat.ReadInput(w, r, a.pool, a.enc, caller)
	if !ok {
		return
	}
	if len(req.Text) > maxChatTextBytes {
		http.Error(w, "message too long", http.StatusRequestEntityTooLarge)
		return
	}
	if a.agent == nil || a.enc == nil {
		http.Error(w, "chat is not configured on this server", http.StatusServiceUnavailable)
		return
	}
	if a.chatLimiter != nil {
		if !a.chatLimiter.Allow(caller.UserID) {
			http.Error(w, "too many chat requests; please wait a moment and retry", http.StatusTooManyRequests)
			return
		}
		if !a.chatLimiter.Acquire(caller.UserID) {
			http.Error(w, "too many requests in flight; please wait", http.StatusTooManyRequests)
			return
		}
		defer a.chatLimiter.Release(caller.UserID)
	}
	systemSuffix, err := suffix(r.Context(), caller, id)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	tenant, user, slackClient, err := chat.ResolveContext(r.Context(), a.pool, a.enc, caller)
	if err != nil {
		slog.Warn("posters: resolving chat context", "error", err)
		http.Error(w, "could not load your workspace", http.StatusInternalServerError)
		return
	}
	// The Slack path's setup-complete gate applies here too.
	if !tenant.SetupComplete && !caller.IsAdmin {
		http.Error(w, "Kit is still being set up; ask your admin to finish", http.StatusForbidden)
		return
	}
	sw, err := sse.New(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer sw.Close()
	emit := chat.Emitter(sw.Emit)
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("panic in poster chat", "panic", rec)
			_ = emit(chat.EventError, map[string]any{"message": "something went wrong on our side; please try again"})
		}
	}()
	_ = chat.Execute(r.Context(), chat.ExecuteInput{
		Pool: a.pool, Agent: a.agent, Slack: slackClient, Tenant: tenant, User: user,
		Text: req.Text, Attachments: attachments,
		Scope: &chat.Scope{App: AppName, Kind: kind, ID: id.String(), SystemSuffix: systemSuffix, AllowedTools: chatToolNames},
	}, emit)
}

// posterChatSuffix renders the poster's situation for the agent.
func (a *App) posterChatSuffix(ctx context.Context, caller *services.Caller, id uuid.UUID) (string, error) {
	poster, _, err := a.loadPoster(ctx, caller.TenantID, id)
	if err != nil {
		return "", err
	}
	brand, err := a.brandFor(ctx, caller.TenantID)
	if err != nil {
		return "", err
	}
	eventLine := ""
	if poster.EventID != nil {
		if evs := a.eventsService(); evs != nil {
			if ev, err := evs.Get(ctx, caller.TenantID, *poster.EventID); err == nil {
				eventLine = ev.Title + " (" + factsFor(ev).When + ")"
			}
		}
	}
	data := brandPromptData(brand)
	data["Title"] = strOr(poster.Title, "untitled")
	data["PosterID"] = poster.ID.String()
	data["EventLine"] = eventLine
	data["Format"] = brand.PortraitFormat
	data["StockAllowed"] = a.stockAllowed(ctx, caller)
	return mustRender("system_poster_chat.tmpl", data), nil
}

// templateChatSuffix renders the template's situation for the agent.
func (a *App) templateChatSuffix(ctx context.Context, caller *services.Caller, id uuid.UUID) (string, error) {
	t, err := getTemplate(ctx, a.pool, caller.TenantID, id)
	if err != nil {
		return "", err
	}
	brand, err := a.brandFor(ctx, caller.TenantID)
	if err != nil {
		return "", err
	}
	data := brandPromptData(brand)
	data["Name"] = t.Name
	data["TemplateID"] = t.ID.String()
	data["Status"] = string(t.Status)
	data["Builtin"] = t.Builtin()
	data["ImageInstructions"] = mustRender("user_image_structure.tmpl", nil)
	return mustRender("system_template_chat.tmpl", data), nil
}

func brandPromptData(b *posterrender.Brand) map[string]any {
	return map[string]any{
		"Grounds":     strings.Join(sortedKeys(b.Grounds), ", "),
		"Accents":     strings.Join(sortedKeys(b.Accents), ", "),
		"DisplayFont": b.Fonts.Display.Family,
		"TextFont":    b.Fonts.Text.Family,
		"MonoFont":    b.Fonts.Mono.Family,
	}
}

// httpErr maps the app's errors to statuses for plain (non-SSE) replies.
func (a *App) httpErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrNoBrand):
		writeErr(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "invalid: "))
	default:
		slog.Error("posters: request failed", "error", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}
