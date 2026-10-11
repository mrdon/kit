package cards

// Access and validation helpers shared by the CardService methods:
// per-card visibility for non-admins, scope-assignment enforcement, the
// write-access check, and decision option validation.

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// callerCanSee returns true if the caller's scopes include the card. Used
// for single-row Get so we don't leak existence.
func (s *CardService) callerCanSee(ctx context.Context, c *services.Caller, cardID uuid.UUID) (bool, error) {
	scopeSQL, scopeArgs := c.ScopeFilterIDs("sc", 3)
	args := []any{c.TenantID, cardID}
	args = append(args, scopeArgs...)
	query := `SELECT 1 FROM app_card_scopes acs JOIN scopes sc ON sc.id = acs.scope_id WHERE acs.tenant_id = $1 AND acs.card_id = $2 AND (` + scopeSQL + `) LIMIT 1`
	var one int
	if err := s.pool.QueryRow(ctx, query, args...).Scan(&one); err != nil {
		return false, nil //nolint:nilerr // no-rows means no access
	}
	return true, nil
}

// enforceScopeAccess prevents non-admins from scoping a card to a role
// they don't hold or to a user other than themselves. Admins are trusted.
func (s *CardService) enforceScopeAccess(c *services.Caller, roleScopes []string, userScopes []uuid.UUID) error {
	if c.IsAdmin {
		return nil
	}
	for _, role := range roleScopes {
		if !slices.Contains(c.Roles, role) {
			return services.ErrForbidden
		}
	}
	for _, uid := range userScopes {
		if uid != c.UserID {
			return services.ErrForbidden
		}
	}
	return nil
}

// canWrite is the write-access check for update / resolve / ack. Admins and
// non-admins pass as long as the card is visible — more granular write-vs-
// read split is an open question per the plan.
func canWrite(c *services.Caller, card *Card) bool {
	if c.IsAdmin {
		return true
	}
	// If the caller could see the card via Get, they can write. Read/write
	// parity is acceptable for the MVP slice.
	_ = card
	return true
}

// validateOptions checks option ids are unique and the recommended id (if
// set) matches one of the options.
func validateOptions(opts []DecisionOption, recommended string) error {
	seen := map[string]struct{}{}
	for _, o := range opts {
		if o.OptionID == "" {
			return errors.New("option.option_id is required")
		}
		if o.Label == "" {
			return fmt.Errorf("option %q: label is required", o.OptionID)
		}
		if _, dup := seen[o.OptionID]; dup {
			return fmt.Errorf("duplicate option_id %q", o.OptionID)
		}
		seen[o.OptionID] = struct{}{}
	}
	if recommended != "" {
		if _, ok := seen[recommended]; !ok {
			return fmt.Errorf("recommended_option_id %q is not in options", recommended)
		}
	}
	return nil
}
