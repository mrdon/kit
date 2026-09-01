package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// The promotion-channel tools.
//
// Channels are configuration, and the console page is the comfortable way to
// edit one. These exist because SETTING THEM UP is a bulk job -- eight or nine
// destinations, each with a campaign -- and doing that through a form eight
// times is worse than describing it once in chat.
//
// Steps are the part worth keeping out of a tool argument. Hand an agent a
// JSON array of {kind, offset_days, expires_after_days, min_prominence} and it
// will produce something plausible and subtly wrong -- a drip on a weekly
// series, a cadence on a one-off. So the tool takes a named CAMPAIGN instead
// and expands it here, which is the same set the console's preset dropdown
// offers.

// campaign names the shape of a channel's work. One word the caller picks,
// rather than a step array they compose.
type campaign string

const (
	// campaignSubmit is a community calendar: one submission per event.
	campaignSubmit campaign = "submit_once"
	// campaignSocial is a feed post: announce, a featured-only nudge, a
	// day-before, plus a cadence so standing nights get mentioned periodically
	// instead of announced every week.
	campaignSocial campaign = "announce_and_remind"
	// campaignStory is for anything that expires in a day.
	campaignStory campaign = "day_of_only"
	// campaignCadence is periodic-only: no per-event work at all.
	campaignCadence campaign = "every_few_weeks"
)

// campaignSteps expands a campaign name into the step array. Keeping this in
// Go rather than in the tool schema is what stops an agent inventing a drip
// for a weekly series.
func campaignSteps(c campaign, submitLabel string) ([]Step, error) {
	switch c {
	case campaignSubmit:
		if submitLabel == "" {
			submitLabel = "Submit the event"
		}
		return []Step{{Key: "submit", Label: submitLabel, Kind: StepOneshot}}, nil
	case campaignSocial:
		return []Step{
			{Key: "announce", Label: "Announce", Kind: StepDrip, OffsetDays: 21, Automatable: true},
			{Key: "remind", Label: "Remind", Kind: StepDrip, OffsetDays: 7, ExpiresAfterDays: 4,
				MinProminence: ProminenceFeatured, Automatable: true},
			{Key: "day-before", Label: "Day before", Kind: StepDrip, OffsetDays: 1, ExpiresAfterDays: 1,
				Automatable: true},
			{Key: "mention", Label: "Post about it", Kind: StepCadence, IntervalDays: 28, Automatable: true},
		}, nil
	case campaignStory:
		return []Step{
			{Key: "day-before", Label: "Day before", Kind: StepDrip, OffsetDays: 1, ExpiresAfterDays: 1, Automatable: true},
			{Key: "day-of", Label: "Day of", Kind: StepDrip, OffsetDays: 0, ExpiresAfterDays: 1, Automatable: true},
		}, nil
	case campaignCadence:
		return []Step{
			{Key: "mention", Label: "Post about it", Kind: StepCadence, IntervalDays: 28, Automatable: true},
		}, nil
	}
	return nil, invalid("unknown campaign %q — use submit_once, announce_and_remind, day_of_only or every_few_weeks", c)
}

// channelInput is the union of what the create/update tools accept. Pointers
// so an absent field differs from a cleared one, which is what makes the
// update a partial patch -- same convention as eventInput.
type channelInput struct {
	Channel string `json:"channel"`

	Name           *string `json:"name"`
	Mode           *string `json:"mode"`
	SubmitURL      *string `json:"submit_url"`
	MinProminence  *string `json:"min_prominence"`
	LeadTimeDays   *int    `json:"lead_time_days"`
	IncludeOffsite *bool   `json:"include_offsite"`
	Active         *bool   `json:"active"`
	FeedTier       *string `json:"feed_tier"`
	Verified       *bool   `json:"verified"`

	Campaign    *string `json:"campaign"`
	SubmitLabel *string `json:"submit_label"`
}

// resolveChannel finds a channel by id or by name, so a caller can say
// "the chamber" rather than carrying a uuid around.
func resolveChannel(ctx context.Context, svc *Service, tenantID uuid.UUID, ref string) (*Channel, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, invalid("which channel? Give its name or id")
	}
	if id, err := uuid.Parse(ref); err == nil {
		return getChannel(ctx, svc.pool, tenantID, id)
	}
	all, err := listChannels(ctx, svc.pool, tenantID)
	if err != nil {
		return nil, err
	}
	want := normaliseName(ref)
	var matches []Channel
	for i := range all {
		if normaliseName(all[i].Name) == want {
			return &all[i], nil
		}
		if strings.Contains(normaliseName(all[i].Name), want) {
			matches = append(matches, all[i])
		}
	}
	switch len(matches) {
	case 0:
		return nil, invalid("no promotion channel called %q", ref)
	case 1:
		return &matches[0], nil
	default:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.Name
		}
		sort.Strings(names)
		return nil, invalid("%q matches several channels: %s", ref, strings.Join(names, ", "))
	}
}

func coreListChannels(ctx context.Context, caller *services.Caller, svc *Service) (string, error) {
	channels, err := svc.ListChannels(ctx, caller.TenantID)
	if err != nil {
		return "", err
	}
	settings, err := getSettings(ctx, svc.pool, caller.TenantID)
	if err != nil {
		return "", err
	}
	return FormatChannelList(channels, publicFeedURLs(settings)), nil
}

func coreSaveChannel(ctx context.Context, caller *services.Caller, svc *Service, raw json.RawMessage, creating bool) (string, error) {
	var in channelInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", invalid("could not read the arguments")
	}

	var c Channel
	if !creating {
		found, err := resolveChannel(ctx, svc, caller.TenantID, in.Channel)
		if err != nil {
			return "", err
		}
		c = *found
	} else {
		// A new channel is manual and active unless told otherwise. Manual is
		// the honest default: every other mode is a claim about somebody else's
		// system that nobody has verified yet.
		c = Channel{Mode: ChannelManual, MinProminence: ProminenceNormal, Active: true}
	}

	if in.Name != nil {
		c.Name = *in.Name
	}
	if in.Mode != nil {
		c.Mode = ChannelMode(*in.Mode)
		if !ValidChannelMode(c.Mode) {
			return "", invalid("mode must be manual, subscribed or automated")
		}
	}
	if in.SubmitURL != nil {
		c.SubmitURL = *in.SubmitURL
	}
	if in.MinProminence != nil {
		c.MinProminence = Prominence(*in.MinProminence)
		if !ValidProminence(c.MinProminence) {
			return "", invalid("min_prominence must be background, normal or featured")
		}
	}
	if in.LeadTimeDays != nil {
		c.LeadTimeDays = *in.LeadTimeDays
	}
	if in.IncludeOffsite != nil {
		c.IncludeOffsite = *in.IncludeOffsite
	}
	if in.Active != nil {
		c.Active = *in.Active
	}
	if in.FeedTier != nil {
		c.FeedTier = Tier(*in.FeedTier)
	}
	if in.Verified != nil && *in.Verified {
		now := timeNow()
		c.VerifiedAt = &now
	}
	if in.Campaign != nil {
		label := ""
		if in.SubmitLabel != nil {
			label = *in.SubmitLabel
		}
		steps, err := campaignSteps(campaign(*in.Campaign), label)
		if err != nil {
			return "", err
		}
		c.Steps = steps
	} else if in.SubmitLabel != nil {
		// Relabelling without replacing the campaign. Handled separately
		// because the obvious reading of "pass submit_label" is "change the
		// wording", and folding it into the campaign branch made it silently
		// do nothing whenever the campaign was left alone.
		for i := range c.Steps {
			if c.Steps[i].Kind == StepOneshot {
				c.Steps[i].Label = *in.SubmitLabel
			}
		}
	}
	if creating && len(c.Steps) == 0 {
		return "", invalid("a new channel needs a campaign — submit_once, announce_and_remind, day_of_only or every_few_weeks")
	}

	saved, err := svc.SaveChannel(ctx, caller.TenantID, c)
	if err != nil {
		return "", err
	}
	verb := "Updated"
	if creating {
		verb = "Added"
	}
	return fmt.Sprintf("%s %s.\n\n%s", verb, saved.Name, FormatChannel(*saved)), nil
}

func coreDeleteChannel(ctx context.Context, caller *services.Caller, svc *Service, raw json.RawMessage) (string, error) {
	var in channelInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", invalid("could not read the arguments")
	}
	found, err := resolveChannel(ctx, svc, caller.TenantID, in.Channel)
	if err != nil {
		return "", err
	}
	if err := svc.DeleteChannel(ctx, caller.TenantID, found.ID); err != nil {
		return "", err
	}
	// Historical promo rows cascade with the channel. Said out loud because it
	// is the one irreversible part -- switching a channel off with active=false
	// keeps the record and stops the work just as well.
	return fmt.Sprintf("Removed %s, along with its record of what had already been posted there. "+
		"To stop the reminders but keep the history, set it inactive instead.", found.Name), nil
}

func corePromoList(ctx context.Context, caller *services.Caller, svc *Service) (string, error) {
	items, err := svc.PromoList(ctx, caller.TenantID)
	if err != nil {
		return "", err
	}
	return FormatPromoList(items), nil
}
