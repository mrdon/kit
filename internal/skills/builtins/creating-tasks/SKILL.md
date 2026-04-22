---
name: creating-tasks
description: "How to create a scheduled task: picking cron_expr vs run_at, timezone handling, scope and channel_id, writing a description the scheduled agent can execute, and designing the optional `policy` block (allow-list, force-gate, pinned args) so the agent cannot go off-script. Use whenever creating, editing, or advising on a task."
---

# Creating Tasks

Read this before calling `create_task` (or advising on one). A scheduled task is **a prompt that fires without you in the loop** — you won't see the conversation, so the description must stand on its own and any safety rails must be structural, not persuasive.

## The two moving parts

Every `create_task` call has two independent concerns. Get both right:

1. **The description** — the prompt the scheduled agent runs. Plain text. Be concrete about data sources, name tools explicitly, specify output shape. The agent has no conversation context when this fires.
2. **The policy** (optional) — a capability manifest the registry enforces. Use when the task touches outbound channels, has a sensitive argument (which channel, which recipient), or needs to stay inside a narrow toolset. **Prompts can be ignored; policies cannot.**

## Schedule

Pick **exactly one** of `cron_expr` or `run_at`:

- `cron_expr`: recurring. 5-field classic cron — `minute hour day-of-month month day-of-week`. Examples: `0 9 * * MON` (Monday 9 AM), `0 16 * * FRI` (Friday 4 PM), `*/30 * * * *` (every 30 min).
- `run_at`: one-time. ISO 8601 in the caller's timezone — `2026-04-05T21:20:00` or without seconds. Must be in the future.

Timezone resolution order: user's profile → Slack user profile → tenant → UTC.

## Scope

Who can see and run the task:

- `"user"` (default) — personal to the caller. Tasks show up in their `list_tasks` only.
- `"tenant"` — visible tenant-wide. **Admin only.**
- A role name (e.g. `"founders"`) — visible to members of that role. Caller must hold the role or be admin.

Scope also controls what the task's agent can read from todos, decisions, memories, etc. at fire time — the agent runs with the creator's identity, so it sees only what the creator sees.

## channel_id

Where the task's agent posts by default if its description says "post" without naming a channel. For tasks using `post_to_channel` explicitly with a pinned argument, `channel_id` can be omitted.

## Writing the description

The description is the prompt at fire time. Write it like briefing a teammate with amnesia:

- **Concrete data sources.** Not "summarise recent activity" — "list open todos (via `list_todos`), decisions resolved in the last 7 days (via `list_decisions`), and active briefings (via `list_briefings`)."
- **Name the output tool.** "Post a Slack mrkdwn message to the channel via `post_to_channel`." This nudges the agent toward the right terminal.
- **Specify shape.** "4–8 bullet points, Slack mrkdwn, no headers."
- **Guardrails.** "Do not include ticket numbers." "If no todos are open, post nothing."

**Don't rely on the description for safety.** If the task must not post to the wrong channel, that goes in the policy — not in the description. LLMs can and will skip "require approval" instructions on a bad run.

## The policy block

Three independent fields; set any combination:

```json
{
  "allowed_tools": ["list_todos", "list_decisions", "post_to_channel"],
  "force_gate":    ["post_to_channel"],
  "pinned_args":   { "post_to_channel": { "channel": "C09FOUNDERS" } }
}
```

### allowed_tools

Narrows the registry for this task's runs. Four states:

- **Absent / `null`** — no restriction. The agent can call any tool it normally could.
- **Empty `[]`** — allow nothing except agent-infrastructure (`load_skill`, `load_skill_file`, etc.). Use for read-only reporter tasks where the agent should only consult skills, no external calls.
- **Non-empty list** — only these tools plus infrastructure. The list is *literal* — no wildcards.

If the agent calls a tool not on the list, the registry returns `tool "X" not permitted by task policy` as the tool result. The agent sees the error and self-corrects within the same turn.

Infrastructure tools always allowed regardless: `load_skill`, `load_skill_file`, `resolve_decision`, `revise_decision_option`, `send_slack_message`.

### force_gate

Tool names that always route through an approval card at fire time, even if the agent omitted `require_approval: true`. The approval card is identical to the one the agent could have requested voluntarily — same chat-revise, same approve/skip. The difference: **the task creator guarantees the gate, not the LLM.**

Reach for this whenever a silent post would be bad: public-channel announcements, customer-facing DMs, any content whose phrasing the creator wants to preview.

`force_gate` overrides `DenyCallerGate` (the per-tool flag that suppresses the agent's own opt-in) — task contracts win over tool defaults.

### pinned_args

Per-tool key→value overrides. The pinned value replaces the agent's value *before* the gate check, so:

- The approval card preview shows the pinned (true) arguments the user actually approves.
- The handler that eventually runs receives the pinned arguments.

Use when an argument's value is a hard constraint, not an LLM decision:

- **Channel** — `{"post_to_channel": {"channel": "C09FOUNDERS"}}` — no matter what the agent says, the post lands in #founders.
- **Recipient** — `{"dm_user": {"user_id": "U0ABCDEF"}}` — task always DMs the same person.
- **Safety flags** — `{"delete_memory": {"confirm": false}}` — agent can never actually delete.

Pinning **silently overrides** — the handler doesn't error if the agent supplied a different value; the override is logged as a `policy_enforced` session_event so `get_session_events` shows the audit trail.

## Worked examples

### Weekly state-of-the-company post

```json
{
  "description": "Draft a weekly state-of-the-company post. Pull signal from open todos (list_todos), decisions resolved in the last 7 days (list_decisions), active briefings (list_briefings). 4–8 bullets, Slack mrkdwn, no headers. Post via post_to_channel.",
  "cron_expr": "0 9 * * MON",
  "channel_id": "C09FOUNDERS",
  "policy": {
    "allowed_tools": ["list_todos", "list_decisions", "list_briefings", "search_memories", "post_to_channel"],
    "force_gate": ["post_to_channel"],
    "pinned_args": { "post_to_channel": { "channel": "C09FOUNDERS" } }
  }
}
```

Rationale: allow-list shrinks the surface to just the read tools + one terminal; force_gate means every weekly post lands as a card you review; pinned channel means even a confused agent cannot post to #general.

### Read-only daily standup brief (no posting)

```json
{
  "description": "Summarise yesterday's completed todos and today's scheduled briefings. Return the summary as your final message; do not post to any channel.",
  "cron_expr": "0 8 * * MON-FRI",
  "policy": {
    "allowed_tools": []
  }
}
```

Rationale: `allowed_tools: []` means only infrastructure runs — no posts, no side effects. The agent's "final message" lives in the task's session log for manual review via `get_session_events`.

### Single-recipient nag

```json
{
  "description": "Check open todos assigned to the recipient. If any are overdue, DM a polite nudge listing them.",
  "cron_expr": "0 10 * * TUE",
  "policy": {
    "allowed_tools": ["list_todos", "dm_user"],
    "pinned_args": { "dm_user": { "user_id": "U0ABCDEF" } }
  }
}
```

Rationale: no `force_gate` (the creator is OK with the DM going out automatically); pinned recipient means the agent cannot DM someone else even if the description is misread.

## Testing

- **`run_task` with `dry_run: true`** — runs the agent end-to-end without real side effects. Use after creating a task to verify the description produces sensible tool calls.
- **Private test channel first** — if the task posts, point it at a channel only you see, run it once with `run_task`, verify the approval card / message, then point it at production.
- **Tighten iteratively** — if the agent skips the pinned channel or calls an unlisted tool, watch the `policy_enforced` events in `get_session_events` to confirm enforcement fired.

## Gotchas

- **Policies don't elevate privileges.** A policy can't grant access to a tool the creator couldn't already call. A non-admin listing an admin-only tool in `allowed_tools` errors at `create_task`.
- **Pinned args are frozen into pending cards.** If you edit a task's policy (via `update_task`) while a gate card is pending, the card still carries the *old* pinned value — the user already saw and approved that shape. Next firing uses the new policy.
- **Empty description = no-op safeguard.** A task with a description the agent can't meaningfully act on will spin, error, and eventually DM you the failure. Prefer specifying a graceful "do nothing" terminal ("if X is empty, end without posting").
- **`update_task` replaces the policy wholesale.** A non-nil `policy` argument overwrites all three fields. To tweak one, read the current policy via `list_tasks` and re-send the full shape.
