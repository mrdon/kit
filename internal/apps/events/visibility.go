package events

// This file is small and separate on purpose. IsPubliclyVisible is the single
// gate between Gravity's private bookings and the open web; keeping it alone
// in a 60-line file means it can be audited at a glance and its truth table
// tested exhaustively.
//
// The rule callers must follow: never hand-write the predicate at a call site.
// The feed asks this function, and so does anything else that decides whether
// a row may leave the building. A second copy of the condition somewhere is
// how a customer's birthday party ends up on the brewery's website.

// Status is the lifecycle axis: is this event settled?
type Status string

const (
	// StatusDraft is still being worked out. Draft events appear nowhere --
	// not on the calendar, not in the feed -- so an event can be revised
	// repeatedly before anyone sees it.
	StatusDraft Status = "draft"
	// StatusPublished means CONFIRMED, not public. A private party is
	// published the moment it is on the books.
	StatusPublished Status = "published"
	// StatusCancelled is called off. The row survives as a tombstone so the
	// next sync knows which Google event to remove.
	StatusCancelled Status = "cancelled"
)

// Visibility is the exposure axis: may the outside world see this?
type Visibility string

const (
	// VisibilityPrivate is the default. Default-deny: publishing is the
	// explicit act.
	VisibilityPrivate Visibility = "private"
	VisibilityPublic  Visibility = "public"
)

// Venue is the location axis.
type Venue string

const (
	VenueOnsite Venue = "onsite"
	// VenueOffsite is a festival or event we attend elsewhere. Still often
	// public -- we want "come see us at X" on the website.
	VenueOffsite Venue = "offsite"
)

// SpaceImpact is how much of the taproom an event occupies.
type SpaceImpact string

const (
	SpaceImpactNone SpaceImpact = "none"
	// SpaceImpactPartial is a reserved area, e.g. the back room for a party.
	SpaceImpactPartial SpaceImpact = "partial"
)

// IsPubliclyVisible reports whether this event may appear on the public feed
// and, through it, the website.
//
// Both axes must agree: published (settled) AND public (shareable). Neither
// implies the other -- a confirmed private party is published+private, and a
// draft public event is not ready regardless of its visibility.
func (e *Event) IsPubliclyVisible() bool {
	if e == nil {
		return false
	}
	return e.Status == StatusPublished && e.Visibility == VisibilityPublic
}

// ValidStatus etc. gate writes at the service layer so a bad value is refused
// with a clear message before the database CHECK constraint fires.
func ValidStatus(s Status) bool {
	return s == StatusDraft || s == StatusPublished || s == StatusCancelled
}

func ValidVisibility(v Visibility) bool {
	return v == VisibilityPublic || v == VisibilityPrivate
}

func ValidVenue(v Venue) bool {
	return v == VenueOnsite || v == VenueOffsite
}

func ValidSpaceImpact(s SpaceImpact) bool {
	return s == SpaceImpactNone || s == SpaceImpactPartial
}
