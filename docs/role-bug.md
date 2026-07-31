# Bug: MCP user identifiers are inconsistent across tools

## Status

Open. Reproduced 2026-04-08 from a Claude Code MCP session against `https://kit.twdata.org/mcp`.

## Summary

The MCP surface uses two different user identifiers in two different tools, with no way for a caller to translate between them:

- `mcp__kit__list_role_members` returns **Slack user IDs** (`U09AN7KJU3G`).
- `mcp__kit__create_todo` and `mcp__kit__update_todo` require **kit internal UUIDs** in the `assigned_to` field. Passing a Slack ID returns `Invalid assigned_to UUID`.
- There is no MCP tool to enumerate users, look a user up by Slack ID, or otherwise resolve `Slack ID → kit UUID`.

The result: an MCP client can see *who is in a role* but cannot programmatically *assign that person to a todo*. Any cross-tool user reference requires out-of-band knowledge that no MCP tool exposes.

## Reproduction

```text
1. Call list_role_members(role_name="member")
   → Returns: "- U09AN7KJU3G"

2. Call create_todo(title="...", role_scope="founder")
   → Succeeds, returns todo UUID

3. Call update_todo(todo_id=<uuid>, assigned_to="U09AN7KJU3G")
   → Error: "Invalid assigned_to UUID."
```

The specific session was a Claude Code agent trying to assign a real founder todo (file IRS Form 8832) to Matt Gorbsky based on his role membership. The agent had to fall back to leaving the todo unassigned and naming Matt in the description.

## Why this matters

- **Agent workflows can't compose tools.** The whole point of having both `list_role_members` and `create_todo` in the same MCP surface is that an agent should be able to chain them: "find the people in role X, assign work to person Y." Right now that chain is broken at the join.
- **Silent failure mode.** A naive caller will pass the Slack ID (the only user identifier kit has handed them) and get a validation error with no hint that a different identifier exists somewhere or how to obtain it.
- **No documentation pointer.** The error message says "Invalid assigned_to UUID" but doesn't say where to get a valid one. There's no MCP tool the caller can fall back to.

## Probable root cause

`list_role_members` is rendering the `users.slack_user_id` column for display, while `todos.assigned_to` is a foreign key to `users.id` (the kit-internal UUID). Two valid identifiers for the same row, but only one is exposed via MCP.

Worth checking `internal/models/user.go` and the `list_role_members` handler to confirm. The fix lives in one of three places.

## Proposed fixes (any one would close the gap)

### Option A — `list_role_members` returns UUIDs (or both)

Smallest change. Make `list_role_members` return the kit UUID alongside (or instead of) the Slack ID. Format suggestion:

```text
- Matt Gorbsky <U09AN7KJU3G> (kit UUID: 7f3a…)
```

Or as structured output if the MCP tool supports JSON. Display name is also worth including — right now `list_role_members` returns only an opaque ID, which forces every caller to already know who that is.

### Option B — `assigned_to` accepts either identifier

Make the `create_todo` / `update_todo` handlers normalize: if the input matches a Slack ID pattern, look up the corresponding `users.id`; otherwise treat it as a UUID. Caller doesn't need to care which identifier they have.

This is the most caller-friendly option but adds parsing logic to every tool that takes a user reference.

### Option C — Add a `find_user` MCP tool

```text
find_user(slack_id?: string, email?: string, name?: string) → {id, slack_id, name, email, roles}
```

Lets a caller resolve one identifier to the other on demand. Works as a general-purpose lookup, not just for the role-members → todo path. Probably the right long-term answer regardless of A or B, since other tools that take user references will hit the same wall.

## Recommendation

**Option A + Option C, in that order.** Option A is a one-line fix that immediately unblocks the role-members → todo pattern. Option C is the general-purpose primitive that future cross-user-reference tools will need anyway. Option B (input normalization) is tempting but adds duplicated parsing to every tool surface and obscures what kit's canonical user identifier actually is — better to expose UUIDs and a lookup tool than to paper over the duality.

## Related observations

- The `assigned_to` field in the schema is documented as "User UUID to assign to" but the only tool that returns user identifiers (`list_role_members`) doesn't return UUIDs. The schema documentation is technically correct and the tools are individually reasonable; the bug is in the *gap between them*.
- `list_role_members` returns only an identifier, not a display name. Even if the identifier issue were fixed, agents would still have no way to confirm "yes, U09AN7KJU3G is the Matt I'm looking for" without out-of-band info. Adding `name` to the output (whether `display_name`, `real_name`, or whatever the user record has) would close that loop.
