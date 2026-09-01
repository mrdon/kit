package squaresales

import (
	"math"
	"slices"
)

// Thresholds for the whole analysis, in one block so tuning is a single
// diff and every number is visible next to its neighbours.
const (
	// baselineWindow is how many same-weekday samples back we look. Eight
	// weeks is long enough for a stable median and short enough that a
	// seasonal drift moves with it rather than being flagged forever.
	baselineWindow = 8

	// madFloorFraction floors the robust-z denominator at a fraction of
	// the median. MAD can be zero on a freakishly consistent run, which
	// would turn a $30 wobble into a six-sigma event; this says we never
	// claim to resolve better than 10% of a typical day. It also removes
	// the divide-by-zero branch.
	madFloorFraction = 0.10

	// A day must clear ALL THREE gates. Each alone misfires: the z-score
	// alone fires constantly on low-variance weekdays, the percentage
	// alone fires on every volatile Tuesday, and the absolute floor alone
	// ignores that a $200 swing means different things on different days.
	dayZThreshold        = 2.5
	dayPctThreshold      = 0.20
	dayAbsThresholdCents = 7500

	// importantZ escalates a day finding from notable to important.
	importantZ = 4.0

	// minBaselineSamples is the fewest same-weekday open days a finding
	// may be built on. Below this the median is not a baseline, it is an
	// anecdote.
	minBaselineSamples = 4

	// thinBaseline is where a finding is still allowed but must say so and
	// may not escalate to important.
	thinBaseline = 7
)

// median returns the median of xs. The input is copied before sorting, so
// the caller's slice is never reordered. Empty returns 0.
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

// mad returns the median absolute deviation of xs about med.
func mad(xs []float64, med float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	dev := make([]float64, len(xs))
	for i, x := range xs {
		dev[i] = math.Abs(x - med)
	}
	return median(dev)
}

// Baseline is the robust summary of one comparison window.
//
// Median and MAD rather than mean and standard deviation, and not as a
// matter of taste: this tenant's August Saturdays were $2725, $724, $935
// and $807. The mean is dragged to $1298 by the one big day and the
// standard deviation inflates to ~$950, so nothing would ever flag again --
// the single genuinely anomalous day destroys the detector. The median
// ($871) is unmoved by it.
type Baseline struct {
	N      int
	Median float64
	Scale  float64 // max(MAD, madFloorFraction*Median): the robust-z denominator
}

// newBaseline builds a Baseline from the most recent baselineWindow
// samples. ok is false when there are fewer than minBaselineSamples.
func newBaseline(samples []float64) (Baseline, bool) {
	if len(samples) > baselineWindow {
		samples = samples[len(samples)-baselineWindow:]
	}
	if len(samples) < minBaselineSamples {
		return Baseline{N: len(samples)}, false
	}
	med := median(samples)
	scale := math.Max(mad(samples, med), madFloorFraction*med)
	if scale <= 0 {
		scale = 1 // every sample was zero; any deviation is undefined, not infinite
	}
	return Baseline{N: len(samples), Median: med, Scale: scale}, true
}

// robustZ is the modified z-score of v: 0.6745*(v-Median)/Scale. The
// constant makes it comparable to a standard z-score for normal data.
//
// v is in DOLLARS, as are Median and Scale — the *Cents thresholds convert
// at the comparison rather than the statistics working in two units.
func (b Baseline) robustZ(v float64) float64 {
	if b.Scale == 0 {
		return 0
	}
	return 0.6745 * (v - b.Median) / b.Scale
}

// pctDelta is (v - Median) / Median, 0 when the median is 0.
func (b Baseline) pctDelta(v float64) float64 {
	if b.Median == 0 {
		return 0
	}
	return (v - b.Median) / b.Median
}

// thin reports whether this baseline is usable but too small to trust
// fully -- findings built on it are capped at notable and say so.
func (b Baseline) thin() bool { return b.N < thinBaseline }

// fires reports whether a deviation clears all three day-level gates.
func (b Baseline) fires(v float64) bool {
	return math.Abs(b.robustZ(v)) >= dayZThreshold &&
		math.Abs(b.pctDelta(v)) >= dayPctThreshold &&
		math.Abs(v-b.Median)*100 >= dayAbsThresholdCents
}
