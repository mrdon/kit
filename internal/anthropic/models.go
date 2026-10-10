package anthropic

import "sync/atomic"

// Compiled-in Claude model IDs, one per tier. Kit starts on these and
// never waits on the API to learn better ones: ResolveLatestInBackground
// moves each tier to the newest release the Models API lists once it
// answers, so a new model is picked up without an edit here. Keep these
// current anyway, for an offline start and for tests. Latest as of
// 2026-10: the 5.5 family.
const (
	DefaultOpus   = "claude-opus-5-5"
	DefaultSonnet = "claude-sonnet-5-5"
	DefaultHaiku  = "claude-haiku-5-5"
)

type modelSet struct {
	opus, sonnet, haiku string
}

var current atomic.Pointer[modelSet]

func init() {
	current.Store(&modelSet{opus: DefaultOpus, sonnet: DefaultSonnet, haiku: DefaultHaiku})
}

// ModelOpus, ModelSonnet and ModelHaiku are the IDs to send right now.
func ModelOpus() string   { return current.Load().opus }
func ModelSonnet() string { return current.Load().sonnet }
func ModelHaiku() string  { return current.Load().haiku }

// SetModels replaces the IDs in one step. Tests use it; production goes
// through ResolveLatestInBackground.
func SetModels(opus, sonnet, haiku string) {
	current.Store(&modelSet{opus: opus, sonnet: sonnet, haiku: haiku})
}
