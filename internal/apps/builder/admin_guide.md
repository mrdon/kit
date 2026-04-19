# Builder Admin Guide

Kit's builder lets tenant admins ship apps by writing Python scripts that Kit runs in a sandbox. You author scripts from Claude Code (or any MCP-connected harness) using Kit's admin meta-tools. Kit stores each script, executes it safely against the tenant's data, and — when you're ready — publishes selected functions as first-class agent/MCP tools regular users invoke via Slack, voice, or the swipe PWA.

This guide is the map. It assumes you've connected Claude Code to your tenant's Kit MCP as an admin.

## Workflow at a glance

1. `create_app` groups all your related scripts, schedules, and exposed tools into one bundle.
2. `create_script` adds Python source under that bundle.
3. `run_script` executes a function synchronously so you can test interactively.
4. `expose_script_function_as_tool` makes a function callable by your team through the normal agent/MCP surface.
5. `schedule_script` fires a function on a cron expression.
6. `rollback_script_run` reverses data mutations from a specific run if something went wrong.

### The shape of each call

```
# 1. Bundle
create_app(name="crm", description="Customer tracking + intake")

# 2. Script
create_script(
    app="crm",
    name="main",
    body="""
def add_contact(name, email):
    row = db_insert_one("contacts", {"name": name, "email": email.lower()})
    return {"contact_id": row["_id"]}
""",
    description="CRM business logic"
)

# 3. Test it
run_script(app="crm", script="main", fn="add_contact",
           args={"name": "Jane", "email": "Jane@Example.com"})

# 4. Expose it to your team
expose_script_function_as_tool(
    app="crm",
    script="main",
    fn_name="add_contact",
    tool_name="crm_add_contact",
    description="Add a contact to the CRM. Deduplicates by lowercased email.",
    args_schema={"type": "object", "required": ["name", "email"], "properties": {
        "name": {"type": "string"}, "email": {"type": "string"}
    }},
    visible_to_roles=["admin", "sales"]
)

# 5. (Optional) schedule
schedule_script(app="crm", script="main", fn="nightly_cleanup", cron="0 2 * * *")

# 6. Undo a bad run
rollback_script_run(run_id="<uuid from run_script>", confirm=true)
```

Every meta-tool is admin-only. Non-admin callers never see them in their tool list and get a forbidden error on direct invocation.

## Service-layer pattern

For a one-function app, inline is fine. For anything nontrivial, split by concern so each script stays under ~200 LOC and testable in isolation.

The canonical three-script shape:

- **`utils`** — pure helpers: validators, formatters, ID mints. No DB, no LLM, no actions.
- **`dal`** (optional) — data access. `add_contact(data)`, `list_contacts_by_owner(owner_id)`. Wraps `db_*` so the rest of the app doesn't know collection names.
- **`main`** — business logic + the functions you expose. Calls the other two via `shared("utils", "format_phone", phone=...)` and `shared("dal", "add_contact", data=...)`.

### Worked CRM example

**utils** (shared helpers):

```python
def format_phone(phone):
    digits = "".join(c for c in phone if c.isdigit())
    if len(digits) == 10:
        return "+1" + digits
    if len(digits) == 11 and digits.startswith("1"):
        return "+" + digits
    return None  # caller decides what to do with invalid input

def validate_contact(doc):
    errors = []
    if not doc.get("name"):
        errors.append("name required")
    email = doc.get("email", "")
    if "@" not in email or len(email) < 5:
        errors.append("email missing or invalid")
    return errors
```

**dal** (data access):

```python
def add_contact(doc):
    doc["email"] = doc["email"].lower()
    return db_insert_one("contacts", doc)

def find_contact_by_email(email):
    return db_find_one("contacts", {"email": email.lower()})

def list_by_owner(owner_id, limit=50):
    return db_find("contacts",
                   {"owner_id": owner_id},
                   limit=limit,
                   sort=[("_created_at", -1)])
```

**main** (business logic, exposed):

```python
def add_contact(name, email, phone=None, owner_id=None):
    doc = {"name": name, "email": email, "owner_id": owner_id}
    if phone:
        formatted = shared("utils", "format_phone", phone=phone)
        if formatted is None:
            return {"ok": False, "error": "invalid phone"}
        doc["phone"] = formatted

    errors = shared("utils", "validate_contact", doc=doc)
    if errors:
        return {"ok": False, "errors": errors}

    existing = shared("dal", "find_contact_by_email", email=email)
    if existing:
        return {"ok": False, "error": "duplicate", "contact_id": existing["_id"]}

    row = shared("dal", "add_contact", doc=doc)
    return {"ok": True, "contact_id": row["_id"]}
```

`shared("script", "fn", **kwargs)` dispatches in-app. No new `script_runs` row per hop — cheap helper calls. Cross-app composition uses `tools_call("exposed_tool_name", {...})` which *does* open a child run for audit.

## Field-type conventions

The v0.1 item store is schemaless JSONB. You pick the conventions. Stick to these so tools and admins can read each other's data.

- **Dates and times** → ISO8601 strings. `now()` returns UTC RFC3339Nano; `today()` returns `YYYY-MM-DD` in the caller's timezone. Strings sort lexically — you get `ORDER BY _created_at DESC` almost for free.
- **Money** → store cents as integer. Never float. `{"amount_cents": 1299}` not `{"amount": 12.99}`. Format at the edge when you render.
- **Email** → lowercased string. Validate presence of `@` before insert. Lowercase means deduplication via `$in` and `$eq` just works.
- **Phone** → E.164 (`+15551234567`) or digits-only, but pick one and stick to it. The `format_phone` helper above normalises on the way in.
- **References** between docs → `{"ref_app": "crm", "ref_collection": "contacts", "ref_id": "c_abc"}` sub-object. Even within one app, keeping the triple lets you migrate cleanly later.
- **Enums** → string values, filter with `$in`. `{"status": {"$in": ["open", "in_progress"]}}`. No enum type in JSONB; the discipline is yours.
- **Booleans** → `True` / `False` round-trip through the Monty WASM boundary as native booleans. Don't use `"y"` / `"n"`.

Auto system fields `_id`, `_created_at`, `_updated_at` are populated by the runtime on insert (and `_updated_at` on every update). Don't try to set them — the writes are ignored.

## Validation patterns

No heavy validation lib in v0.1. Write your own `validate_*(doc)` helpers in the `utils` script, return a list of error strings, branch on empty list. Keep each validator under ~15 LOC.

```python
def validate_contact(doc):
    errors = []
    if not doc.get("name"):
        errors.append("name required")
    if "@" not in doc.get("email", ""):
        errors.append("email missing or invalid")
    if doc.get("tier") and doc["tier"] not in ["bronze", "silver", "gold"]:
        errors.append("tier must be bronze/silver/gold")
    return errors
```

Why not raise on failure? Because the caller usually wants to collect every problem at once ("name required; email missing or invalid") rather than discover them one at a time across N round-trips.

## Concurrency gotchas

Tenants will run your scripts concurrently. Two scheduled ticks, two users hitting the same exposed tool, a Slack command and a cron fire colliding — all realistic.

**Always prefer atomic update operators over read-modify-write.**

Safe:

```python
db_update_one("reviews", {"_id": review_id}, {"$inc": {"reply_count": 1}})
db_update_one("contacts", {"_id": c_id}, {"$push": {"notes": {"text": "called", "at": now()}}})
db_update_one("contacts", {"_id": c_id}, {"$addToSet": {"tags": "vip"}})
```

Unsafe — race window between read and write, updates get lost:

```python
c = db_find_one("contacts", {"_id": c_id})
c["notes"].append({"text": "called", "at": now()})
db_update_one("contacts", {"_id": c_id}, {"$set": {"notes": c["notes"]}})
# Concurrent writer between find and update? Their note vanished.
```

Supported atomic operators in v0.1: `$set`, `$unset`, `$push`, `$pull`, `$addToSet`, `$inc`. They all resolve at the Postgres jsonb level without a round-trip through your script.

## LLM budget awareness

Every tenant has a daily LLM spend cap (default $5.00, stored as 500 cents in `tenant_builder_config.llm_daily_cent_cap`). Each `llm_*` call:

1. Pre-checks today's spend against the cap. Over-cap → the call fails before contacting Anthropic.
2. Logs tokens in/out + cost cents to `llm_call_log` keyed by `script_run_id`.
3. Increments the running daily spend.

Tier cheat-sheet:

| Call | Model | Use for |
|---|---|---|
| `llm_classify(text, categories)` | Haiku | Pick one of N labels. Cheap. |
| `llm_extract(text, schema)` | Haiku | Pull structured fields out of text. Cheap. |
| `llm_summarize(text, max_words=60)` | Haiku | Short summaries. Cheap. |
| `llm_generate(prompt, max_tokens=200)` | Sonnet by default | Free-form generation. Pricier — use sparingly. |

You can override the model with `model="haiku"` / `"sonnet"` / `"opus"` when the default isn't right, but the defaults are chosen to keep the usual paths on Haiku.

Tips:

- **Early-return on noise.** Scan with cheap string checks before spending tokens. If an incoming review has fewer than 20 characters, skip `llm_classify` entirely.
- **Batch when possible.** One `llm_extract` call over 10 concatenated items can be half the cost of 10 separate calls. Have the schema return a list.
- **Cache results in `app_items`.** If you re-triage the same text every hour, store the classification on the item. Check the item first, only call `llm_classify` on cache miss.
- **Watch `script_stats`.** `script_stats(app="...", days=7)` rolls up cost cents, tokens, and run counts. Budget surprises show up here first.

## Error handling

Monty (the script sandbox) distinguishes two error classes:

- **Host call errors** — any error returned by a `db_*`, `llm_*`, action, or meta call *unwinds* the interpreter. Python `try/except` cannot catch it. Treat every `db_*` failure, every `llm_*` cap hit, every `send_slack_message` network error as fatal-to-run. If you need the script to keep going on failure, return early on the edge cases that you know about.
- **Script-raised errors** — `raise ValueError("...")` inside Python is catchable by Python `try/except` and does not unwind. Use `raise` for your own control flow.

Example:

```python
def process(review_id):
    review = db_find_one("reviews", {"_id": review_id})
    if review is None:
        # Don't raise — just end the run cleanly.
        return {"ok": False, "reason": "not found"}
    # db_update_one below is a host call; if it errors the run aborts.
    db_update_one("reviews", {"_id": review_id}, {"$set": {"processed_at": now()}})
    return {"ok": True}
```

When a run does abort mid-mutation, use `rollback_script_run(run_id=..., confirm=true)` to reverse the data changes via the temporal history tables. Schedules, exposed tools, and memory writes are not rolled back — only `app_items` mutations.

For post-mortems, `script_logs(run_id=...)` returns the `log()` trail the script wrote.

## Debugging

- **Run synchronously first.** `run_script` waits for the run to finish and returns the function's value plus a `run_id`. Test every function there before you schedule or expose it.
- **Log generously.** `log("info", "triaging review", review_id=r_id, length=len(text))` writes a row to `script_logs` keyed to the current run. Cheap. Fast. Pull the trail with `script_logs(run_id=...)`.
- **Use `script_stats`.** `script_stats(app="crm", days=7)` surfaces completed/errors/limits/cancelled counts, avg and max duration, tokens, and cost. The first place to look when you think "this app has been weird lately".
- **Read the script_runs audit.** Each run records `status`, `duration_ms`, `mutation_summary`, plus `parent_run_id` when invoked via `tools_call`. The whole lineage is queryable.

## Common shapes

- **CRUD-only.** Single script, no LLM, no schedule. One exposed `add_*` and one `list_*` function. Example: `mug_club`.
- **Scheduled digest.** One script with a `run()` function that reads recent items and posts to a channel. One `schedule_script` entry. Example: `weekly_digest`.
- **LLM-assisted triage.** Scheduled `run()` that picks up untriaged items, calls `llm_classify` / `llm_extract`, writes results back, and optionally emits a decision card via `create_decision`. Example: `review_triage`.
- **Multi-script with shared helpers.** `utils` + `dal` + `main`, composed with `shared(...)`. Example: `crm`.
- **Event-driven.** v0.1 has no event triggers. Simulate with a frequent (`*/5 * * * *`) scheduled function that polls. True `on_insert` / `on_update` hooks arrive in v0.2.

## Not supported in v0.1

Know these before you write code the sandbox will reject.

- `class Foo:` — Monty doesn't support user-defined classes. Use plain dicts and functions.
- `import` of any kind — scripts get only the allowlisted built-ins listed below. No `requests`, no `datetime`, no `json`.
- `try/except` around host calls — those errors unwind. Python-raised errors are catchable.
- Aggregation pipelines — do aggregation in Python using `db_find` results.
- Filter operators `$or`, `$and`, `$regex`, `$exists`, `$type` — deferred to v0.2. Use multiple separate queries or narrow in Python.
- Bulk writes (`insert_many` / `update_many` / `delete_many`) — iterate in Python for v0.1.
- Positional args to `shared(...)` target functions — pass target kwargs only.
- Deeper than one level of `tools_call` nesting — an exposed tool cannot itself call `tools_call`.

## Quick reference

### Data built-ins

| Function | Purpose |
|---|---|
| `db_insert_one(collection, doc)` | Insert a doc. Returns the stored doc with system fields. |
| `db_find_one(collection, filter)` | First match or `None`. |
| `db_find(collection, filter, limit=, skip=, sort=[("field", 1\|-1)])` | List of matches. |
| `db_update_one(collection, filter, update)` | Atomic update. Returns count. |
| `db_delete_one(collection, filter)` | Delete one. Returns count. |
| `db_count_documents(collection, filter)` | Count without loading. |

### Action built-ins

| Function | Purpose |
|---|---|
| `create_todo(title, description=, priority=, due_date=, role_scope=, assigned_to=, private=)` | Create a Kit todo. |
| `update_todo(todo_id, status=, priority=, due_date=, role_scope=, blocked_reason=, assigned_to=)` | Update one. |
| `complete_todo(todo_id, note=)` | Mark done. |
| `add_todo_comment(todo_id, content)` | Comment on a todo. |
| `create_decision(title, body, options, priority=, role_scopes=)` | Emit a decision card. |
| `create_briefing(title, body, severity=, role_scopes=)` | Emit a briefing card. |
| `create_task(description, cron=, timezone=, channel=, run_once=)` | Kit task, not script schedule. |
| `add_memory(content, scope_type=, scope_value=)` | Save a memory. |
| `send_slack_message(channel, text, thread_ts=)` | Post to Slack. |
| `post_to_channel(channel, text, thread_ts=)` | Alias for `send_slack_message`. |
| `dm_user(user_id, text)` | Send a DM. |
| `find_user(name_or_mention)` | Resolve to `{id, display_name, slack_user_id}` or `None`. |

### LLM built-ins

| Function | Model | Purpose |
|---|---|---|
| `llm_classify(text, categories, model=None)` | Haiku | Returns one category string. |
| `llm_extract(text, schema)` | Haiku | Returns a dict matching `schema`. |
| `llm_summarize(text, max_words=60, model=None)` | Haiku | Returns a summary string. |
| `llm_generate(prompt, max_tokens=200, model=None, schema=None)` | Sonnet | Returns string or dict. |

### Utility built-ins

| Function | Purpose |
|---|---|
| `now()` | UTC RFC3339Nano string. |
| `today()` | `YYYY-MM-DD` in caller's timezone. |
| `date_add(dt, days=, hours=, minutes=, seconds=)` | Shifted RFC3339Nano. |
| `date_diff(a, b)` | `a-b` in seconds, float. |
| `log(level, message, **fields)` | Structured log row on the current run. |

### Composition

| Function | Purpose |
|---|---|
| `shared("script", "fn", **kwargs)` | Within-app helper call. Cheap; no new run row. |
| `tools_call("exposed_tool_name", {"arg": "value"})` | Cross-app exposed-tool call. Opens child run. |

### Meta-tools (admin-only, called from Claude Code)

App lifecycle: `create_app`, `list_apps`, `get_app`, `delete_app`, `purge_app_data`.
Scripts: `create_script`, `update_script`, `list_scripts`, `get_script`, `run_script`, `rollback_script_run`.
Schedules: `schedule_script`, `unschedule_script`, `list_schedules`.
Exposure: `expose_script_function_as_tool`, `revoke_exposed_tool`, `list_exposed_tools`.
Diagnostics: `script_logs`, `script_stats`.

## Pre-flight checklist

- [ ] App has one clear purpose. New concern → new app.
- [ ] Scripts split by concern once they pass ~200 LOC (`utils`, `dal`, `main`).
- [ ] Every insert has a validator, even a 10-line one.
- [ ] Every mutable field uses atomic operators, not read-modify-write.
- [ ] Money is cents (int). Dates are ISO8601 strings. Emails are lowercased.
- [ ] Exposed tool descriptions are precise enough the LLM picks them correctly — same discipline as `creating-skills`.
- [ ] `run_script` works before `schedule_script` runs or `expose_script_function_as_tool` publishes.
- [ ] Budget-sensitive code early-returns on obvious noise before calling `llm_*`.
- [ ] You checked `script_stats` after the first day of real traffic.

---

This file is the canonical source. It is embedded into the binary as an admin-only built-in skill at `internal/skills/builtins/builder-admin-guide/SKILL.md`, which reproduces the same content with skill frontmatter. Edits here should be mirrored to the SKILL.md wrapper so the admin agent sees the current version.
