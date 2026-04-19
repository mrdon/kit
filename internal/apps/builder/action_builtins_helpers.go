// Package builder: action_builtins_helpers.go collects the extra arg-
// coercion shims action dispatchers need on top of what db_builtins.go
// already provides. Keeping them in a separate file keeps the main
// action_builtins.go focused on dispatch wiring.
package builder

import (
	"fmt"
	"time"
)

// argOptionalString extracts a string kwarg, returning "" when absent /
// None. Empty string is allowed (some callers use it as "clear this").
func argOptionalString(args map[string]any, name string) (string, error) {
	raw, ok := args[name]
	if !ok || raw == nil {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("argument %q must be a string or None, got %T", name, raw)
	}
	return s, nil
}

// argOptionalBool extracts a bool kwarg, defaulting to false when absent.
func argOptionalBool(args map[string]any, name string) (bool, error) {
	raw, ok := args[name]
	if !ok || raw == nil {
		return false, nil
	}
	b, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("argument %q must be a bool or None, got %T", name, raw)
	}
	return b, nil
}

// argOptionalStringList extracts a list-of-strings kwarg. Returns nil
// when absent. Non-string items in a present list are a hard error so
// script authors get an immediate signal instead of a silent coercion.
func argOptionalStringList(args map[string]any, name string) ([]string, error) {
	raw, ok := args[name]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("argument %q must be a list of strings or None, got %T", name, raw)
	}
	out := make([]string, 0, len(list))
	for i, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("argument %q[%d] must be a string, got %T", name, i, item)
		}
		out = append(out, s)
	}
	return out, nil
}

// argOptionalDate parses a YYYY-MM-DD string into a *time.Time; absent /
// None / empty returns nil. Mirrors the agent-tool date parser so the
// shape is familiar to admins who have seen the LLM emit due_date.
func argOptionalDate(args map[string]any, name string) (*time.Time, error) {
	raw, ok := args[name]
	if !ok || raw == nil {
		return nil, nil //nolint:nilnil // intended: absent means no date
	}
	s, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("argument %q must be a date string or None, got %T", name, raw)
	}
	if s == "" {
		return nil, nil //nolint:nilnil // intended: absent means no date
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, fmt.Errorf("argument %q must be YYYY-MM-DD, got %q", name, s)
	}
	return &t, nil
}
