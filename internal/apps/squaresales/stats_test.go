package squaresales

import (
	"math"
	"os"
	"testing"
)

// Every figure below is real net sales from the live account. Saturdays,
// 2026-07-04 through 2026-08-29:
//
//	Jul 4   379.12
//	Jul 11  794.10
//	Jul 18  603.89
//	Jul 25  673.70
//	Aug 1   849.98
//	Aug 8  2725.09   <- a beer festival, or something like it
//	Aug 15  723.58
//	Aug 22  935.38
//	Aug 29  807.01
var (
	saturdaysBeforeAug8  = []float64{379.12, 794.10, 603.89, 673.70, 849.98}
	saturdaysBeforeAug29 = []float64{379.12, 794.10, 603.89, 673.70, 849.98, 2725.09, 723.58, 935.38}
)

func TestMedian(t *testing.T) {
	if got := median([]float64{3, 1, 2}); got != 2 {
		t.Errorf("odd median = %v, want 2", got)
	}
	if got := median([]float64{4, 1, 3, 2}); got != 2.5 {
		t.Errorf("even median = %v, want 2.5", got)
	}
	if got := median(nil); got != 0 {
		t.Errorf("empty median = %v, want 0", got)
	}
	// The caller's slice must not be reordered.
	in := []float64{3, 1, 2}
	_ = median(in)
	if in[0] != 3 {
		t.Errorf("median mutated its input: %v", in)
	}
}

// The whole reason for median/MAD. Aug 8 did 3.2x a normal Saturday; with
// a mean/stddev detector that single day inflates sigma so far that nothing
// ever flags again.
func TestOutlierDoesNotDestroyTheDetector(t *testing.T) {
	b, ok := newBaseline(saturdaysBeforeAug29)
	if !ok {
		t.Fatal("8 samples should build a baseline")
	}
	// A mean/stddev detector would put the centre near $973 and sigma near
	// $700. The median is unmoved by the outlier.
	if b.Median < 700 || b.Median > 820 {
		t.Errorf("median = %.2f, want a typical Saturday (~759), not one dragged by Aug 8", b.Median)
	}
	mean := 0.0
	for _, v := range saturdaysBeforeAug29 {
		mean += v
	}
	mean /= float64(len(saturdaysBeforeAug29))
	if math.Abs(mean-b.Median) < 150 {
		t.Errorf("mean %.2f and median %.2f too close; fixture no longer demonstrates the problem", mean, b.Median)
	}
}

// Aug 8 must flag against the Saturdays that preceded it.
func TestAug8Fires(t *testing.T) {
	b, ok := newBaseline(saturdaysBeforeAug8)
	if !ok {
		t.Fatal("5 samples should build a baseline")
	}
	if !b.fires(2725.09) {
		t.Errorf("Aug 8 ($2725.09 vs median $%.2f) should fire; z=%.2f pct=%.2f",
			b.Median, b.robustZ(2725.09), b.pctDelta(2725.09))
	}
	if !b.thin() {
		t.Error("5 samples should count as a thin baseline")
	}
}

// Aug 29 was an ordinary Saturday and must stay quiet. A detector that
// flags this is worse than no detector -- it is the "doesn't show much
// useful" failure in a new costume.
func TestAug29StaysQuiet(t *testing.T) {
	b, ok := newBaseline(saturdaysBeforeAug29)
	if !ok {
		t.Fatal("8 samples should build a baseline")
	}
	if b.fires(807.01) {
		t.Errorf("Aug 29 ($807.01 vs median $%.2f) should NOT fire; z=%.2f pct=%.2f",
			b.Median, b.robustZ(807.01), b.pctDelta(807.01))
	}
	if b.thin() {
		t.Error("8 samples should not be thin")
	}
}

// All three gates are required. This Sunday cleared the percentage and
// absolute gates but not the z gate, which is precisely the case that would
// spam the card if any single gate were used alone.
func TestAllThreeGatesRequired(t *testing.T) {
	sundays := []float64{688.38, 583.69, 779.51, 641.97} // real, 2026-08-02..30
	b, ok := newBaseline(sundays)
	if !ok {
		t.Fatal("4 samples should build a baseline")
	}
	const target = 869.06 // real: Sunday 2026-08-23
	if math.Abs(b.pctDelta(target)) < dayPctThreshold {
		t.Fatal("fixture no longer clears the percentage gate")
	}
	if math.Abs(target-b.Median)*100 < dayAbsThresholdCents {
		t.Fatal("fixture no longer clears the absolute gate")
	}
	if math.Abs(b.robustZ(target)) >= dayZThreshold {
		t.Fatal("fixture no longer fails the z gate; pick another day")
	}
}

func TestBaselineNeedsMinimumSamples(t *testing.T) {
	if _, ok := newBaseline([]float64{100, 200, 300}); ok {
		t.Error("3 samples should not build a baseline")
	}
	if _, ok := newBaseline(nil); ok {
		t.Error("no samples should not build a baseline")
	}
}

// MAD is zero when every sample is identical; the floor stops that turning
// a trivial wobble into an infinite z-score.
func TestMADFloorPreventsHairTrigger(t *testing.T) {
	b, ok := newBaseline([]float64{800, 800, 800, 800, 800})
	if !ok {
		t.Fatal("should build")
	}
	if b.Scale != 80 {
		t.Errorf("Scale = %v, want the 10%% floor (80)", b.Scale)
	}
	if b.fires(810) {
		t.Error("a $10 move on an $800 day must not fire")
	}
}

// Only the most recent baselineWindow samples count, so a seasonal drift
// moves the baseline with it instead of flagging forever.
func TestBaselineWindowTrims(t *testing.T) {
	many := make([]float64, 0, 20)
	for range 12 {
		many = append(many, 100)
	}
	for range 8 {
		many = append(many, 900)
	}
	b, ok := newBaseline(many)
	if !ok {
		t.Fatal("should build")
	}
	if b.N != baselineWindow || b.Median != 900 {
		t.Errorf("N=%d median=%v, want %d and 900 (recent samples only)", b.N, b.Median, baselineWindow)
	}
}

// readSource loads a file from this package for contract assertions.
func readSource(name string) (string, error) {
	b, err := os.ReadFile(name)
	return string(b), err
}
