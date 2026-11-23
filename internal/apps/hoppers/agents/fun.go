package agents

import (
	"math"
	"slices"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
)

// FunStat is what one race did for the "fun" metrics (cmd/hopsim -fun). A lap
// is one turn for every frog; halfway is the middle hop of the race.
type FunStat struct {
	LapSpreads     []int // spots from last frog to first at the end of each full lap
	LapLast        []int // where the last frog was at each lap end
	UnderdogWon    bool  // the winner was last or second-to-last at halfway
	Stragglers     int   // frogs more than a third of the track behind the leader at halfway
	StragglerTurns int   // own turns (all frogs) started more than a third of the track behind
	Runaway        bool  // at some lap end the leader was more than a third of the track ahead of second
	Lap1LeaderWon  bool  // the leader after the first lap won
	LateLeadChange bool  // the lead changed hands in the last lap's worth of hops
	Turnarounds    int   // times a frog turned to go backwards
	LongBack       int   // ...that became 3+ backward hops in a row
	FrogsLongBack  int   // frogs with such a streak this race
	Stuck          []int // per frog: longest run of its own turns with no net forward progress
}

// trace follows every frog through one race.
type trace struct {
	frogs    int
	length   int
	idx      map[string]int
	pos      []int   // where each frog is now
	ranks    [][]int // frog indexes first to last, after each hop
	posHist  [][]int // every frog's position after each hop
	changed  []bool  // the lead changed on this hop
	lapLead  []int   // leader after each full lap
	anchor   []int   // stuck: position when the current run started
	run      []int   // stuck: own turns since the frog last got past anchor
	streak   []int   // current backward streak, in own hops
	counted  []bool  // this streak already counted as long
	longFrog []bool
	fun      FunStat
}

func newTrace(ranking []string, length int) *trace {
	n := len(ranking)
	t := &trace{frogs: n, length: length, idx: map[string]int{}, pos: make([]int, n), anchor: make([]int, n),
		run: make([]int, n), streak: make([]int, n), counted: make([]bool, n), longFrog: make([]bool, n)}
	t.fun.Stuck = make([]int, n)
	for i, f := range ranking {
		t.idx[f] = i
	}
	return t
}

// far is more than a third of the track.
func (t *trace) far(gap int) bool { return float64(gap) > float64(t.length)/3 }

func (t *trace) hop(st engine.State, leadChanged bool) {
	h := st.LastHop
	f, ok := t.idx[h.Frog]
	if !ok {
		return
	}
	if t.far(slices.Max(t.pos) - t.pos[f]) {
		t.fun.StragglerTurns++
	}
	for s, stack := range st.Stacks {
		for _, id := range stack {
			if i, ok := t.idx[id]; ok {
				t.pos[i] = s
			}
		}
	}
	rank := make([]int, 0, t.frogs)
	for _, id := range st.Ranking {
		if i, ok := t.idx[id]; ok {
			rank = append(rank, i)
		}
	}
	t.ranks = append(t.ranks, rank)
	t.changed = append(t.changed, leadChanged)
	t.stuck(f)
	t.backStreak(f, h)
	if len(t.ranks)%t.frogs == 0 && len(rank) > 1 {
		t.fun.LapSpreads = append(t.fun.LapSpreads, t.pos[rank[0]]-slices.Min(t.pos))
		t.fun.LapLast = append(t.fun.LapLast, slices.Min(t.pos))
		t.lapLead = append(t.lapLead, rank[0])
		if t.far(t.pos[rank[0]] - t.pos[rank[1]]) {
			t.fun.Runaway = true
		}
	}
	t.posHist = append(t.posHist, slices.Clone(t.pos))
}

// finish works out the whole-race numbers once the winner is known.
func (t *trace) finish(winner string) FunStat {
	w, ok := t.idx[winner]
	if !ok || len(t.ranks) == 0 {
		return t.fun
	}
	mid := len(t.ranks) / 2
	if r := t.ranks[mid]; len(r) >= 2 {
		t.fun.UnderdogWon = slices.Index(r, w) >= len(r)-2
	}
	lead := slices.Max(t.posHist[mid])
	for _, p := range t.posHist[mid] {
		if t.far(lead - p) {
			t.fun.Stragglers++
		}
	}
	if len(t.lapLead) > 0 {
		t.fun.Lap1LeaderWon = t.lapLead[0] == w
	}
	t.fun.LateLeadChange = slices.Contains(t.changed[max(0, len(t.changed)-t.frogs):], true)
	return t.fun
}

// bandSkill is a seat's hit rate on a band of width w, for a seat that hits
// a band of width base at rate p: aim lands Normal(centre, sigma), so
// p = erf(base/(2*sqrt2*sigma)) and the rate scales inside the erf.
func bandSkill(p, base, w float64) float64 {
	if p <= 0 || p >= 1 || w == base || base <= 0 {
		return p
	}
	return math.Erf(math.Erfinv(p) * w / base)
}

// stuck counts f's own turns since it last got further than where the run
// started.
func (t *trace) stuck(f int) {
	if t.pos[f] > t.anchor[f] {
		t.anchor[f], t.run[f] = t.pos[f], 0
		return
	}
	t.run[f]++
	t.fun.Stuck[f] = max(t.fun.Stuck[f], t.run[f])
}

func (t *trace) backStreak(f int, h *engine.HopInfo) {
	if !h.Back {
		t.streak[f] = 0
		return
	}
	if h.Turned {
		t.fun.Turnarounds++
		t.streak[f], t.counted[f] = 0, false
	}
	t.streak[f]++
	if t.streak[f] >= 3 && !t.counted[f] {
		t.counted[f] = true
		t.fun.LongBack++
		if !t.longFrog[f] {
			t.longFrog[f] = true
			t.fun.FrogsLongBack++
		}
	}
}
