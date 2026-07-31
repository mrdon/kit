package events

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mrdon/kit/internal/apps/googlecalendar"
)

// The probe message is the whole value of the probe: it fires at the one
// moment an admin is present to act on it. Raw Google JSON in that slot is a
// dead end, so each recognised failure must name the fix.
func TestExplainProbeFailure(t *testing.T) {
	const cal = "cal@group.calendar.google.com"
	const acct = "svc@example.iam.gserviceaccount.com"

	cases := []struct {
		name     string
		err      error
		wants    []string
		notWants []string
	}{
		{
			name:     "not found blames sharing before the id",
			err:      fmt.Errorf("probe: %w", &googlecalendar.APIError{StatusCode: http.StatusNotFound, Body: `{"error":{"code":404}}`}),
			wants:    []string{cal, acct, "shared"},
			notWants: []string{`{"error"`},
		},
		{
			name:     "forbidden names the permission level",
			err:      fmt.Errorf("probe: %w", &googlecalendar.APIError{StatusCode: http.StatusForbidden, Body: "nope"}),
			wants:    []string{cal, acct, "Make changes to events"},
			notWants: []string{"nope"},
		},
		{
			// Unrecognised failures keep the raw detail: an opaque message
			// here would be worse than an ugly one.
			name:  "unknown errors keep the detail",
			err:   errors.New("connection reset"),
			wants: []string{cal, "connection reset"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := explainProbeFailure(cal, acct, tc.err)
			for _, w := range tc.wants {
				if !strings.Contains(got, w) {
					t.Errorf("message missing %q:\n%s", w, got)
				}
			}
			for _, w := range tc.notWants {
				if strings.Contains(got, w) {
					t.Errorf("message leaks raw API detail %q:\n%s", w, got)
				}
			}
		})
	}
}
