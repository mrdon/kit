package task

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mrdon/kit/internal/services"
)

// mapTaskError maps a TaskService error to a user-facing message and the
// HTTP status that fits it. Shared by the MCP handlers (which use only the
// message) and the console HTTP handlers (which use both) so the two
// surfaces give identical wording — the CLAUDE.md tool-parity rule.
//
// handled is false for errors that aren't part of the known set; callers
// should treat those as unexpected (MCP returns the raw error, HTTP 500s).
// roleName is only used for the ErrInvalidRole message.
func mapTaskError(err error, caller *services.Caller, roleName string) (msg string, status int, handled bool) {
	switch {
	case errors.Is(err, ErrPrimaryRoleNotSet):
		return primaryRoleNotSetMessage(caller), http.StatusBadRequest, true
	case errors.Is(err, services.ErrForbidden):
		return "Permission denied.", http.StatusForbidden, true
	case errors.Is(err, ErrInvalidRole):
		return fmt.Sprintf("Role %q does not exist. Use list_roles to see available roles.", roleName), http.StatusBadRequest, true
	case errors.Is(err, ErrInvalidPriority):
		return fmt.Sprintf("Invalid priority. Use one of: %s.", strings.Join(Priorities, ", ")), http.StatusBadRequest, true
	case errors.Is(err, services.ErrNotFound):
		return "Task not found.", http.StatusNotFound, true
	}
	return "", 0, false
}

// parseYMD parses a YYYY-MM-DD date, returning a friendly error message
// (empty on success) for the named field. Shared so MCP and HTTP reject
// bad dates identically.
func parseYMD(field, value string) (*time.Time, string) {
	d, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, fmt.Sprintf("Invalid %s format. Use YYYY-MM-DD.", field)
	}
	return &d, ""
}
