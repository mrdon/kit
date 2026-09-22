---
name: user-guide
description: "How to use Kit — adding skills, creating rules, scheduling tasks, managing roles, and searching your knowledge base."
---

# Kit User Guide

Kit is your team's knowledge base and automation assistant. It stores skills (knowledge articles), enforces rules (agent behavior), runs scheduled jobs, and answers questions — all accessible from Slack or any MCP-compatible AI tool.

## Adding a Skill

Skills are reusable pieces of knowledge — procedures, policies, FAQs, or anything your team needs to reference.

**In Slack**, just describe what you want to save:

> "Create a skill called closing-checklist with our end-of-day steps: lock the front door, run the register report, and set the alarm."

You can also upload files directly — PDFs, Word docs, markdown, or ZIP archives. Kit reads them and creates skills automatically.

**Via MCP**, use the `create_skill` tool with a name, description, and content (markdown).

Before authoring or summarizing a skill, consult the `creating-skills` skill — it covers how to pick a name, write a description that the bot can find, and keep content dense enough for the chat agent to give short, useful answers.

By default, skills are visible to everyone. You can scope them to a specific role when creating:

> "Create a skill called payroll-process scoped to managers."

## Adding a Rule

Rules tell Kit's AI how to behave — tone, policies, guardrails. Think of them as standing instructions that shape every response.

**In Slack:**

> "Add a rule: always suggest checking the employee handbook before answering HR questions."

**Via MCP**, use `create_rule` with the rule content and an optional priority (lower number = higher priority).

Rules can be scoped to specific roles so different teams get different behavior.

## Scheduling a Task

Tasks let Kit do things on a schedule — daily summaries, weekly reminders, recurring reports. Just describe when in plain language:

> "Every weekday at 9am, post a morning briefing to this channel."
> "Every Monday at 8am, remind the team about the weekly standup."
> "Tomorrow at 3pm, send me the sales report."

**Via MCP**, use `create_job` with a description and schedule.

For non-trivial tasks — especially those posting to public channels, pinning a specific argument, or needing forced approval gates before the agent can act — consult the `creating-jobs` skill. It covers cron vs one-time schedules, scope, writing a description the scheduled agent can execute, and designing the optional `policy` block that constrains the agent at fire time.

## Managing Roles and Access

Kit uses roles to control who sees what. Create roles, assign users, then scope skills, rules, and tasks to those roles.

> "Create a role called managers."
> "Assign @jane to managers."

Anything scoped to a role is only visible to members of that role. Anything scoped to "tenant" is visible to everyone. Items with no scopes are invisible (default deny).

## Connecting External Tools via MCP

Each Slack workspace has its own MCP endpoint URL of the form `{base-url}/{workspace-slug}/mcp`. You only need your workspace's slug to configure Claude Code, Cursor, or any other MCP client.

- Right after you install Kit, Kit DMs you the exact URL. If you kept that message, copy-paste it into your client.
- Lost it? Message Kit in a DM: *"what's my MCP URL?"* and Kit will repeat it.
- Or open the web console's **Connect AI tools** page (`/{workspace-slug}/web/connect`) — it shows your workspace's endpoint plus copy-paste setup for Claude Code and Cursor.

If you belong to more than one Slack workspace with Kit installed, add one MCP entry per workspace — each URL binds its access token to exactly one workspace, and signing into the wrong one during the OAuth handshake returns a clear error rather than silently issuing a token against the wrong tenant.

## Searching Your Knowledge Base

**In Slack**, just ask a question — Kit automatically searches relevant skills and memories to answer:

> "What's our return policy?"
> "How do I close out the register?"

**Via MCP**, use `search_skills` with a query for full-text search, or `list_skills` to browse everything you have access to.

## Memories

Kit remembers important facts from conversations. These are short-lived, contextual notes that help Kit give better answers over time. You can also explicitly ask Kit to remember something:

> "Remember that our holiday hours start December 20th."

Use `search_memories` to find previously saved facts, or `forget_memory` to remove one.

## Tasks

Kit tracks tasks for your team. Create them from conversation or explicitly:

> "Create a todo to restock the paper towels, assign it to me."
> "What tasks are overdue?"

Tasks support priorities, due dates, role scoping, and an activity log. Use `list_tasks` to see open items or `complete_task` to mark one done. Use `snooze_task` (with `days` = any value 1–365; common picks are 1, 3, 7, 14, 30) to hide a todo from your swipe feed temporarily while keeping it active — worth doing for something urgent you've consciously deferred, since the feed shows urgent tasks only (see "Decisions and briefings"). To delete, set `status` to `cancelled` via `update_task` — it's a soft delete, recoverable by an admin via the DB if done accidentally.

## Web console

For deliberate, do-it-yourself work on a desktop, open the web console at `/<your-slug>/web`. It's a direct-manipulation UI — distinct from Slack (ask the agent) and the swipe feed (mobile triage). The launcher links to:

- **Tasks** — a priority-banded list (Blocker / High / Normal) grouped by an auto-assigned topic category. Drag a task between bands to reprioritize, tap "I'm on it" to claim it (which reserves it so teammates skip it) or the checkbox to resolve. Also a flat List view, a detail drawer, and the comment timeline. The same tasks still surface in the swipe feed.
- **Kiosk** — wall-mounted screens: give each one a permanent Kit address, then change what it shows by editing its URL here. Any member can repoint a screen.
- **Vault** — the shared-password vault (set up, unlock, add, reveal, rotate), end-to-end encrypted in your browser.
- **Skills** — browse the knowledge base, search, and open a skill to read it. Admins can create, edit (name, description, content), delete, and attach files; built-in skills show read-only. Everyone sees only the skills their roles can.
- **Jobs** — your scheduled work: each row shows its schedule, status, linked skill, last run, and any error. Open one to edit its description, change or clear the linked skill, adjust the capability policy, or delete it. You see your own jobs plus role/tenant ones; admins see and manage every job in the workspace. Create new jobs by asking Kit in chat.
- **Apps** — admin-only page to turn features (vault, calendar, events, kiosk, and so on) on or off for the whole workspace. Disabling a feature removes it everywhere — its tools, pages, cards, and the agent's knowledge of it — for everyone, until an admin turns it back on. Only user-facing features appear here; core plumbing (the console itself, admin tools, file attachments, the card feed, and the integrations registry) is always on.
- **Integrations** — connect external services from one page: click **Connect** and enter the secret on a secure one-time form (it never passes through the assistant). Personal email is self-service for any user; workspace-wide services (Square, Google Calendar) are admin-only.
- **Chat widget** — admin-only setup page for the website chat widget.

It works alongside Slack and the feed, not instead of them.

## Slack Channel Search

Admins can configure Slack channels for Kit to search. Once configured, Kit can read and search messages in those channels:

> "Look in #general for any action items from today."
> "Search #ops for mentions of the deploy."

Channels are scoped to roles, so users only see messages from channels they have access to. Use `list_slack_channels` to see available channels.

## Calendars

Admins can plug in any public iCal (`.ics`) URL — a Google Calendar share link, a band tour calendar, a brewery shift schedule — and Kit will keep it in sync and answer questions about the events on it.

> "Configure a calendar called shifts using https://example.com/shifts.ics"
> "Configure a calendar called festivals at https://band.example.org/calendar.ics scoped to parents."

Once configured, just ask:

> "Who's working tonight?"
> "When is the next festival?"
> "Anything on the calendar this Saturday?"

Calendars are scoped to roles like other Kit resources. Use `list_calendars` to see what's configured (and the last sync status), and `get_calendar_events` for date or keyword queries. Kit re-fetches each calendar in the background, so changes on the source feed show up automatically.

## Square shift sync

If you run staff scheduling in Square, Kit can mirror your **published** schedule into a Google Calendar your team already subscribes to — so shifts show up in everyone's calendar automatically. It's admin-only setup and then runs itself (a background sync every 15 minutes).

Two one-time connections, both on the **Integrations** page in the web console (click **Connect** on each):

- **Square** — paste a Square Production Access Token (Developer Console → Credentials → Production). A Production token carries every scope, so the same one covers shift sync (`TIMECARDS_READ`, `EMPLOYEES_READ`, `MERCHANT_PROFILE_READ`) and sales (`REPORTING_READ`). Leave the refresh token blank — a Production token doesn't expire. Once connected, `square_list_shifts` lists the upcoming published schedule so you can confirm the pull.
- **Google Calendar** — create a Google service account, download its JSON key, and **share your target calendar with the service account's email as a writer**. Paste the key + the calendar's ID. Then `gcal_check` writes and deletes a probe event to confirm write access. (A service account has no per-seat cost and needs no admin domain setup.)

Enable the **Square Shift Sync** feature on the Apps page and it starts syncing. Each published shift becomes an **all-day** calendar event on the shift's date, titled with the team member's first name and shift hours (e.g. "Alice · 6:00am–2:00pm") so it stays unobtrusive when layered over a personal calendar while still showing who opens and closes; cancelled shifts are removed on the next sweep. Run `squareshifts_sync_now` to sync on demand and `squareshifts_status` to see the last run. If the calendar has drifted — someone deleted synced events by hand, or stale ones linger — `squareshifts_reconcile` repairs it against Square; pass `dry_run: true` first to see exactly what it would add or remove before it touches anything. Kit only reads Square's *published* schedule — it doesn't build schedules (Square's API doesn't expose staff availability or time-off).

## Square sales card

If Square is connected, Kit posts one card each morning with yesterday's sales — and, more usefully, what was unusual about them. Enable **Square Sales Insights** on the Apps page.

Every figure comes with its comparison, because a revenue number on its own tells you nothing. The baseline is the **same weekday** over the previous eight weeks, so a Tuesday is judged against Tuesdays; comparing against yesterday would flag every Monday as a collapse. The comparison uses a median rather than an average, so one exceptional day (a festival, a private buyout) doesn't quietly raise the bar for the next two months.

A normal day is one line — the total, the order count, the average ticket, and how that compares. When something stands out, the card leads with it:

- **an unusually high or low day** — it has to clear all three of: outside the normal spread for that weekday, at least 20% off the typical figure, and at least $75 in absolute terms. Any one alone produces noise.
- **a dead stretch** — an hour that took far less than that same hour normally does on that weekday. Adjacent hours merge into one span ("3pm-5pm ran dead"). Hours that never trade much can't be flagged, so closed hours stay silent without anyone configuring opening times.
- **item movers** — a beer that sold well above or below its own usual figure for that weekday.
- **orders and revenue diverging** — more visits with smaller tabs, or fewer visits with bigger ones. Different problems, and the top-line number hides both.
- **a shift in comping** — comps are reported as a *rate* rather than a dollar figure, because $120 comped on a festival Saturday is the same posture as $30 on a slow Tuesday. Only the rate tells you which way the dial has moved.

Findings are ordered by how much money moved and capped at three, so the card stays readable in one glance.

**Comps appear every day they happen**, flagged or not — "Comps $47.00 — 5.8% of gross, usual 4.3%" — so you can see the running level and dial it up or back deliberately rather than discovering a drift months later.

There's no analysis until there's enough history: a comparison needs at least four prior same-weekday trading days, and until then the card says so rather than quoting a number with nothing behind it. Days with no sales are recorded as closed and excluded from every baseline, so a holiday doesn't drag the following weeks down.

`squaresales_status` shows how far back the data goes and when the sync last ran; `squaresales_sync_now` pulls immediately; `squaresales_post_card_now` builds a card for any past day on demand (pass `preview: true` to read it without posting). All three are admin-only. Sales are re-pulled nightly for the past month, because refunds and disputes can move a day's total days after the fact.


## Events

Enter an event once and Kit fans it out: it lands on the team's Google Calendar, and public events also reach the website. Anyone in the workspace can author events; the calendar and website setup is admin-only.

Create one from chat, or from the **Events** page in the web console:

> "Create an event for Trivia Night on Tuesday 14th at 7pm, repeating weekly"
>
> "Supper Club on Sept 4th, Oct 2nd and Nov 6th, 6pm, public"
>
> "Copy the Beer School event to January 8th"
> "Add a private booking for Sarah's 40th, Saturday 6-9pm in the back room, about 30 people"
> "We're pouring at the Denver Beer Fest on the 22nd — that's offsite and public"

**Two things are separate, and it matters.** *Status* is whether an event is settled: draft → published → cancelled. *Visibility* is whether the outside world may see it: public or private. So a confirmed private booking is **published and private** — on the team calendar the moment it's booked, and never on the website. Publishing does not make something public.

Events start **private**, so nothing reaches the website unless someone says it should. A draft appears nowhere at all — not on the calendar, not on the site — so you can rewrite it as many times as you like before anyone sees it.

Three things describe an event, and they're independent rather than one "type":

- **visibility** — `public` (website + feed) or `private` (internal only)
- **venue** — `onsite`, or `offsite` for a festival you're attending. An offsite event can still be public: "come see us there" belongs on the site.
- **space impact** — whether it reserves part of the room, so whoever's working knows

**Prominence** is how loudly an event speaks, and whose it is. Most events need nothing set — `normal` is the default and already headlines its own day.

- `featured` — the website leads with it. A superlative most real events never earn: the anniversary party is featured, the bike night isn't, and both still headline their day. Several can be marked at once; the site leads with the next one.
- `normal` — a real event. The default.
- `background` — **your** standing offer rather than a happening: NFL Sundays, happy hour, a weekly cask tapping. Printed and published, but it never takes the headline off a real event on the same day.
- `amenity` — a **partner's** standing offer sold in your room: the pizza deal, a food truck's regular night. Everything `background` is, and below it.

That last distinction only shows up on a day with no real event, and then it decides everything. A Monday carrying Double D's pizza deal at 4pm and your own football night at 6pm used to print **BOGO PIZZA** with the football mentioned underneath — chosen on door time, which is an accident of the clock rather than a judgement about what the taproom is for. Marking the pizza `amenity` flips it. Set it once on the recurring deal and it stays right.

**Labels** say what kind of thing an event is, so your website can group events onto their own page without Kit needing to know that page exists. An event can carry several. Tick the usual ones — `giveback`, `food`, `trivia`, `music`, `release`, `family`, `community` — or type your own for anything else. They are lowercased and hyphenated on save, and a few obvious synonyms fold automatically ("charity" and "Give Back" both become `giveback`). Reuse a label that already exists rather than inventing a synonym for it: the website matches the exact word, so a second spelling quietly splits the group in two. Labels are not prominence (how loudly an event speaks) and not venue (where it happens) — they are the subject.

**Staff notes** go on the calendar entry, where the bartender working that night is already looking. They never appear on the website.

**Repeats come in three shapes**, and picking the right one means one web page instead of five:

- **Every week** — a standing night like trivia, same weekday as the start date.
- **Every month** — "the first Friday", "the last Friday", or a day of the month like the 15th. Kit offers only the patterns that actually match your start date. A month with no such date is skipped rather than moved, so a series on the 31st simply doesn't run in February.
- **On set dates** — an add-and-remove list of dates for a series that follows no pattern: a supper club scheduled around the chef, a five-week course with a gap over a holiday. One event on several dates means one web page, one poster and one set of staff notes.

The exception is a run of genuinely *different* events — live music with a different band each week. Those each have their own name, description and poster, so they stay separate events.

**Duplicate instead of retyping.** The **Duplicate** button on an event (or "copy that event to the 12th" in chat) makes a new draft with the same blurb, staff notes, price, capacity and poster. The copy is independent — editing one never changes the other — and it gets its own web address. Give it a new date and it becomes a one-off on that date; leave the date alone and it duplicates the schedule exactly.

**Adding a poster from chat or a harness.** The event form takes a poster as a file upload, but a tool call can only carry text — so when Kit creates an event for you in chat, or an AI harness creates one over MCP, the reply comes back with a one-time upload link instead. POST the image to that address and it becomes the event's poster. The link works once, expires after fifteen minutes, and only touches the one event it was made for. Ask for another any time ("give me an upload link for Bike Night") to replace a poster or to replace a link that has gone stale.

**Cancel rather than delete.** Cancelling removes an event from the calendar and the website but keeps the record, so the calendar entry gets cleaned up and the web address is never reused for different content. Web addresses are frozen once an event is published, because links to them may already be in a social post or newsletter.

### Getting events onto other people's calendars

The events page publishes three subscribable calendar feeds on your website, so a
chamber of commerce, a city calendar or a regular's phone can follow along instead of
being told about each event separately. They are nested — each one is a smaller version
of the last:

- `/events.ics` — **everything**, standing offers like happy hour and the food partner's deals included. For regulars.
- `/events-highlights.ics` — real happenings, no standing offers. For a trade guild or
  business association.
- `/events-featured.ics` — just the big ones. For a chamber or town calendar.

Which events land in which comes from the **prominence** you already set, so there is
nothing extra to maintain. Events you are only *attending* (venue set to offsite) appear
only in the everything feed — the organiser already lists those on the community
calendars, so sending them again would just duplicate the listing.

Point people at `/events#feeds` on your site rather than at a bare file, and ask them to
**subscribe rather than import**. A one-time import looks like it worked and then quietly
goes stale, still listing events you cancelled months ago.

### Promotion channels and the weekly list

Some places will never take a feed — they want a form filled in. Add each one as a
**channel** on the admin **Event promotion** page: what it is called, the link to their
submit form, how much notice they need, and which events are worth sending them.

Setting up eight destinations through a form eight times is worse than describing them,
so you can do it in chat instead:

> "Add the Louisville Chamber of Commerce as a promotion channel — submit once, big
> events only, they want two weeks' notice."
> "Add Instagram stories, day-of only."
> "The chamber is subscribing to our feed now — switch it over, I've confirmed it."

Pick a **campaign** rather than describing timings: `submit_once` for a calendar you fill
a form in for, `announce_and_remind` for a feed post, `day_of_only` for stories, or
`every_few_weeks` for periodic reminders. Kit works out which of those suit a one-off
versus a standing weekly series, so the same channel does the right thing for both.

Kit then keeps a running list on the Events page of what is outstanding, in the order it
needs doing. Ordering is by *their* deadline, not the event date: a calendar that wants
a fortnight's notice becomes urgent well before one that will take a listing the day
before. Each row carries the deep link and a copy button with the details ready to paste,
and you tick it off or skip it.

A missed reminder disappears rather than piling up — a "one week out" post is no use
three days before, and a list of everything you never got round to is not worth reading.
Standing series work differently again: they are not announced weekly, but you can set a
channel to remind you to post about trivia every few weeks, timed from the last time you
actually did.

Once a calendar agrees to subscribe to a feed, switch that channel to **They subscribe**
and it stops generating work entirely. That is the goal — every channel you move across
is a chore retired rather than a chore made faster.

### The table topper

The cog menu on the Events page prints the week's card for the taproom tables — **Table topper — this week** or **next week** — a coloured band per event with the day, the door time, a couple of lines about it, and the event's own poster. It's a PDF: two identical 4x6in cards on one landscape sheet, with dashed lines to cut along. One sheet covers two tables, and a cut card drops straight into a standard 4x6 table frame.

Nothing to lay out: it's built from the events you already entered, and only from **published, public** ones — a private booking never appears on a card sitting in front of customers. The bullets come from the event's **summary** — the one-line teaser — or, if you wrote the description as a list of short lines, from those lines instead. Repeating events show the date they land on *this* week, so a weekly quiz prints with this Wednesday on it.

**Write the summary for the card.** A band prints about two lines of 46 characters, so keep the first sentence under about 45 and the whole summary under about 90. The first sentence is the one that always survives: on a day with a second event on it, the band spends its other line naming that event, and the first sentence is all the headliner gets. Publishing an event tells you if its summary is longer than that.

**Don't repeat the day, the door time or the title.** The card already prints all three in the largest type on the page, so a summary that opens "Quiz night every Wednesday at 6:30pm" spends its only line saying what the reader can already see. Kit takes those back out where it can do so cleanly, but a line written to say something new beats one that had to be edited down. Say what the thing *is* and why someone would come.

Pick **next week** on a Friday and the card is ready before the weekend. Seven events is the most that fits on one card; anything beyond that is counted at the bottom rather than dropped silently.

Admin setup, on the **Events** page under Admin:

- **Pick the calendar.** Connect Google Calendar on the Integrations page first (a service account — see Square shift sync below for the same setup), then share your events calendar with the service account's email and choose it from the dropdown. If the dropdown is empty, the calendar hasn't been shared with the service account yet.
- **Set the website URL pattern**, e.g. `https://www.example.com/events/{slug}`. Each event's public link is built from this, so changing your domain later doesn't mean rewriting past events.
- **Copy the feed URL and token** into your website's build. The site fetches published public events from it and generates a page for each. The feed carries what's on over the **next two months, up to 20 events** — a website is a "what's on" page, not an archive, and a venue booked out to Christmas would otherwise hand the site a wall of dates nobody is planning around yet. Anything further out arrives on a later build as the events ahead of it happen, so nothing is lost.
- **Paste a build hook URL** (in Netlify: Site configuration → Build & deploy → Build hooks → Add build hook) if you want the website to rebuild itself. With one set, Kit publishes overnight at 2am whenever something is waiting to go out — so an event you add on Tuesday afternoon is live by Wednesday morning without anyone pressing anything. Nights where nothing changed are skipped, so it doesn't burn build minutes rebuilding an identical site. The **Publish** button on the Events page still rebuilds on demand when you don't want to wait.

Kit syncs to the calendar every 15 minutes. Use **Sync now** to push immediately, and **Check for drift** if the calendar has got out of step — someone deleted an entry by hand, say. That shows exactly what it would change before touching anything, and only ever touches entries Kit created.

### Telling staff what's on when they work

If you run scheduling in Square, Kit can post the day to a channel each morning: who's working and what's on, private bookings included, so nobody sets the room five minutes before thirty people arrive. The people on shift are @-mentioned, and the per-event detail goes in a thread — so the channel gets one line a day, not a wall of text.

Set it up on the **Event staff notices** page under Admin.

- **Pick the channel.** Kit has to be in it to post; channels it hasn't been invited to are greyed out, so run `/invite @Kit` there first. Leave it on "Nowhere" to turn notices off.
- **Pair people with Slack accounts, optionally.** Square and Kit know people by different ids and nothing links them, so each person on your published schedule gets a dropdown of your Slack members. This is only what turns a name into a ping — anyone unpaired is still named in the post, they just aren't notified. So notices work the day you pick a channel, and pairing improves them later.

Press **Preview** to see the exact post and its thread before anyone does. **Send now** posts today's; pressing it twice is safe, because an unchanged notice already posted isn't repeated. If the day's plan genuinely changes after the morning post, the next run picks up the difference.

Nothing on today means nothing is posted — a daily "nothing today" is how a channel learns to ignore a bot.

New hire? They appear in the dropdown once they're on the published Square schedule. You don't need them to have used Kit before — picking them creates their Kit account.

## Email

Connect any IMAP + SMTP mailbox so Kit can read your inbox and draft replies on your behalf. Gmail works via an app password (enable 2FA, then generate one at https://myaccount.google.com/apppasswords). iCloud, Yahoo, Fastmail, and self-hosted IMAP work with their normal passwords. Microsoft 365 / Outlook.com aren't supported yet — they require OAuth.

Connect it on the **Integrations** page in the web console: find the "Email — personal" connector and click **Connect**. You enter your password on a secure one-time form in the browser — it never passes through the assistant. (Email is self-service: any user can connect their own mailbox.)

Once configured, ask:

> "Any emails from Jim this week?"
> "Read uid 12345."
> "Draft a reply to that last email thanking them and proposing Thursday at 1pm."

**Sends always go through an approval card.** When you ask Kit to send email, the drafted message appears in your card stack — you can review it, long-press to revise ("make it more formal, drop the last paragraph"), and swipe to approve. Kit never sends directly. The body is markdown; the recipient's mail client sees both a rich-HTML and plain-text version.

Tools: `search_emails`, `read_email`, `mark_read`, `send_email` (agent-side only — `send_email` is not exposed via MCP because it's gated).

## Vault (shared passwords)

Kit's vault stores team logins — POS accounts, SaaS dashboards, Mailchimp, Squarespace, anything where a small group needs to share one set of credentials. Values are encrypted in the browser before they leave the user's device; Kit and the LLM never see plaintext.

The vault is locked behind **one shared master password per workspace** — the same model your members-only website probably uses. Anyone on your team who knows the password can unlock the vault; share it out-of-band the same way you share that website password. Each entry is also scoped to one role, so role membership decides who sees which entries even after unlocking. The "member" role includes everyone in the workspace; smaller roles like `managers` or `kitchen-staff` restrict visibility.

**First-time setup.** An admin opens the vault at `/{workspace-slug}/web/vault`; with no vault yet, the page offers a setup card to pick a master password (Kit suggests a strong passphrase — accept it or type your own). Copy it, share it with at least one teammate via your usual out-of-band channel, and write it down somewhere safe. **Kit cannot recover this password if it's forgotten by everyone.**

**Unlocking.** Every user unlocks with the same shared password at `/{workspace-slug}/web/vault` — the page prompts for it the first time you take an action that needs the key. Unlock lasts for the browser session (idle-times out after 10 minutes of no activity, hard-locks after 30 minutes total).

**Saving a secret.** Ask Kit, and Kit hands back a URL to a browser form — you type the password there, the browser encrypts it client-side, and the ciphertext is what hits the server. Kit will not accept a password pasted into chat:

> "Save the password for our Squarespace, scoped to managers."

**Using a secret.** Ask Kit by name; Kit returns a one-tap URL that opens the reveal page in your browser (you'll be prompted for the shared master password if your session has timed out):

> "What's the login for our POS?"
> "Find the Mailchimp password."

**Browsing.** The vault web UI at `/{workspace-slug}/web/vault` shows every entry you can see, with filters and an add button. Tools work the same: `list_secrets` (optionally `role_id`-filtered), `find_secret` for fuzzy lookup by name, `view_secret` for a reveal URL, `start_add_secret` for the add URL.

**Re-scoping or deleting.** `set_secret_role` changes which role owns an entry; `delete_secret` removes it (no undo — recoverable only by re-adding from another source). Both are gated through a confirmation card before they take effect.

**Rotating the password.** When you need to change the shared password (e.g. after an employee departure), an admin opens `/{workspace-slug}/web/vault` and uses **Rotate password**. Rotation requires the **old** password — the browser unwraps the existing vault key under it and re-wraps under the new one, so every stored secret keeps working. After rotation, share the new password out-of-band; open tabs on other devices re-lock automatically.

**If the master password is lost.** There is no recovery — the master password is the only thing that can decrypt the vault. If nobody remembers it, an admin can open `/{workspace-slug}/web/vault` and use **Destroy vault** to permanently delete every stored secret and start over with a fresh setup. It makes you re-type the workspace slug as a confirmation gate; there is no undo. The agent tool `nuke_vault` and MCP tool of the same name return this URL but never run the destruction themselves.

## Decisions and briefings (card stack)

Kit surfaces agent-driven prompts in a swipeable mobile card stack at `{base-url}/{workspace-slug}/` (sign in via Slack at `/{workspace-slug}/login`). Each workspace has its own URL — the system prompt names yours, so just ask Kit "what's my card stack URL?" in a DM. Install it to your home screen: iOS Safari → Share → Add to Home Screen; Android Chrome → ⋮ → Install app.

Two kinds of card:

- **Decisions** — a judgment call with 2-4 options and a recommended default.
  - **Swipe right** → approve the recommended option
  - **Tap** → open the detail view to pick any option
  - If the chosen option has a prompt, Kit queues a one-shot agent task that runs it (e.g. posts to a channel, sends a DM, calls a tool).
- **Briefings** — informational updates, usually recaps or alerts.
  - **Swipe right (👍)** → useful; archive it
  - **Swipe left (👎)** → not useful; dismiss it
  - **Tap** → open the detail view

Both thumbs up and thumbs down are recorded on the card (terminal state + timestamp + user), so the signal is available if you want to tune future briefings toward what's actually useful.

**What reaches the feed.** The stack is a triage queue, not a full list — it only carries what wants a decision now, so an empty feed means you're done, not that something is missing.

- **Tasks** appear when they're overdue, due in the next couple of days, or marked `blocker`. Everything else is still open and still yours; it just lives in the web console and `list_tasks` until its due date comes into range, and reappears on its own when it does. Nothing is archived or changed by dropping out of the feed. If a task matters and never shows up, give it a due date or bump it to `blocker`.
- **Briefings** at the default `info` severity clear themselves after three days, so routine "created 3 tasks" / "sync finished" notes stop costing a swipe. Mark a briefing `notable` or `important` and it waits until acked; any briefing can name its own `ttl_days` instead.
- **Decisions** never expire on their own. Someone has to answer them.

**Chat with a card.** Long-press any card (about 600ms) to open a chat panel bound to that card. Type a message or hold the mic button to talk — both land in the same conversation. Use it to modify, reschedule, or ask about the card without switching back to Slack. Follow-up messages attach to the same session, so you can say "make it high priority" and then "no, actually low" and Kit understands the correction. The panel stays open until you close it.

**Quick chat (the + button).** Tap the floating "+" button in the bottom-right of the feed for fast capture without a card in mind — "add a todo to buy milk", "remind me to call Pat tomorrow", "what decisions are open?". After the agent acts (creates a todo, etc.) the panel closes itself after about a second; tap the panel during that hold to keep it open if you want to correct. Questions, clarifications, and approvals keep the panel open automatically. Each open starts a fresh conversation — close and reopen for a clean slate.

Voice is optional — the mic button only appears in browsers with `MediaRecorder` support (Chrome/Firefox/Edge/Safari 14.5+; Firefox on mobile falls back to typed-only). Admin setup for transcription is documented in `CLAUDE.md`.

Create from an agent context (Slack, MCP, or a skill):

> "Create a decision to reorder Moonbeam hops with options: send the draft order, edit first, or skip."
> "Create a briefing about last night's sales — highest Thursday in 6 months."

Via MCP: `create_decision`, `create_briefing`, `update_decision`, `update_briefing`, `list_decisions`, `list_briefings`, `resolve_decision`, `ack_briefing`. Cards are scoped like other Kit resources — role, user, or tenant-wide.

## Website chat widget

You can embed Kit as a floating chat bubble on your own website (e.g. a Wix members-only page). Visitors click the bubble, ask a question, and get an answer drawn from your tenant's skills, rules, memories, and calendar. The widget is strictly read-only — visitors can't create tasks, save memories, or trigger any action. Kit-internal builtin skills (this user guide, etc.) and any skill wired into a scheduled job are filtered out automatically, so only your customer-facing knowledge surfaces.

**Setup (admin, one-time):**

> "Create a widget token for https://example.com"

Kit returns a token (shown once — save it) and a one-line `<script>` snippet. Paste the snippet into your site's custom-HTML block; the bubble appears in the bottom-right and survives client-side navigation.

**Manage tokens** with `list_widget_tokens` and `revoke_widget_token` (revoke stops the embed working immediately).

**Review conversations** any member of the workspace can ask:

> "What questions did people ask the widget this week?"
> "Did anyone ask about band camp?"
> "Show me conversations that didn't get a good answer."

Kit calls `list_widget_conversations`, `search_widget_conversations`, and `read_widget_conversation` to answer. Visitor identity is anonymous (a per-browser UUID), so cross-conversation grouping works without storing personal data.

## Menu boards and the printed menu

The **Menu** page (`/<your-slug>/web/menu`) holds one tap list per workspace and renders it two ways.

The **screen address** is the wall display: a permanent public URL you paste into a kiosk board once. Point the menu at your Untappd digital board with `set_menu_source` (the id is the number in `business.untappd.com/boards/<id>`) and the tap list follows — staff keep curating in Untappd exactly as they do now, and the screen re-checks when it asks, at most once a minute.

The **side panels** are the rotating rail beside the tap list — a weekly agenda, a poster, or a "book the space" call to action. Change them with `set_menu_panels`, which replaces the panels and **nothing else**: your taps, wordmark and footer are untouched whatever you pass. Use `set_menu_board` only when you mean to set the tap list or the venue chrome by hand, since that one replaces the whole document.

Panels hold **text, not a calendar**. Nothing in a panel expires on its own, so a one-off date written into one is still on the wall weeks after the event — write recurring wording like "Every Wed" instead, and let a scheduled job refresh anything dated.

Only beers that are **actually pouring** reach the printed menu — Kit takes that from whether Untappd prices a 4oz taster, which every tap has and nothing else does. A beer whose prices you clear in Untappd drops off the paper (and its heading with it, if it was the last one under it), and a can listed beside the taps never appears as though you could order a glass of it. Put cans and bottles in `extras` instead.

**Printable menu** on the same page opens a letter-sized PDF for the tables: a coloured band per section, and a row per beer with its style, ABV, half-pour and full-pour prices, and a sentence about it. It paginates itself, so a beer added in Untappd pushes the rest along instead of needing a designer. A beer that pours in something other than a pint carries its size beside the price, so nobody is quoted a 16oz they cannot order.

The sheet prints **what was last synced**, not whatever Untappd says at the moment you open it. Press **Sync from Untappd** on the settings page after the taps change. That keeps a third party off the critical path of somebody standing at a printer, and it means a failure to reach Untappd lands in front of you when you asked for it rather than silently subtracting the descriptions from a document.

Pages fill up before they break, and a section that doesn't fit continues on the next page under a repeated heading. Keeping whole sections together was tried and looked worse — three tall beers that won't fit leave the bottom half of a page white, which on a menu reads as a mistake rather than as design.

### Why descriptions often don't come down

Sync reads **two different Untappds, and they fail independently.**

- The digital board (`business.untappd.com`) has the beers, sections, prices and ABVs. It answers anybody, so this half essentially always works.
- The descriptions live on the consumer site (`untappd.com`), which sits behind Cloudflare bot management. It answers a laptop fine and answers a server with a challenge — so from Kit's own host this half usually **cannot** succeed, whatever the network is doing.

So "16 beers synced, 0 descriptions" is a normal result, not a fault, and Sync says so rather than leaving you to notice on paper. Three ways to fill them in:

1. **Type them** on the settings page, under "What each beer says". Anything written in Kit wins over Untappd and is never overwritten.
2. **Have an agent do it.** Point Claude Code (or any MCP harness) on your own machine at Kit: it can reach untappd.com, and `sync_menu_print` reports exactly which beers have no description, so it has a work list. It pushes them back with `set_menu_notes`, which **merges** — unlike `set_menu_print`, which replaces the whole configuration.
3. **Write them in Untappd** and sync from a machine that can reach it.

Stored descriptions are kept forever, so this is a once-per-new-beer job, not a recurring one.

### Configuring it

Configure the printed menu with `set_menu_print`, passing a JSON document:

- `brand` — your untappd.com slug, the `gravitybrewing` in `untappd.com/gravitybrewing`. Without it Sync doesn't even try for descriptions. They're fetched once per beer and then remembered, so a description you correct in Kit is not overwritten later.
- `title` / `subtitle` — the masthead, e.g. `"Beers"` and `"& Beverages"`.
- `flight` — the line above the footer, e.g. `"Try any set of four 4oz pours as a flight"`.
- `foot_left` / `foot_right` — the wifi and social lines.
- `colors` — section heading name to a `#rrggbb` bar colour. Sections you don't name get one from the house palette.
- `notes` — beer name to a description you write yourself. These win over Untappd, so they're the fix for a beer Untappd has no page for, and the way to say something on paper you don't want on your public brewery listing.
- `blurbs` — section heading to a single sentence printed under that heading. A heading that matches your tap list gets the sentence above its beers; a heading that matches nothing becomes a section of its own at the end of the menu. This is how **snacks** go on a beer menu: one bar saying `Snacks` and one line saying what's behind it, rather than a row and a price for every bag of pretzels.
- `hero` — the key of an image stored with `set_menu_asset`, printed behind the masthead. A `print_logo` asset, if you upload one, goes on the masthead too — use a white knockout, it sits on a colour band.
- `extras` — the rows Untappd has no opinion about: canned non-alcoholics, sodas, juice boxes. Each has a `section`, `name`, optional `style`, and `pours: [{size, label, price}]`. A section whose rows are all packaged prints one price column instead of three.

Everything is optional. A workspace that configures none of it still prints a usable menu — the headings and beers come from Untappd and the colours cycle.

All of the same settings have a form in the console, under **Admin → Printed menu** (`/<your-slug>/web/admin/menu`), which is usually easier than hand-writing the JSON.

## Kiosk screens

For screens that just sit on a wall running a browser — a lobby TV, a shop-floor dashboard — Kit gives each one a permanent address so you never have to walk over with a keyboard to change what it shows.

Set up a **board** per screen on the **Kiosk** page (`/<your-slug>/web/kiosk`): a name, an address key, and the URL it should display. The screen's address is `https://<your-kit>/<your-slug>/kiosk/<key>` — open that once in the kiosk's browser and it redirects to whatever the board currently points at. Later, change the URL on the page and the screen follows.

A screen only picks up changes on its own if something on the machine is watching. The setup panel on the page has a copy-paste shell loop that asks the board where to point every 30 seconds and reloads the browser when the answer changes. Without it, the screen shows whatever it loaded at boot until someone reloads it.

Each board shows **Live** once a machine is polling it, so a screen that has gone dark is visible from the page rather than from someone walking past it. A board with no URL yet shows a plain "no content assigned" card on the screen instead of an error.

Board addresses are **public and unauthenticated** — that's what lets a machine with no login use them. Anyone who knows the address can see where the screen points, so don't send a screen to a URL that is itself a private link.

## Trivia

A live pub quiz that runs on three screens at once: a host console you drive from a laptop, a big TV the room watches, and every team's own phone.

**The rules, as you read them out.** Five lines — six with the final wager on — and that is the whole game:

1. Everybody types a number. Closest **without going over** wins.
2. If everyone's too high, "smaller than all of these" wins.
3. Whoever wrote the winning answer takes the board money.
4. Then everyone bets: your $100 chip and your $200 chip — on **one answer or split across two**.
5. Chips on the winning answer pay their value. Wrong chips cost you nothing.
6. *(final wager on)* Last question: you'll see the **category first** — set your wager before the question. Then put it on whichever answer you like. Right doubles it, wrong loses it.

There is no buzzer and no adjudication — every team answers every question, all the guesses are revealed together, and because answers are numbers the round scores itself.

**Question sets.** On the **Trivia** page (`/<your-slug>/web/trivia`), create a game and give it some questions. Kit ships the Wits & Wagers question set, which you can add with one click, or upload your own CSV with `question`, `topics` and `answer` columns, in any order.

**What makes a good question here.** Not a normal pub-quiz question. The answer should be something nobody *knows* but anybody can reason toward — "how many islands make up Indonesia?" rather than "how many wives did Henry VIII have?". Recall questions break the game: everyone who knows the answer writes the same number, they all tie, and "closest without going over" has nothing left to separate them. Small answers are the warning sign — if most of your answers are under ten, every table will guess the same thing.

A **set** is just a named group of questions, and each game draws from whichever sets you tick. That's how you keep a Christmas quiz apart from an ordinary Tuesday. Uploading a set with a name that already exists replaces its contents, so fixing a typo and re-uploading does what you'd expect. Delete a set whenever you like — a game that's still in play will block it, but finished games won't, because they keep their own copy of every question they asked. Topics are separated by semicolons, so one question can belong to two categories. Answers must be numbers — that is what makes "closest without going over" work. Re-uploading a corrected sheet updates rows in place rather than duplicating them, and the report tells you what landed, what was a duplicate, and which line of the sheet had a typo.

**A question is asked once.** By default a game will not draw on anything an earlier night already asked, so a weekly quiz never repeats itself and you don't have to keep track. Only questions that were actually read out count — a cell nobody got round to opening is still fresh next week — and deleting an old game puts every question it asked back in the pot. The setup page and the question-sets page both show how many fresh questions you have left, per category and per set. If your bank runs thin, tick **Allow questions from past games** on that game's setup page rather than deleting history. Settings carry over too: a new game starts with the last one's board shape, values and timers.

**Build the board.** Pick your categories (or hit **Auto**) and Kit fills a 5 × 2 grid — ten questions, about half an hour. If a category doesn't have enough questions, you're told which one and by how much, at setup time rather than three questions into the night. "Not enough *fresh* questions" is a different problem with a different fix: upload more, delete an old game, or allow repeats.

**Two boards and a break fill a pub hour**, and that is the default — Jeopardy's shape. The night runs: a board, an intermission, a second board, then the final. Set **Board rounds** to 1 for a short night or 3 for a long one; it does not count the final. The break is a real stop — the TV puts the standings up and the join code stays in the corner, nothing counts down, and the night resumes when you press the button. That is the point of it: people get up, get a drink, and argue about question four.

Each round draws its own categories, so you tick ten rather than five (the first five are round one), and **each round is worth one more than the last** — $100 a cell, then $200, then $300, with the chips scaling to match. That keeps the balance where round one set it and gives a table that had a bad first half a real way back. It adds no rule: the room is told the same five lines and plays them twice.

**Going well? Add a round while it runs.** At the break, and on an emptied board with the final still to come, the live page offers **Add another round** — a fresh board of categories the night hasn't used, at the next value up. You don't have to guess the room's appetite at seven o'clock. It stops at three rounds, and it will tell you if the question bank has run out of unused categories rather than failing after you've announced it.

Every cell inside a round is worth the same, on purpose. Questions carry no difficulty grade, and the board fills each column by topic and a shuffle — so a dearer row would look harder without being harder. Rounds escalate; rows don't.

**Read the board before the room does.** Under the grid, the setup page lists what every tile will actually ask — grouped by category, cheapest first, with the answer beside each question. A category of ten good questions still has the one that landed wrong, or the one the regulars had last month at another pub, and you're the only person who can tell.

**Swap** rotates a single tile to the next question in that category and leaves the other nine alone — no rebuilding a board you were happy with to fix one cell. Press it again for another. The column and the money never move, so nothing changes for anyone but you. Each category header says how many spare questions it has to reach for; at *nothing spare* the button is off, and the fix is the usual one: upload more, delete an old game, or allow repeats. Questions freeze once the game starts.

Cells are worth $100 and $200 by default, the same as the chips, which makes betting the larger half of the game: only the table that *wrote* the winning answer takes a cell, but every table places chips every round. Raise the cell values in Settings if you'd rather knowing the answer outweighed reading the room.

**Put the TV on `/<your-slug>/trivia/tv` once and leave it.** That address always shows the newest game, so you never have to walk over and retype a URL — the same idea as a kiosk screen. Before the first game it shows a "no quiz tonight" card, and it picks up a new game on its own within about fifteen seconds. Each game also has its own screen address if you ever need to pin one night (two rooms, or reviewing an old game), but the stable one is what belongs on the wall. Because it follows the newest game, a stray game somebody made for a test is what the screen shows until it is gone — **Delete all games** on the Trivia page clears the list in one go (it asks twice; scores go with the games).

**Run it.** Put the QR code where people can scan it. Teams join by scanning; each table types its own name. Up to 20 teams.

**People can join all night.** The code stays in the corner of the TV between questions, and a table walking in on question seven takes a seat at $0 and plays from the next question — the betting and the final wager still give them a real shot. The one cut-off is the final itself: once it starts, a phone that scans is told the game is closing rather than sold a seat it can't use.

From the live page you pick a cell, then press one button per beat: **Reveal answers**, **Open betting**, **Score round**, **Next**. The clocks run themselves — 60 seconds to answer and 45 to bet by default, with betting opening the moment the cards come up (**Settings → Deal** adds a beat before it if you want one) — so you can put the laptop down. Closing the laptop doesn't stop the game: the timing lives on the server, and once everyone is in the clock drops to a few seconds rather than burning the rest of it.

**Nobody gets cut off mid-thought.** When the last table answers or puts down its last chip, the phase doesn't end on the spot — the clock drops to 5 seconds (**Settings → Grace after everyone's in**), so the table that acted last still gets to look at the room and change its mind, which every other table had time to do. Set it to 0 if you'd rather the night moved.

**Who picks the next category** is decided for you, so it is never the host's favourite. The first pick is drawn at random when you press Start — the TV spins a wheel with every table on it and lands on one. After that the pick goes to the table that wrote the winning answer; if several tables wrote it, to whichever of them is furthest behind, and if nobody wrote it at all, to the lowest-scoring table in the room. The board says whose pick it is and why, the live page gives you the same sentence to read out, and that table's phone says "it's your pick".

**If a table's phone dies**, tap their name on the live page and read out the four-digit code it gives you. That's the only way back in, on purpose — with twenty names on a TV screen, letting somebody pick a team off a list would let anyone play as anyone.

**The final wager** is the one round where a table risks its own money, and the bet comes **before** the question. The screen shows the category alone, every table locks an amount blind (30s by default — **Settings → Wager**), and only then is the question read out. After the reveal they put that stake on whichever answer they like. Right doubles it, wrong loses it, and $0 is a real choice — it's the leader's defensive play. Nobody can finish below $0.

The final is **on by default**. You can switch it off per game (**Settings → Final wager**), and with it off scores only ever go up and no stake control appears on any phone — the setting to use for a first night, when the one rule where you can *lose* money is the one people get wrong.

**There's a break before the final too** — the biggest one of the night. When the last board is spent, the game goes to the break rather than straight into a blind wager: standings up, a last drink, and you press **Start the final** when the room is back.

**The night never ends itself.** When the last board is spent the game waits on the break screen with the standings up, and you press **Start the final**, **Add a board** or **Go to the podium** when the room tells you which it wants.

**Ask about it afterwards.** In Slack:

> "How did trivia go last night?"
> "What were the results for jumping-lion?"

Kit calls `trivia_status` and `trivia_results`. Those are the only two trivia tools and both are read-only — nothing about running a live game is improved by asking an assistant to press the button in a loud room.

Game addresses are **public and unauthenticated**: that's what lets somebody scan a QR code and play without a Kit account. Anyone with the link can watch, and a phone with no cookie gets the full read-only view.
