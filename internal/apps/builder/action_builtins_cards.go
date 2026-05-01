// Package builder: action_builtins_cards.go dispatches create_decision /
// create_briefing into CardService, and create_task into JobService.
//
// The tool schema the LLM sees uses "context" as the decision body field;
// for scripts we normalise to "body" across both decision and briefing
// to keep the naming symmetrical — admins writing Python shouldn't have
// to memorise which card kind uses which key.
package builder

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
	"github.com/mrdon/kit/internal/apps/cards"
	"github.com/mrdon/kit/internal/services"
)

// dispatchCreateDecision handles create_decision(title, body, options,
// priority="medium", role_scopes=None) → card dict.
//
// options is a list of dicts: [{"label": str, "prompt": str}]. Monty
// doesn't hand us an option_id field — we synthesise one from the 1-based
// index so translator logic stays stable even if scripts rearrange the
// list across runs.
func dispatchCreateDecision(ctx context.Context, a *ActionBuiltins, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
	title, err := argString(call.Args, "title")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	body, err := argString(call.Args, "body")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	rawOptions, ok := call.Args["options"]
	if !ok || rawOptions == nil {
		return nil, fmt.Errorf("%s: missing required argument %q", call.Name, "options")
	}
	optionList, ok := rawOptions.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: argument %q must be a list, got %T", call.Name, "options", rawOptions)
	}
	priority, err := argOptionalString(call.Args, "priority")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	if priority == "" {
		priority = string(cards.DecisionPriorityMedium)
	}
	roleScopes, err := argOptionalStringList(call.Args, "role_scopes")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	options, err := parseDecisionOptions(optionList)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	c, err := deps.caller(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	card, err := deps.cardsSvc.CreateDecision(ctx, c, cards.CardCreateInput{
		Title:      title,
		Body:       body,
		RoleScopes: roleScopes,
		Decision: &cards.DecisionCreateInput{
			Priority: cards.DecisionPriority(priority),
			Options:  options,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	a.insertCount++
	return cardToMap(card), nil
}

// dispatchCreateBriefing handles create_briefing(title, body,
// severity="info", role_scopes=None) → card dict.
func dispatchCreateBriefing(ctx context.Context, a *ActionBuiltins, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
	title, err := argString(call.Args, "title")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	body, err := argString(call.Args, "body")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	severity, err := argOptionalString(call.Args, "severity")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	if severity == "" {
		severity = string(cards.BriefingSeverityInfo)
	}
	roleScopes, err := argOptionalStringList(call.Args, "role_scopes")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	c, err := deps.caller(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	card, err := deps.cardsSvc.CreateBriefing(ctx, c, cards.CardCreateInput{
		Title:      title,
		Body:       body,
		RoleScopes: roleScopes,
		Briefing:   &cards.BriefingCreateInput{Severity: cards.BriefingSeverity(severity)},
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	a.insertCount++
	return cardToMap(card), nil
}

// dispatchCreateTask handles create_task(description, cron,
// timezone="UTC", channel=None, run_once=False) → job dict.
//
// For v0.1 the Phase-3 signature exposes one-shot jobs via run_once=True
// paired with a run_at implied from "now" — scripts that need a specific
// future time can add their own timestamp builder later. cron is passed
// through verbatim so admins who already think in cron (the agent surface
// also uses cron_expr) don't need to re-learn a shape.
func dispatchCreateTask(ctx context.Context, a *ActionBuiltins, deps *actionDeps, call *runtime.FunctionCall) (any, error) {
	description, err := argString(call.Args, "description")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	cron, err := argOptionalString(call.Args, "cron")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	tz, err := argOptionalString(call.Args, "timezone")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	if tz == "" {
		tz = "UTC"
	}
	channel, err := argOptionalString(call.Args, "channel")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	runOnce, err := argOptionalBool(call.Args, "run_once")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	if !runOnce && cron == "" {
		return nil, fmt.Errorf("%s: cron is required when run_once is false", call.Name)
	}

	c, err := deps.caller(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}

	var runAt *time.Time
	if runOnce {
		now := time.Now()
		runAt = &now
	}

	job, err := deps.svc.Jobs.Create(ctx, c, services.CreateInput{
		Description: description,
		CronExpr:    cron,
		Timezone:    tz,
		ChannelID:   channel,
		RunOnce:     runOnce,
		RunAt:       runAt,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	a.insertCount++
	return map[string]any{
		"id":          job.ID.String(),
		"description": job.Description,
		"cron_expr":   job.CronExpr,
		"timezone":    job.Timezone,
		"run_once":    job.RunOnce,
		"channel_id":  job.ChannelID,
	}, nil
}

// parseDecisionOptions converts the incoming list of Python dicts into
// DecisionOption structs. Each option picks up a 1-based option_id unless
// the script passed an explicit id — we treat a string "option_id" field
// as an override so power users can set stable ids.
func parseDecisionOptions(list []any) ([]cards.DecisionOption, error) {
	if len(list) == 0 {
		return nil, errors.New("options must be a non-empty list")
	}
	out := make([]cards.DecisionOption, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("options[%d] must be a dict, got %T", i, item)
		}
		label, _ := m["label"].(string)
		if label == "" {
			return nil, fmt.Errorf("options[%d].label is required", i)
		}
		prompt, _ := m["prompt"].(string)
		id, _ := m["option_id"].(string)
		if id == "" {
			id = fmt.Sprintf("opt-%d", i+1)
		}
		out = append(out, cards.DecisionOption{
			OptionID:  id,
			SortOrder: i,
			Label:     label,
			Prompt:    prompt,
		})
	}
	return out, nil
}

// cardToMap flattens a *cards.Card into a script-friendly map. The
// nested Decision / Briefing sub-structs unpack alongside the top-level
// fields so scripts don't have to reach into sub-objects to grab
// priority / severity / options.
func cardToMap(card *cards.Card) map[string]any {
	if card == nil {
		return nil
	}
	out := map[string]any{
		"id":        card.ID.String(),
		"tenant_id": card.TenantID.String(),
		"kind":      string(card.Kind),
		"title":     card.Title,
		"body":      card.Body,
		"state":     string(card.State),
	}
	if card.Decision != nil {
		out["priority"] = string(card.Decision.Priority)
		opts := make([]any, 0, len(card.Decision.Options))
		for _, o := range card.Decision.Options {
			opts = append(opts, map[string]any{
				"option_id": o.OptionID,
				"label":     o.Label,
				"prompt":    o.Prompt,
			})
		}
		out["options"] = opts
	}
	if card.Briefing != nil {
		out["severity"] = string(card.Briefing.Severity)
	}
	return out
}
