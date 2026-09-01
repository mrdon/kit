package events

import (
	"fmt"
	"strings"
	"time"
)

// Text renderings for the promotion tools. Both surfaces run through
// dispatchCore, so there is one formatter here rather than a copy per surface.

// FormatChannelList renders the whole promotion setup.
//
// Grouped by mode rather than listed flat, because the interesting question
// about this list is not "what is in it" but "how much of it is still work" --
// which is the thing the whole design is trying to shrink.
func FormatChannelList(channels []Channel, feeds feedURLs) string {
	if len(channels) == 0 {
		return "No promotion channels yet. Add the places you post events — your chamber, " +
			"the city calendar, Facebook, Instagram — and Kit will track what still needs doing " +
			"on the Events page."
	}

	var manual, subscribed, automated []Channel
	for _, c := range channels {
		switch c.Mode {
		case ChannelSubscribed:
			subscribed = append(subscribed, c)
		case ChannelAutomated:
			automated = append(automated, c)
		case ChannelManual:
			manual = append(manual, c)
		default:
			manual = append(manual, c)
		}
	}

	var b strings.Builder
	section := func(title string, list []Channel) {
		if len(list) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s (%d)\n", title, len(list))
		for _, c := range list {
			fmt.Fprintf(&b, "  %s\n", channelLine(c))
		}
		b.WriteString("\n")
	}

	section("You do these", manual)
	section("They pull the feed — no work", subscribed)
	section("Kit posts these", automated)

	if feeds.All != "" {
		b.WriteString("Calendar feeds to hand out:\n")
		fmt.Fprintf(&b, "  everything  %s\n", feeds.All)
		fmt.Fprintf(&b, "  highlights  %s\n", feeds.Highlights)
		fmt.Fprintf(&b, "  featured    %s\n", feeds.Featured)
		b.WriteString("Ask them to subscribe rather than import — an import goes stale and " +
			"keeps listing cancelled events.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// channelLine is the one-line summary inside a list.
func channelLine(c Channel) string {
	parts := []string{c.Name}

	switch c.MinProminence {
	case ProminenceFeatured:
		parts = append(parts, "big events only")
	case ProminenceBackground:
		parts = append(parts, "everything incl. standing offers")
	case ProminenceNormal:
		// The default needs no annotation; saying "normal events" on most rows
		// would just be noise on every line.
	}
	if c.LeadTimeDays > 0 {
		parts = append(parts, fmt.Sprintf("%dd notice", c.LeadTimeDays))
	}
	if c.IncludeOffsite {
		parts = append(parts, "takes offsite")
	}
	if !c.Active {
		parts = append(parts, "OFF")
	}
	// The silent-failure case: a subscribed channel produces no work by
	// definition, so if nobody ever confirmed the subscription, events stop
	// reaching them and nothing says so.
	if c.Mode == ChannelSubscribed && c.VerifiedAt == nil {
		parts = append(parts, "NOT CONFIRMED — nobody has checked they are really pulling it")
	}
	return strings.Join(parts, " · ")
}

// FormatChannel renders one channel in full, including its campaign.
func FormatChannel(c Channel) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", c.Name)

	switch c.Mode {
	case ChannelSubscribed:
		fmt.Fprintf(&b, "  they subscribe to the %s feed", c.FeedTier)
		if c.VerifiedAt != nil {
			fmt.Fprintf(&b, ", confirmed %s\n", c.VerifiedAt.Format("2 Jan 2006"))
		} else {
			b.WriteString(" — NOT yet confirmed, so nothing is watching this one\n")
		}
	case ChannelAutomated:
		fmt.Fprintf(&b, "  Kit posts it via %s\n", c.Connector)
	case ChannelManual:
		b.WriteString("  you do it")
		if c.SubmitURL != "" {
			fmt.Fprintf(&b, " — %s", c.SubmitURL)
		}
		b.WriteString("\n")
	default:
		b.WriteString("  you do it")
		if c.SubmitURL != "" {
			fmt.Fprintf(&b, " — %s", c.SubmitURL)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "  takes: %s and above", c.MinProminence)
	if c.IncludeOffsite {
		b.WriteString(", including events you are only attending")
	} else {
		b.WriteString(", not events you are only attending")
	}
	b.WriteString("\n")

	if c.LeadTimeDays > 0 {
		fmt.Fprintf(&b, "  wants %d days' notice\n", c.LeadTimeDays)
	}
	if !c.Active {
		b.WriteString("  inactive — generates nothing\n")
	}

	if len(c.Steps) > 0 && c.Mode != ChannelSubscribed {
		b.WriteString("  what happens:\n")
		for _, s := range c.Steps {
			fmt.Fprintf(&b, "    %s — %s\n", s.Label, describeStepTiming(s))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeStepTiming says when a step fires in words, and — for a drip — what
// happens if it is missed, which is the part people get wrong about them.
func describeStepTiming(s Step) string {
	switch s.Kind {
	case StepDrip:
		var when string
		switch s.OffsetDays {
		case 0:
			when = "on the day"
		case 1:
			when = "the day before"
		default:
			when = fmt.Sprintf("%d days before", s.OffsetDays)
		}
		if s.ExpiresAfterDays > 0 {
			when += fmt.Sprintf(", drops off after %dd", s.ExpiresAfterDays)
		}
		if s.MinProminence == ProminenceFeatured {
			when += ", big events only"
		}
		return when + " (one-off events only)"
	case StepCadence:
		return fmt.Sprintf("every %d days, timed from the last one (standing series only)", s.IntervalDays)
	case StepOneshot:
		return "once per event"
	}
	return "once per event"
}

// FormatPromoList renders the outstanding work, in the order it wants doing.
func FormatPromoList(items []PromoItem) string {
	var todo []PromoItem
	for _, it := range items {
		if it.State.actionable() {
			todo = append(todo, it)
		}
	}
	if len(todo) == 0 {
		if len(items) == 0 {
			return "Nothing outstanding. Either everything is posted, or no promotion " +
				"channels are set up yet."
		}
		return "Nothing outstanding — everything due has been posted or skipped."
	}

	var b strings.Builder
	overdue := 0
	for _, it := range todo {
		if it.Overdue {
			overdue++
		}
	}
	fmt.Fprintf(&b, "%d outstanding", len(todo))
	if overdue > 0 {
		fmt.Fprintf(&b, ", %d overdue", overdue)
	}
	b.WriteString(":\n\n")

	for _, it := range todo {
		mark := " "
		if it.Overdue {
			mark = "!"
		}
		fmt.Fprintf(&b, "%s %s — %s: %s (%s)\n",
			mark, it.EventTitle, it.ChannelName, it.StepLabel, dueInWords(it.DueAt))
		if it.State == PromoAutoFailed {
			fmt.Fprintf(&b, "    Kit tried and failed%s — do it by hand or fix the connection\n",
				noteSuffix(it.Note))
		}
		if it.SubmitURL != "" {
			fmt.Fprintf(&b, "    %s\n", it.SubmitURL)
		}
		if it.StepKind == StepCadence && it.LastDoneAt != nil {
			fmt.Fprintf(&b, "    last posted %s\n", it.LastDoneAt.Format("2 Jan"))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func noteSuffix(note string) string {
	if strings.TrimSpace(note) == "" {
		return ""
	}
	return ": " + note
}

// dueInWords keeps the list scannable. Days rather than dates because the
// question being asked is "how late is this", not "what is the date".
func dueInWords(due time.Time) string {
	days := int(due.Sub(timeNow()).Hours() / 24)
	switch {
	case days < -1:
		return fmt.Sprintf("%d days overdue", -days)
	case days < 0:
		return "overdue"
	case days == 0:
		return "today"
	case days == 1:
		return "tomorrow"
	default:
		return fmt.Sprintf("in %d days", days)
	}
}
