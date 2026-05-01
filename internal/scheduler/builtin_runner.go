// Package scheduler: builtin_runner.go implements the JobRunner for
// job_type='builtin' — jobs like "Sync user profiles from Slack" that
// run a native Go handler instead of the LLM agent. The actual handler
// registry lives in builtin.go (findBuiltinHandler); this file just
// adapts that registry to the JobRunner interface.
package scheduler

import (
	"context"

	"github.com/mrdon/kit/internal/models"
)

// builtinRunner dispatches job_type='builtin' rows to their native
// handlers. Wraps s.ExecuteBuiltinTask so both the claim loop and the MCP
// run_task tool (which calls the exported method directly) route through
// the same code.
type builtinRunner struct{ s *Scheduler }

func (r *builtinRunner) JobType() string { return string(models.JobTypeBuiltin) }

func (r *builtinRunner) Run(ctx context.Context, job *models.Job) error {
	r.s.ExecuteBuiltinTask(ctx, *job)
	return nil
}
