package task

import (
	"context"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/cards/shared"
	"github.com/mrdon/kit/internal/services"
)

// The stack endpoint finds view providers by type assertion, so a dropped
// method would quietly empty the All tasks view rather than fail the build.
var _ apps.ViewCardProvider = (*cardProvider)(nil)

// ViewItems answers the PWA's All tasks view: the same personal scope as
// the feed (assigned to the caller, or unassigned in a role they hold),
// without the urgency filter, so work that isn't due yet can be swiped
// through too. The cards and their actions are the feed's own, so swipe to
// complete, swipe to snooze and tap for detail behave the same in both.
func (p *cardProvider) ViewItems(ctx context.Context, caller *services.Caller, view shared.StackView, limit int) (shared.StackPage, bool, error) {
	if view != shared.ViewTasks {
		return shared.StackPage{}, false, nil
	}
	page, err := p.taskPage(ctx, caller, stackAllOpen, limit)
	if err != nil {
		return shared.StackPage{}, true, err
	}
	return page, true, nil
}
