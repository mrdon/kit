---
name: gated-tools-guide
description: "How to add a gated tool to Kit (like send_email, future edit_doc) and how the decision-card approval mechanism works end-to-end. Covers DefaultPolicy, the approval.Token ceremony, handler idempotency via resolve_token, shadow-path discipline, client preview components, and testing. Use when adding or modifying any tool whose side effects need human approval before execution."
---

# Gated tools in Kit

A gated tool's call is intercepted by `tools.Registry.Execute` and wrapped in a decision card the user approves. Only after approval does the handler run — via the same registry, with an unforgeable `approval.Token` on ctx. This is the **single enforcement point** for dangerous-tool authorization in Kit.

## Concept model

```
agent → Registry.Execute(send_email, args)
       → Def.DefaultPolicy == PolicyGate
       → approval.FromCtx(ctx) → (no token)
       → createGateCard(tenant, user, tool, args) → card {id} created
       → returns "HALTED: send_email requires human approval. Decision card {id}..."
       → agent short-circuits turn, tells user "I've queued that for your review"

(user reviews in PWA, optionally revises via chat, taps Approve)

PWA → HTTP POST /api/v1/stack/items/cards/decision/{id}/action {option_id: approve}
     → CardService.ResolveDecision
        → re-checks PolicyLookup + is_gate_artifact (tamper defense)
        → flips card to 'resolving' with deadline + resolve_token, commits
        → ToolExecutor: approval.WithToken(ctx, Mint(cardID, resolveToken))
        → Registry.Execute(send_email, args)
          → Def.DefaultPolicy == PolicyGate
          → approval.FromCtx(ctx) → (token present)
          → dispatches Def.Handler
            → handler dedupes by resolveToken, runs SMTP, returns message-id
        → captures result, flips card to 'resolved' with resolved_tool_result
```

The authoring session (if any) gets a `decision_resolved` event with a truncated tool_result so the workflow resumes with context.

## When to gate

Gate (set `DefaultPolicy: tools.PolicyGate`):
- Operations with irreversible side effects visible to third parties: `send_email`, `post_to_channel` in untrusted channels, `schedule_calendar_event` on external calendars.
- Operations that modify external systems: `edit_doc`, `edit_spreadsheet`, `move_file`.
- Any operation with meaningful cost or reputational risk.

Do NOT gate (keep `PolicyAllow`):
- Reads (`find_user`, `list_todos`, `get_decision_tool_result`).
- Internal-only writes the user can trivially undo (`create_todo`, `update_memory`). Voice-style flows like "create me 3 todos" need these to run directly.
- Idempotent state-syncing operations.

When in doubt, leave it `PolicyAllow` and rely on the confidence dial (agents can voluntarily gate via `create_decision(tool_name, tool_arguments, ...)` when uncertain).

## Registering a gated tool

Minimum change to add `send_email` as a `PolicyGate` tool:

```go
// in internal/tools/mailer.go (or wherever)
func registerMailerTools(r *tools.Registry) {
    r.Register(tools.Def{
        Name:          "send_email",
        Description:   "Send an email to a list of recipients. Subject and body support markdown.",
        DefaultPolicy: tools.PolicyGate, // <- the one-line change that gates it
        Schema: /* JSON schema for {to, subject, body, ...} */,
        Handler: sendEmailHandler,
    })
}
```

Handler contract:

```go
func sendEmailHandler(ec *tools.ExecContext, input json.RawMessage) (string, error) {
    // By the time we get here, Registry.Execute has already verified
    // approval.FromCtx(ec.Ctx) is populated. Extract the resolve
    // token — it's the idempotency key.
    _, resolveToken, _ := approval.FromCtx(ec.Ctx)

    var args SendEmailArgs
    _ = json.Unmarshal(input, &args)

    // MANDATORY: check the dedupe table first. If this resolve_token
    // has already sent, return the cached result without re-sending.
    // The scheduler's stuck-resolving sweep can requeue a wedged
    // card; without dedupe the tool runs twice.
    if prior, ok := db.LookupSentEmail(ec.Ctx, resolveToken); ok {
        return prior.MessageID, nil
    }

    msgID, err := mailer.send(ec.Ctx, args) // package-private or guarded
    if err != nil {
        return "", err
    }
    _ = db.RecordSentEmail(ec.Ctx, resolveToken, msgID)
    return msgID, nil
}
```

## Shadow-path discipline (non-negotiable)

The registry gate protects callers that go through `tools.Registry.Execute`. It does NOT protect against direct calls to the underlying dangerous operation. A future developer adding `mailer.Send(ctx, ...)` to `internal/apps/builder/action_builtins.go` would bypass the gate entirely.

**Pick one of two patterns for every PolicyGate tool:**

1. **Package-private operation.** The dangerous function is lowercase, in its own package, with the tool handler as the only caller:
   ```go
   // internal/mailer/mailer.go
   package mailer
   func send(ctx context.Context, args Args) (string, error) { ... }
   ```
   Nothing outside `internal/mailer` can import `send`. If another caller needs email, they add a `PolicyAllow` wrapper or go through the registry.

2. **Self-guarded operation.** The function is exported but guards itself on ctx:
   ```go
   // internal/mailer/mailer.go
   func Send(ctx context.Context, args Args) (string, error) {
       guard.RequireApproval(ctx) // panics if ctx lacks the marker
       // ... real send ...
   }
   ```
   The `guard` ctx-key marker is set only when `Registry.Execute` dispatches a PolicyGate handler. Builder action_builtins and MCP direct-calls panic; only the registry path works.

Pick whichever fits; do not leave a public, unguarded service function alongside a gated tool.

See `CLAUDE.md` for the repo-wide rule.

## Client preview component

Every gated tool needs a preview in the PWA so users see the action before approving. Add `web/app/src/kinds/tool_previews/<tool_name>.tsx`:

```tsx
import type { ToolPreviewProps } from './index';

type SendEmailArgs = { to: string[]; subject: string; body: string };

export function SendEmailPreview({ args }: ToolPreviewProps) {
  const a = args as SendEmailArgs;
  return (
    <div className="tool-preview tool-preview--email">
      <div><strong>To:</strong> {a.to.join(', ')}</div>
      <div><strong>Subject:</strong> {a.subject}</div>
      <pre>{a.body}</pre>
    </div>
  );
}
```

Register in `web/app/src/kinds/tool_previews/index.tsx`:

```ts
toolPreviews['send_email'] = SendEmailPreview;
```

Without a dedicated preview, the card falls back to `JsonPreview` (a collapsed `<details>` JSON view). Fine for MVP of a narrow tool; required for user-facing gated tools.

## Authoring-agent guidance (creating gated cards)

Agents can create decision cards that gate a concrete tool call via `create_decision`:

```
create_decision(
  title: "Email reply to Jim",
  context: "Jim emailed about last week's lunch. I drafted a short thank-you.",
  options: [
    {
      option_id: "send",
      label: "Send",
      tool_name: "send_email",
      tool_arguments: {
        to: ["jim@acme.com"],
        subject: "Great lunch, thanks!",
        body: "Hi Jim, …",
      },
      // prompt is POST-execution follow-up work
      prompt: "after sending, mark the 'reply to Jim' todo complete",
    },
    { option_id: "skip", label: "Skip" },
  ],
  recommended_option_id: "send",
  priority: "medium",
)
```

For PolicyGate tools the agent can also just call the tool directly — `Registry.Execute` auto-creates an equivalent card. Explicit `create_decision` lets you customize title/body/prompt for richer context.

## What NEVER to do

1. **Never construct an `approval.Token`** and call `WithToken` outside `CardService.ResolveDecision` (one legitimate call site, grep-enforced).
2. **Never add a non-registry entry point** to a PolicyGate operation. No direct builder action_builtins, no MCP handler that bypasses `tools.Registry.Execute`, no helpers in other packages that duplicate the tool logic.
3. **Never claim the action happened** when you see a tool_result starting with `HALTED:`. Tell the user you've queued it.
4. **Never populate `tool_arguments`** on an option with unsanitized third-party content without wrapping in `<untrusted>` tags for the chat-revision LLM. (The chat path's `buildCardSystemSuffix` already does this.)
5. **Never remove `is_gate_artifact` validation** from `ResolveDecision`. The re-check is the only thing catching post-creation tamper.
6. **Never forget handler-side `resolve_token` dedupe** for a PolicyGate tool with side effects. The scheduler sweep can requeue a wedged card — without dedupe, that re-runs the tool.

## Testing checklist

For every new gated tool:

- Unit: `Registry.Execute` with no approval token returns `HaltedPrefix` output and does not invoke the handler.
- Unit: `Registry.Execute` with approval token dispatches the handler.
- Unit: handler is idempotent on repeated `resolve_token` — second call returns cached result without side effect.
- Integration: `CardService.CreateDecision` with this tool_name stamps `is_gate_artifact = true`.
- Integration: `ResolveDecision` refuses a card where `is_gate_artifact = false` and tool_name points at your gated tool (simulated tamper via direct DB UPDATE).
- Client: preview component renders with representative args.

See `internal/apps/cards/gated_tools_test.go` for the full test pattern (uses `internal/tools/testgated`'s `_test_gated_echo` stand-in).

## Debugging pointers

- **Card stuck in `resolving` forever.** Scheduler sweep runs every 60s — past `resolving_deadline` it flips back to `pending` with `last_error` set. If the sweep isn't running: check scheduler logs for "periodic sweep failed"; manual recovery is a SQL `UPDATE app_cards SET state='pending' WHERE id=$1` + clear `resolving_deadline` / `resolve_token`.
- **Card refused at resolve time with "not a gate artifact".** Either the tool was PolicyAllow at creation and later promoted (the card needs recreating), or someone tampered with `tool_name` post-hoc. Check `app_card_decisions.is_gate_artifact`.
- **"HALTED:" appearing to the user.** System-prompt rule + `executeTools` short-circuit should prevent this. If seen, the agent's system prompt lost the rule — check `internal/agent/context.go`'s `buildSystemPrompt` output.
- **Double-send.** Check the handler's dedupe table; `resolve_token` must be persisted BEFORE the side effect. The sweep can legitimately cause a retry on a card that appeared wedged but actually succeeded.
- **Session events for audit.** `list_sessions` MCP tool + `get_session_events` reveal the `decision_resolved` event (truncated tool_result) and every `tool_results` block from the agent's turn. Full result lives on `app_card_decisions.resolved_tool_result` (fetch via `get_decision_tool_result(card_id)`).

## Key files

- `internal/tools/registry.go` — `Policy`, `Def.DefaultPolicy`, `Registry.Execute` policy dispatch.
- `internal/tools/approval/approval.go` — the unforgeable `Token`.
- `internal/apps/cards/service.go` — `CreateDecision` stamps `is_gate_artifact`, `ResolveDecision` re-checks + runs tool + records result, `ReviseDecisionOption` narrow revise path.
- `internal/apps/cards/db_mutate.go` — `sweepStuckResolvingCards` recovery.
- `internal/agent/agent.go` — `executeTools` short-circuits on `HaltedPrefix`; `rebuildHistory` renders the `decision_resolved` event.
- `internal/chat/chat.go` — `buildCardSystemSuffix` surfaces options with `<untrusted>` wrapper for chat-revise; `DropGatedTools: true` drops gated tools from the chat registry.
- `web/app/src/kinds/cards_decision.tsx` + `web/app/src/kinds/tool_previews/` — PWA preview dispatch + resolving-state UI.
- `internal/tools/testgated/` — test-only `_test_gated_echo` stand-in tool.
- `internal/apps/cards/gated_tools_test.go` — full-loop integration tests.
