// Package scheduler: builtin_runner.go implements the TaskRunner for
// task_type='builtin' — tasks like "Sync user profiles from Slack" that
// run a native Go handler instead of the LLM agent. The actual handler
// registry lives in builtin.go (findBuiltinHandler); this file just
// adapts that registry to the TaskRunner interface.
package scheduler

import (
	"context"

	"github.com/mrdon/kit/internal/models"
)

// builtinRunner dispatches task_type='builtin' rows to their native
// handlers. Wraps s.ExecuteBuiltinTask so both the claim loop and the MCP
// run_task tool (which calls the exported method directly) route through
// the same code.
type builtinRunner struct{ s *Scheduler }

func (r *builtinRunner) TaskType() string { return string(models.TaskTypeBuiltin) }

func (r *builtinRunner) Run(ctx context.Context, task *models.Task) error {
	r.s.ExecuteBuiltinTask(ctx, *task)
	return nil
}
