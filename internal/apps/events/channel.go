package events

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// A channel is a place events get promoted to. See migration 087 for why
// channels are rows rather than code, and why only `automated` needs a Go
// connector behind it.

// ChannelMode is how work reaches a destination.
type ChannelMode string

const (
	// ChannelManual means a human fills in their form. Generates checklist
	// items. Every channel starts here.
	ChannelManual ChannelMode = "manual"
	// ChannelSubscribed means they pull our ICS feed. Generates nothing, ever.
	// This is the win condition -- it retires a chore rather than speeding it
	// up -- and also the only mode that can fail silently, which is what
	// VerifiedAt is for.
	ChannelSubscribed ChannelMode = "subscribed"
	// ChannelAutomated means Kit posts through an API. Requires Connector.
	ChannelAutomated ChannelMode = "automated"
)

func ValidChannelMode(m ChannelMode) bool {
	return m == ChannelManual || m == ChannelSubscribed || m == ChannelAutomated
}

// StepKind distinguishes the three rhythms a campaign step can have. They
// differ mainly in what happens when one is missed, which is the interesting
// part -- see StepDue.
type StepKind string

const (
	// StepOneshot is do-it-once: submit to the chamber, create the annual
	// Facebook recurring event. Stays outstanding until done or the event
	// passes.
	StepOneshot StepKind = "oneshot"
	// StepDrip is a timed beat offset from the event date, and it EXPIRES. A
	// "one week out" reminder is worthless three days before; leaving it on
	// the list turns the page into a guilt ledger.
	StepDrip StepKind = "drip"
	// StepCadence is for recurring series only: "post about trivia" every few
	// weeks, anchored to when it was last done rather than to a calendar
	// grid. You do not announce trivia weekly, but you do want people
	// reminded it exists.
	StepCadence StepKind = "cadence"
)

func ValidStepKind(k StepKind) bool {
	return k == StepOneshot || k == StepDrip || k == StepCadence
}

// Step is one beat of a channel's campaign, stored in the channel's steps
// JSONB. Written by presets in the admin UI, not hand-authored.
type Step struct {
	Key   string   `json:"key"`
	Label string   `json:"label"`
	Kind  StepKind `json:"kind"`

	// OffsetDays is how many days BEFORE the event a drip step is due, as a
	// positive number. Ignored by other kinds.
	OffsetDays int `json:"offset_days,omitempty"`
	// IntervalDays is how often a cadence step recurs. Ignored by other kinds.
	IntervalDays int `json:"interval_days,omitempty"`
	// ExpiresAfterDays is how long a drip step stays actionable once due. Zero
	// means it stays until the event itself passes.
	ExpiresAfterDays int `json:"expires_after_days,omitempty"`

	// MinProminence is this step's own floor, on top of the channel's. It is
	// what lets one channel take the announce for a normal event and the full
	// drip only for a featured one.
	MinProminence Prominence `json:"min_prominence,omitempty"`

	// Automatable is false for work no API can do -- creating the annual
	// Facebook recurring event, most obviously. Such a step keeps producing a
	// manual row even on an automated channel.
	Automatable bool `json:"automatable,omitempty"`
}

// Channel is one destination.
type Channel struct {
	ID       uuid.UUID `json:"id"`
	TenantID uuid.UUID `json:"tenant_id"`

	Name      string      `json:"name"`
	Mode      ChannelMode `json:"mode"`
	Connector string      `json:"connector,omitempty"`
	SubmitURL string      `json:"submit_url,omitempty"`

	FeedTier   Tier       `json:"feed_tier,omitempty"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`

	LeadTimeDays int `json:"lead_time_days"`
	// IncludeOffsite admits events we are attending rather than hosting. See
	// migration 087 for why it defaults off and cannot be inferred.
	IncludeOffsite bool       `json:"include_offsite"`
	Steps          []Step     `json:"steps"`
	MinProminence  Prominence `json:"min_prominence"`

	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// prominenceRank orders the editorial axis so a floor can be compared against
// an event. Background is genuinely below normal here: that is the whole point
// of the axis, and it is what keeps happy hour off the chamber's calendar
// without anyone maintaining a list of exceptions.
func prominenceRank(p Prominence) int {
	switch p {
	case ProminenceBackground:
		return 0
	case ProminenceNormal:
		return 1
	case ProminenceFeatured:
		return 2
	}
	// An unset or unrecognised value ranks as normal, which is the same
	// default the column itself carries.
	return 1
}

// meetsFloor reports whether an event clears a minimum prominence.
func meetsFloor(e *Event, floor Prominence) bool {
	if floor == "" {
		floor = ProminenceNormal
	}
	return prominenceRank(e.Prominence) >= prominenceRank(floor)
}

// appliesTo reports whether a channel wants to hear about an event at all.
//
// Publicly visible is the hard gate and is asked here rather than assumed:
// promoting a private booking would be the same class of mistake as leaking
// one to the website, and visibility.go is explicit that the predicate is
// never hand-written at a call site.
func (c *Channel) appliesTo(e *Event) bool {
	if !c.Active || !e.IsPubliclyVisible() {
		return false
	}
	// A subscribed channel pulls the feed itself; there is nothing to do and
	// so nothing to list.
	if c.Mode == ChannelSubscribed {
		return false
	}
	// Someone else's event, unless this channel explicitly wants them. Checked
	// independently of prominence: a featured offsite event is still not ours
	// to list on a town calendar, though it is very much worth a post of our
	// own. Same rule the narrow ICS tiers follow.
	if e.Venue == VenueOffsite && !c.IncludeOffsite {
		return false
	}
	return meetsFloor(e, c.MinProminence)
}

// stepApplies reports whether one step runs for one event.
//
// The kind rules are the load-bearing part. A recurring series gets one-shot
// and cadence steps but never drip: you do not run an announce/remind campaign
// for something that happens every Tuesday. A one-off gets one-shot and drip
// but never cadence: there is no series to periodically remind anyone about.
func (c *Channel) stepApplies(s Step, e *Event) bool {
	// An unset step floor INHERITS the channel's rather than defaulting to
	// normal. Defaulting would quietly make a channel-level floor of
	// `background` unreachable -- every step would still demand normal -- so
	// setting the channel to take standing offers would appear to do nothing.
	floor := s.MinProminence
	if floor == "" {
		floor = c.MinProminence
	}
	if !meetsFloor(e, floor) {
		return false
	}
	switch s.Kind {
	case StepDrip:
		return !e.Repeats()
	case StepCadence:
		return e.Repeats()
	case StepOneshot:
		return true
	}
	return false
}

// isManualWork reports whether a step still needs a human on this channel.
// True for every step of a manual channel, and for the non-automatable steps
// of an automated one -- the annual Facebook event being the case that makes
// this per-step rather than per-channel.
func (c *Channel) isManualWork(s Step) bool {
	if c.Mode == ChannelAutomated {
		return !s.Automatable
	}
	return c.Mode == ChannelManual
}

// normaliseName is how two channel names are compared. The unique index is on
// lower(name), so any lookup that wants to agree with the database has to fold
// the same way.
func normaliseName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// normaliseChannel fills defaults and trims input so a hand-made row or a
// sloppy API call cannot produce a channel the list builder has to defend
// against.
func normaliseChannel(c *Channel) {
	c.Name = strings.TrimSpace(c.Name)
	c.SubmitURL = strings.TrimSpace(c.SubmitURL)
	c.Connector = strings.TrimSpace(c.Connector)
	if !ValidChannelMode(c.Mode) {
		c.Mode = ChannelManual
	}
	if !ValidProminence(c.MinProminence) {
		c.MinProminence = ProminenceNormal
	}
	if c.LeadTimeDays < 0 {
		c.LeadTimeDays = 0
	}
	// An automated channel with no connector would silently do nothing, which
	// is worse than never having been automated: the checklist has stopped
	// watching it too. Fall back to manual so the work stays visible.
	if c.Mode == ChannelAutomated && c.Connector == "" {
		c.Mode = ChannelManual
	}
	if c.Mode != ChannelSubscribed {
		c.FeedTier = ""
		c.VerifiedAt = nil
	}
	out := c.Steps[:0]
	for _, s := range c.Steps {
		s.Key = strings.TrimSpace(s.Key)
		if s.Key == "" || !ValidStepKind(s.Kind) {
			continue
		}
		if s.MinProminence != "" && !ValidProminence(s.MinProminence) {
			s.MinProminence = ProminenceNormal
		}
		if s.Kind == StepCadence && s.IntervalDays <= 0 {
			// A zero interval would compute as "due again immediately" every
			// time it was done, which is an infinite nag.
			s.IntervalDays = 28
		}
		out = append(out, s)
	}
	c.Steps = out
}
