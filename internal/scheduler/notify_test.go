package scheduler

import (
	"strings"
	"testing"

	"github.com/mrdon/kit/internal/models"
)

func TestJobAlertOncePerFailure(t *testing.T) {
	boom, other := "boom", "something else"
	cases := []struct {
		name          string
		prev, current *string
		want          jobAlert
	}{
		{"starts failing", nil, &boom, jobAlertFailed},
		{"keeps failing", &boom, &boom, jobAlertNone},
		{"fails differently", &boom, &other, jobAlertNone},
		{"recovers", &boom, nil, jobAlertRecovered},
		{"keeps working", nil, nil, jobAlertNone},
	}
	for _, c := range cases {
		if got := jobAlertFor(c.prev, c.current); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestJobAlertTextTrimsLongErrors(t *testing.T) {
	job := models.Job{Description: "Sync things"}
	text := jobAlertText(jobAlertFailed, job, strings.Repeat("x", maxAlertError*2))
	if len(text) > maxAlertError+200 {
		t.Errorf("alert is %d bytes; a long error should be trimmed", len(text))
	}
	if !strings.Contains(text, "Sync things") {
		t.Errorf("alert should name the job: %q", text)
	}
}
