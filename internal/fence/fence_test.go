package fence

import (
	"testing"
	"time"

	"github.com/mrdon/kit/internal/models"
)

// A superseded process pauses at once, resumes if the newer build goes away,
// and exits only after it has stayed superseded for retireAfter.
func TestObservePausesThenRetires(t *testing.T) {
	retired := 0
	f := &Fence{retire: func() { retired++ }}
	newer := &models.Instance{Build: "newer"}
	start := time.Now()

	if f.observe(newer, start) || f.Active() {
		t.Fatal("did not pause for a newer build")
	}
	if f.observe(nil, start.Add(time.Minute)) || !f.Active() {
		t.Fatal("did not resume once the newer build was gone")
	}

	// The clock restarts after a resume: a deploy that failed its
	// healthcheck must not count toward retiring the old container.
	f.observe(newer, start.Add(2*time.Minute))
	if f.observe(newer, start.Add(2*time.Minute+retireAfter-time.Second)) || retired != 0 {
		t.Fatal("retired before the grace ran out")
	}
	if !f.observe(newer, start.Add(2*time.Minute+retireAfter)) || retired != 1 {
		t.Fatalf("retired %d times at the end of the grace, want 1", retired)
	}
}

func TestNilFenceIsActive(t *testing.T) {
	var f *Fence
	if !f.Active() {
		t.Fatal("nil fence blocked work")
	}
}
