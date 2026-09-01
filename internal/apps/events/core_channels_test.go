package events

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrdon/kit/internal/services"
)

// The channel tools are exercised through dispatchCore, which is the exact
// entry point both the MCP server and the agent registry use. Testing the
// wrapper surfaces separately would test the wrappers, not the behaviour.

func (sf *syncFixture) tool(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshalling args: %v", err)
	}
	caller := &services.Caller{TenantID: sf.tenant.ID, IsAdmin: true}
	out, err := dispatchCore(context.Background(), caller, sf.svc, name, raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func (sf *syncFixture) toolErr(t *testing.T, name string, args map[string]any) error {
	t.Helper()
	raw, _ := json.Marshal(args)
	caller := &services.Caller{TenantID: sf.tenant.ID, IsAdmin: true}
	_, err := dispatchCore(context.Background(), caller, sf.svc, name, raw)
	return err
}

// A campaign name expands to the right steps here rather than being handed to
// the caller as a JSON array. That is the point of the indirection: an agent
// composing step objects produces plausible nonsense like a drip on a weekly
// series.
func TestChannelTools_CampaignExpandsToSteps(t *testing.T) {
	sf := newSyncFixture(t)

	sf.tool(t, "events_add_channel", map[string]any{
		"name": "Chamber", "campaign": "submit_once", "min_prominence": "featured",
		"lead_time_days": 14, "submit_label": "Add to the chamber calendar",
	})
	sf.tool(t, "events_add_channel", map[string]any{
		"name": "Instagram story", "campaign": "day_of_only",
	})

	channels, err := sf.svc.ListChannels(sf.ctx, sf.tenant.ID)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	byName := map[string]Channel{}
	for _, c := range channels {
		byName[c.Name] = c
	}

	chamber := byName["Chamber"]
	if len(chamber.Steps) != 1 || chamber.Steps[0].Kind != StepOneshot {
		t.Errorf("submit_once should be a single one-shot, got %+v", chamber.Steps)
	}
	if chamber.LeadTimeDays != 14 || chamber.MinProminence != ProminenceFeatured {
		t.Errorf("chamber config not applied: %+v", chamber)
	}

	// A story expires in a day, so an announce three weeks out would be gone
	// before anyone saw it.
	story := byName["Instagram story"]
	for _, s := range story.Steps {
		if s.Kind != StepDrip || s.OffsetDays > 1 {
			t.Errorf("day_of_only should only carry same-day beats, got %+v", s)
		}
	}
}

func TestChannelTools_RejectsUnknownCampaign(t *testing.T) {
	sf := newSyncFixture(t)
	err := sf.toolErr(t, "events_add_channel", map[string]any{
		"name": "Somewhere", "campaign": "every_tuesday",
	})
	if err == nil {
		t.Fatal("expected an unknown campaign to be refused")
	}
	// The message has to name the alternatives, or the caller's only recovery
	// is to guess again.
	if !strings.Contains(err.Error(), "submit_once") {
		t.Errorf("error should list the valid campaigns, got: %v", err)
	}
}

// Referring to a channel by a fragment of its name is the whole reason these
// tools are usable from chat -- nobody carries a uuid around.
func TestChannelTools_ResolvesByPartialName(t *testing.T) {
	sf := newSyncFixture(t)
	sf.tool(t, "events_add_channel", map[string]any{
		"name": "Louisville Chamber of Commerce", "campaign": "submit_once",
	})

	out := sf.tool(t, "events_update_channel", map[string]any{
		"channel": "chamber", "lead_time_days": 21,
	})
	if !strings.Contains(out, "Louisville Chamber of Commerce") {
		t.Errorf("partial name did not resolve: %s", out)
	}
}

// An ambiguous fragment must ask rather than pick one, because picking wrong
// silently reconfigures the wrong destination.
func TestChannelTools_AmbiguousNameIsRefused(t *testing.T) {
	sf := newSyncFixture(t)
	sf.tool(t, "events_add_channel", map[string]any{"name": "Facebook posts", "campaign": "announce_and_remind"})
	sf.tool(t, "events_add_channel", map[string]any{"name": "Facebook story", "campaign": "day_of_only"})

	err := sf.toolErr(t, "events_update_channel", map[string]any{
		"channel": "facebook", "active": false,
	})
	if err == nil {
		t.Fatal("expected an ambiguous channel name to be refused")
	}
	if !strings.Contains(err.Error(), "Facebook posts") || !strings.Contains(err.Error(), "Facebook story") {
		t.Errorf("error should name the candidates, got: %v", err)
	}
}

// The win condition: switching a channel to subscribed retires its work
// entirely, and the listing says so.
func TestChannelTools_SubscribingMovesItOutOfTheWorkList(t *testing.T) {
	sf := newSyncFixture(t)
	sf.tool(t, "events_add_channel", map[string]any{
		"name": "Chamber", "campaign": "submit_once", "min_prominence": "featured",
	})

	sf.tool(t, "events_update_channel", map[string]any{
		"channel": "Chamber", "mode": "subscribed", "feed_tier": "featured", "verified": true,
	})

	out := sf.tool(t, "events_channels", nil)
	if !strings.Contains(out, "They pull the feed") {
		t.Errorf("subscribed channel should be listed as needing no work:\n%s", out)
	}
	if strings.Contains(out, "You do these") {
		t.Errorf("nothing should be left in the manual group:\n%s", out)
	}
}

// A subscribed channel nobody confirmed is the one silent failure in the
// design, so the listing has to shout about it.
func TestChannelTools_UnconfirmedSubscriptionIsFlagged(t *testing.T) {
	sf := newSyncFixture(t)
	sf.tool(t, "events_add_channel", map[string]any{"name": "Chamber", "campaign": "submit_once"})
	sf.tool(t, "events_update_channel", map[string]any{
		"channel": "Chamber", "mode": "subscribed", "feed_tier": "featured",
	})

	if out := sf.tool(t, "events_channels", nil); !strings.Contains(out, "NOT CONFIRMED") {
		t.Errorf("an unverified subscription must be called out:\n%s", out)
	}
}

func TestChannelTools_PromoListRendersOutstandingWork(t *testing.T) {
	sf := newSyncFixture(t)
	featured := ProminenceFeatured
	// Inside promoWindowMonths -- a date years out is genuinely not on the
	// list, which is the window doing its job.
	when := timeNow().AddDate(0, 1, 0).Format("2006-01-02 15:04")
	sf.publishedPublic(t, CreateParams{
		Title: "Oktoberfest", StartsAt: when, Prominence: &featured,
	})
	sf.tool(t, "events_add_channel", map[string]any{
		"name": "Chamber", "campaign": "submit_once", "min_prominence": "featured",
		"submit_url": "https://chamber.example/submit",
	})

	out := sf.tool(t, "events_promo_list", nil)
	if !strings.Contains(out, "Oktoberfest") || !strings.Contains(out, "Chamber") {
		t.Errorf("promo list should show the outstanding submission:\n%s", out)
	}
	if !strings.Contains(out, "https://chamber.example/submit") {
		t.Errorf("promo list should carry the deep link:\n%s", out)
	}
}

// Deleting takes the history with it, so the tool says so and points at the
// reversible alternative.
func TestChannelTools_DeleteWarnsAboutHistory(t *testing.T) {
	sf := newSyncFixture(t)
	sf.tool(t, "events_add_channel", map[string]any{"name": "Chamber", "campaign": "submit_once"})

	out := sf.tool(t, "events_remove_channel", map[string]any{"channel": "Chamber"})
	if !strings.Contains(out, "inactive") {
		t.Errorf("delete should mention the non-destructive alternative: %s", out)
	}
	if got := sf.tool(t, "events_channels", nil); !strings.Contains(got, "No promotion channels yet") {
		t.Errorf("channel should be gone: %s", got)
	}
}

// Relabelling without touching the campaign has to work: the obvious reading
// of "pass submit_label" is "change the wording", and folding it into the
// campaign branch made it silently do nothing.
func TestChannelTools_SubmitLabelAloneRenamesTheStep(t *testing.T) {
	sf := newSyncFixture(t)
	sf.tool(t, "events_add_channel", map[string]any{
		"name": "DBA newsletter", "campaign": "submit_once",
		"submit_label": "Send it for the newsletter",
	})

	out := sf.tool(t, "events_update_channel", map[string]any{
		"channel": "DBA newsletter", "submit_label": "Include in the monthly newsletter",
	})
	if !strings.Contains(out, "Include in the monthly newsletter") {
		t.Errorf("submit_label alone should rename the step:\n%s", out)
	}
}
