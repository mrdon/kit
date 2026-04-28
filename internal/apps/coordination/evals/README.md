# Coordination evals

Fixture-based eval suite for `parseMeetingReply`. The deterministic parts
of coordination (state machine, slot generation, recompute) are covered
by unit tests; this suite covers the LLM-driven parser, which is the
piece most likely to regress when prompts get tweaked or model versions
change.

## Layout

```
evals/parse_meeting_reply/
  <case_name>/
    input.json     — message log + candidate slots fed to parseMeetingReply
    expected.json  — strict JSON match for intent + current_constraints
    rubric.md      — (optional) natural-language criteria for the LLM judge path
```

## Two ways to run

### Path A: API runner (production fidelity)

```bash
make eval-parse
```

Runs the Go test under build tag `eval` (not in `make prepush` because
it costs money and depends on the live API). Hits the same Anthropic
endpoint, model, and prompt the engine uses in production. Compares
`intent` exactly and `current_constraints` structurally against
`expected.json`. Fails the case on any mismatch.

Use this:
- Before tightening or rewriting `prompts/parse_meeting_reply.txt`
- When bumping the production model version
- After any change to `internal/apps/coordination/llm.go`

Cost: pennies per full run with the seed cases.

### Path B: Claude Code judge (cheaper iteration)

Runs the parser via the same Go path (Haiku, production prompt) to get
an actual model output, but uses Claude Code as the **judge** instead
of exact-match comparison. Useful for cases where multiple outputs are
acceptable and a strict JSON expected file is brittle.

```bash
# Step 1: run the parser via the Go test runner with output capture.
make eval-parse-capture > /tmp/parse-results.json

# Step 2: from your Claude Code session, walk the results and grade each
# against the rubric.md in its case directory.
```

A skill at `.claude/skills/coordination-eval-judge.md` documents the
exact procedure. Pass criteria are spelled out in each case's
`rubric.md`; cases without a rubric fall back to exact-match.

Costs: zero additional API spend (rolls into your Claude Code usage).
Trade-off: Claude Code as judge isn't necessarily Haiku, so production
fidelity for the *judging* step varies. The *parse* step still uses
Haiku.

## Adding a new case

1. Create a directory under `parse_meeting_reply/<descriptive_name>/`
2. Drop `input.json` with `candidate_slots` + `message_log`
3. Drop `expected.json` with the intent + (optional) constraints
4. Optionally add `rubric.md` for the Claude Code judge path

That's it — both runners walk the directory automatically.

## Seed cases

| Case | Tests |
|---|---|
| `single_turn_accept` | Basic "Tue 10am works" → reply, accept slot |
| `single_turn_decline` | "I can't make any of those" → decline |
| `single_turn_out_of_window` | "Try the week after" → out_of_window |
| `single_turn_unrelated` | "What's the PTO policy?" → unrelated (parser falls through) |
| `single_turn_ambiguous` | "Let me check and get back to you" → ambiguous |
| `multi_turn_correction` | "Free at 10" then "no actually 11" → only 11 accepted |

More seed cases (counter-proposal, conditional constraint, polite-no,
multiple-slots, partial-outside-window, decline-with-alternative,
multi-coord-disambig) can be added following the same pattern.
