package netlify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ChangeThread groups successive agent_runs for one Slack thread.
// Created lazily on first netlify_request_change call in a thread.
type ChangeThread struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	SlackChannel     string
	SlackThreadTS    string
	State            string // active | shipped | abandoned
	ProductionBranch string
	LatestAgentRunID *uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// AgentRun is one round-trip to Netlify Agent Runners. Mirrors the
// upstream AgentRunner record we care about plus our own grouping +
// summary fields.
type AgentRun struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	ChangeThreadID uuid.UUID
	NetlifyRunID   string
	Prompt         string
	BaseBranch     string
	ResultBranch   string
	PreviewURL     string
	State          string
	Summary        string
	ResultDiff     string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DoneAt         *time.Time
}

// EnsureChangeThread finds or creates the change-thread for a given
// Slack thread. Idempotent — subsequent calls with the same key
// return the existing row.
func EnsureChangeThread(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	slackChannel, slackThreadTS, productionBranch string,
) (*ChangeThread, error) {
	if slackChannel == "" || slackThreadTS == "" {
		return nil, errors.New("slack channel + thread_ts required to track a change thread")
	}
	const q = `
        INSERT INTO app_netlify_change_threads (
            tenant_id, slack_channel, slack_thread_ts, production_branch
        )
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (tenant_id, slack_channel, slack_thread_ts)
            DO UPDATE SET updated_at = now()
        RETURNING id, tenant_id, slack_channel, slack_thread_ts, state,
                  production_branch, latest_agent_run_id, created_at, updated_at`
	var ct ChangeThread
	err := pool.QueryRow(ctx, q, tenantID, slackChannel, slackThreadTS, productionBranch).Scan(
		&ct.ID, &ct.TenantID, &ct.SlackChannel, &ct.SlackThreadTS, &ct.State,
		&ct.ProductionBranch, &ct.LatestAgentRunID, &ct.CreatedAt, &ct.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upserting change thread: %w", err)
	}
	return &ct, nil
}

// GetChangeThread fetches an existing thread by id.
func GetChangeThread(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (*ChangeThread, error) {
	const q = `
        SELECT id, tenant_id, slack_channel, slack_thread_ts, state,
               production_branch, latest_agent_run_id, created_at, updated_at
        FROM app_netlify_change_threads
        WHERE id = $1`
	var ct ChangeThread
	err := pool.QueryRow(ctx, q, id).Scan(
		&ct.ID, &ct.TenantID, &ct.SlackChannel, &ct.SlackThreadTS, &ct.State,
		&ct.ProductionBranch, &ct.LatestAgentRunID, &ct.CreatedAt, &ct.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil
		}
		return nil, fmt.Errorf("loading change thread: %w", err)
	}
	return &ct, nil
}

// CreateAgentRun inserts a new agent_run row right after Netlify
// accepts the run. Returns the freshly-created row.
func CreateAgentRun(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, changeThreadID uuid.UUID,
	netlifyRunID, prompt, baseBranch, previewURL, state string,
) (*AgentRun, error) {
	const q = `
        INSERT INTO app_netlify_agent_runs (
            tenant_id, change_thread_id, netlify_run_id,
            prompt, base_branch, preview_url, state
        )
        VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7)
        RETURNING id, created_at, updated_at`
	var run AgentRun
	run.TenantID = tenantID
	run.ChangeThreadID = changeThreadID
	run.NetlifyRunID = netlifyRunID
	run.Prompt = prompt
	run.BaseBranch = baseBranch
	run.PreviewURL = previewURL
	run.State = state
	err := pool.QueryRow(ctx, q,
		tenantID, changeThreadID, netlifyRunID,
		prompt, baseBranch, previewURL, state,
	).Scan(&run.ID, &run.CreatedAt, &run.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("inserting agent_run: %w", err)
	}
	// Point change_thread at the new run.
	if _, err := pool.Exec(ctx,
		`UPDATE app_netlify_change_threads SET latest_agent_run_id = $2, updated_at = now()
         WHERE id = $1`, changeThreadID, run.ID); err != nil {
		return nil, fmt.Errorf("updating change_thread latest: %w", err)
	}
	return &run, nil
}

// UpdateAgentRunProgress writes per-poll updates from Netlify. Called
// by the watcher after each fetch. Doesn't touch summary/result_diff
// — those are stamped separately when we have them.
func UpdateAgentRunProgress(
	ctx context.Context,
	pool *pgxpool.Pool,
	runID uuid.UUID,
	state, resultBranch, previewURL string,
	doneAt *time.Time,
) error {
	const q = `
        UPDATE app_netlify_agent_runs SET
            state         = $2,
            result_branch = COALESCE(NULLIF($3, ''), result_branch),
            preview_url   = COALESCE(NULLIF($4, ''), preview_url),
            done_at       = COALESCE($5, done_at),
            updated_at    = now()
        WHERE id = $1`
	if _, err := pool.Exec(ctx, q, runID, state, resultBranch, previewURL, doneAt); err != nil {
		return fmt.Errorf("updating agent_run progress: %w", err)
	}
	return nil
}
