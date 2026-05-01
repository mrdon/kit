# Builder Admin Guide

Kit's builder lets tenant admins ship apps by writing Python scripts that Kit runs in a sandbox. You author scripts from Claude Code (or any MCP-connected harness) using Kit's admin meta-tools. Kit stores each script, executes it safely against the tenant's data, and — when you're ready — publishes selected functions as first-class agent/MCP tools regular users invoke via Slack, voice, or the swipe PWA.

This guide is the map. It assumes you've connected Claude Code to your tenant's Kit MCP as an admin.

## Workflow at a glance

1. `create_app` groups all your related scripts, schedules, and exposed tools into one bundle.
2. `app_create_script` adds Python source under that bundle.
3. `app_run_script` executes a function synchronously so you can test interactively.
4. `app_expose_tool` makes a function callable by your team through the normal agent/MCP surface.
5. `app_schedule_script` fires a function on a cron expression.
6. `app_rollback_script_run` reverses data mutations from a specific run if something went wrong.

### The shape of each call

```
# 1. Bundle
create_app(name="crm", description="Customer tracking + intake")

# 2. Script
app_create_script(
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
app_run_script(app="crm", script="main", fn="add_contact",
           args={"name": "Jane", "email": "Jane@Example.com"})

# 4. Expose it to your team
app_expose_tool(
    app="crm",
    script="main",
    fn_name="add_contact",
    tool_name="crm_add_contact",
    description="Add a contact to the CRM. Deduplicates by lowercased email.",
    args_schema={"type": "object", "required": ["name", "email"], "properties": {
        "name": {"type": "string"}, "email": {"type": "string"}
    }},
    visible_to_roles=["sales"]  # admins see it too via the superuser bypass
)

# 5. (Optional) schedule
app_schedule_script(app="crm", script="main", fn="nightly_cleanup", cron="0 2 * * *")

# 6. Undo a bad run
app_rollback_script_run(run_id="<uuid from app_run_script>", confirm=true)
```

Every meta-tool is admin-only. Non-admin callers never see them in their tool list and get a forbidden error on direct invocation.

## Recipe: from admin prompt to working app

When an admin says "build me an app for X", work through these steps in order. Most of them take one MCP call; the script body is the creative part.

1. **Clarify scope** before writing anything. Ask the admin, in one or two targeted questions (not four):
   - Who records data vs. who reads it? (Per-user? Team-wide? Role-gated?)
   - Are there admin-only actions (e.g. "editing a closed record")?
   - Any scheduled digests / briefings / Slack posts?
   - Any external integrations? (There are none in v0.1 — surface that constraint early if the ask implies one.)

2. **Pick collection name(s).** One concept per collection. Plural nouns: `contacts`, `shifts`, `reviews`.

3. **Sketch the doc shape in comments** before writing code. What fields are set at insert vs. updated later? Which reference the caller (see "Identity and per-user scoping" below)? Which fields are admin-editable only?

4. **Write one script** starting with the simplest "add" function. `app_run_script` to smoke-test with a sample input *before* adding more functions or exposing anything. **Load `writing-monty-scripts` now** if you haven't already — it covers the language-level quirks (no classes, no imports, no f-strings, weird date-math edges) and gives you copy-paste helpers.

5. **Add read/list functions.** Smoke-test each via `app_run_script`.

6. **Add mutate/delete functions** with any admin checks in place (see "Admin-only modifications after close" below).

7. **Expose each function as a tool** with a crisp `description` and a complete `args_schema`. Set `visible_to_roles` explicitly — never leave it empty unless the tool is truly open to everyone.

8. **Schedule anything recurring** via `app_schedule_script`. A scheduled builder script creates a task under the hood; for scheduled work that touches channels, DMs, or admin-gated actions you should pass a `policy` block — see the `creating-jobs` skill for how to design `allowed_tools` / `force_gate` / `pinned_args`. Policies cannot be ignored by prompt drift; function bodies can.

9. **Sanity-check** with `app_list_tools`, `app_list_schedules`, `app_script_stats`. If the admin will use this tomorrow, plan to check `app_script_stats` after the first day of real traffic.

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

## Identity and per-user scoping

`current_user()` returns the caller's identity as a dict:

```python
current_user()
# → {"id": "<uuid>", "display_name": "Alice", "timezone": "America/Denver",
#    "roles": ["admin", "sales"]}
# "admin" and "member" are builtin roles every tenant has. Check admin
# status via `"admin" in current_user()["roles"]`.
```

Use it whenever the script's behaviour depends on who called it — recording, listing, or authorising.

**Never accept `user_id` as a tool argument.** Anyone invoking the tool via MCP or an agent could pass any other user's id. `current_user()` is the only trustworthy identity source inside a script.

### Per-user-data pattern

For apps where each user records and reads their own data, always store `user_id` at insert and always filter by it on read. Denormalise `user_name` into the doc so reports don't have to re-resolve every user.

```python
def record_thing(description):
    me = current_user()
    return db_insert_one("things", {
        "user_id":      me["id"],
        "user_name":    me["display_name"],
        "description":  description,
        "created_at":   now(),
    })

def list_my_things(limit=20):
    me = current_user()
    return db_find("things",
                   {"user_id": me["id"]},
                   limit=limit,
                   sort=[("_created_at", -1)])
```

### Admin-scope overlays

If admins see everyone's data but users see only their own, branch on admin role membership:

```python
def list_things(limit=50, user_id=None):
    me = current_user()
    is_admin = "admin" in me["roles"]
    filter = {}
    if is_admin and user_id:
        filter["user_id"] = user_id       # admin filtered to a specific user
    elif not is_admin:
        filter["user_id"] = me["id"]      # non-admin always scoped to self
    # admin with no user_id = see all (empty filter)
    return db_find("things", filter, limit=limit,
                   sort=[("_created_at", -1)])
```

Note: only admins should be trusted with the optional `user_id`. The check above silently ignores non-admin attempts to filter by someone else's id rather than raising — that keeps the tool convenient when a non-admin passes a legitimate-seeming parameter, while still enforcing the scope.

## Admin-only modifications after close

Some apps let users write records but only admins edit or delete them once "closed" (e.g. time entries, submitted reviews). Enforce this with two layers:

1. **Registry layer** — `visible_to_roles=["admin"]`. Names the builtin admin role explicitly so the tool is visible to admins across every surface — including `tools_call`, where `visible_to_roles` is authoritative and the admin superuser bypass does NOT apply.
2. **Script layer** — inside the function, re-check `"admin" in current_user()["roles"]` and raise `PermissionError("admin only")` if false.

Both matter. Without the registry layer, any non-admin with a matching role would see the tool in their catalog and could attempt to call it. Without the script layer, a `tools_call` from another script — or a future widening of `visible_to_roles` — could quietly grant access without re-reviewing the function body.

```python
# Exposed with visible_to_roles=["admin"]  (admin-only across every surface)
def admin_edit_thing(thing_id, **fields):
    if "admin" not in current_user()["roles"]:
        raise PermissionError("admin only")
    db_update_one("things", {"_id": thing_id}, {"$set": fields})
    return {"ok": True}
```

The `app_items_history` trigger captures every UPDATE/DELETE for free, so admin edits are audited without extra code. If an edit goes wrong, `app_rollback_script_run(run_id=...)` reverses that run's mutations.

**On the admin flag.** Admin status is membership in the builtin `admin` role — a tenant-scoped superuser flag (Django-style `is_superuser`, but bounded to the caller's tenant). Admins bypass `visible_to_roles` / role-scope filters **at the agent + MCP registry surface** — so via the LLM or Claude-Code MCP, `visible_to_roles=["manager"]` means "managers AND admins see it." Note that `tools_call` inside a script does **NOT** grant that bypass (it's authoritative): if a tool needs to be callable from other scripts as an admin, include `"admin"` in its `visible_to_roles` explicitly. Scripts check their own caller via `"admin" in current_user()["roles"]`.

## Field-type conventions

The v0.1 item store is schemaless JSONB. You pick the conventions. Stick to these so tools and admins can read each other's data.

- **Dates and times** → ISO8601 strings. `now()` returns UTC RFC3339Nano; `today()` returns `YYYY-MM-DD` in the caller's timezone. Strings sort lexically — so `ORDER BY _created_at DESC` and range predicates like `{"start_at": {"$gte": "2026-04-20T00:00:00Z", "$lte": "2026-04-26T23:59:59Z"}}` both work directly against RFC3339 strings. ($gte / $lte compare numerically for numbers and lexically for strings.)
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
- **Watch `app_script_stats`.** `app_script_stats(app="...", days=7)` rolls up cost cents, tokens, and run counts. Budget surprises show up here first.

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

When a run does abort mid-mutation, use `app_rollback_script_run(run_id=..., confirm=true)` to reverse the data changes via the temporal history tables. Schedules, exposed tools, and memory writes are not rolled back — only `app_items` mutations.

For post-mortems, `app_script_logs(run_id=...)` returns the `log()` trail the script wrote.

## Debugging

- **Run synchronously first.** `app_run_script` waits for the run to finish and returns the function's value plus a `run_id`. Test every function there before you schedule or expose it.
- **Log generously.** `log("info", "triaging review", review_id=r_id, length=len(text))` writes a row to `app_script_logs` keyed to the current run. Cheap. Fast. Pull the trail with `app_script_logs(run_id=...)`.
- **Use `app_script_stats`.** `app_script_stats(app="crm", days=7)` surfaces completed/errors/limits/cancelled counts, avg and max duration, tokens, and cost. The first place to look when you think "this app has been weird lately".
- **Read the script_runs audit.** Each run records `status`, `duration_ms`, `mutation_summary`, plus `parent_run_id` when invoked via `tools_call`. The whole lineage is queryable.
- **Side-effecting actions may fail under `app_run_script`.** `dm_user` / `send_slack_message` and other Slack-touching actions can error with `user_not_found` or similar when invoked via the admin harness, because the synthetic caller may not have a real Slack identity. Pattern: for any function that sends a message or DM, expose a parallel `preview_*` function that returns the message body without side effects — use it for smoke tests, leave the real sender for the scheduler / live Slack invocations.

## Common shapes

- **CRUD-only.** Single script, no LLM, no schedule. One exposed `add_*` and one `list_*` function. Example: `mug_club`.
- **Scheduled digest.** One script with a `run()` function that reads recent items and posts to a channel. One `app_schedule_script` entry. Example: `weekly_digest`.
- **LLM-assisted triage.** Scheduled `run()` that picks up untriaged items, calls `llm_classify` / `llm_extract`, writes results back, and optionally emits a decision card via `create_decision`. Example: `review_triage`.
- **Multi-script with shared helpers.** `utils` + `dal` + `main`, composed with `shared(...)`. Example: `crm`.
- **Event-driven.** v0.1 has no event triggers. Simulate with a frequent (`*/5 * * * *`) scheduled function that polls. True `on_insert` / `on_update` hooks arrive in v0.2.

## Not supported in v0.1

Know these before you write code the sandbox will reject.

**Load the `writing-monty-scripts` skill for the full language-level detail** — what's rejected, idiomatic workarounds, copy-paste helpers (weekday via Zeller's, add-days, ISO normalisation, zero-pad), and the `preview_*` pattern for Slack-touching functions. Headlines only here:

- No `class` definitions. Plain dicts + module-level functions.
- No `import` of anything. No `datetime`, `json`, `re`, `requests`, `math`.
- No `"%s" % n` / f-strings. String concat + a `_zp` zero-pad helper.
- `try/except` does NOT catch host-call errors (`db_*`, `llm_*`, actions, Slack). Return early on expected edges.
- Filter operators `$or` / `$and` / `$regex` / `$exists` / `$type` are v0.2.
- Bulk writes (`*_many`) are v0.2 — iterate in Python.
- `shared(...)` target fn takes kwargs only.
- `tools_call` is one level of nesting max.
- No weekday / start-of-week helpers; use Zeller's (see the skill).
- `date_add` / `date_diff` require full RFC3339 with seconds + `Z`; normalise user input first.

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
| `create_briefing(title, body, severity=, role_scopes=)` | Emit a briefing card. `role_scopes` is a list of role names that must exist in the tenant — every tenant has the builtin `admin` and `member` roles, so `role_scopes=["admin"]` restricts to admins. For tenant-wide visibility leave `role_scopes=[]` (admins also see everything at the agent surface via the superuser bypass). For per-user delivery use `dm_user`. |
| `create_job(description, cron=, timezone=, channel=, run_once=, policy=)` | Kit task (scheduled prompt). **See the `creating-jobs` skill** for description + `policy` design — scheduled prompts fire with no human in the loop, so structural rails matter more than wording. |
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
| `current_user()` | `{id, display_name, timezone, roles}` for the caller. The only trustworthy identity source — never accept `user_id` as a tool arg. Check admin via `"admin" in roles`. |
| `now()` | UTC RFC3339Nano string. |
| `today()` | `YYYY-MM-DD` in caller's timezone. Must be extended (`today() + "T00:00:00Z"`) before passing to `date_add` / `date_diff`. |
| `date_add(dt, days=, hours=, minutes=, seconds=)` | Shifted RFC3339Nano. `dt` MUST be full RFC3339 with seconds + timezone (`YYYY-MM-DDTHH:MM:SSZ` or offset). Strings like `2026-04-22T09:00` error — normalise user input first (append `:00Z` or call `parse_iso_datetime` style helper). |
| `date_diff(a, b)` | `a-b` in seconds, float. Same input strictness as `date_add`. |
| `log(level, message, **fields)` | Structured log row on the current run. |

### Composition

| Function | Purpose |
|---|---|
| `shared("script", "fn", **kwargs)` | Within-app helper call. Cheap; no new run row. |
| `tools_call("exposed_tool_name", {"arg": "value"})` | Cross-app exposed-tool call. Opens child run. |

### Meta-tools (admin-only, called from Claude Code)

App lifecycle: `create_app`, `list_apps`, `get_app`, `delete_app`, `purge_app_data`.
Scripts: `app_create_script`, `app_update_script`, `app_list_scripts`, `app_get_script`, `app_run_script`, `app_rollback_script_run`.
Schedules: `app_schedule_script`, `app_unschedule_script`, `app_list_schedules`.
Exposure: `app_expose_tool`, `app_revoke_tool`, `app_list_tools`.
Diagnostics: `app_script_logs`, `app_script_stats`.

## Pre-flight checklist

- [ ] App has one clear purpose. New concern → new app.
- [ ] Scripts split by concern once they pass ~200 LOC (`utils`, `dal`, `main`).
- [ ] Every insert has a validator, even a 10-line one.
- [ ] Every mutable field uses atomic operators, not read-modify-write.
- [ ] Per-user scripts derive the owner from `current_user()`, never from a tool argument. Stored as `user_id` on the doc; filtered on every read.
- [ ] Admin-only functions use `visible_to_roles=["admin"]` at registry time (names the builtin admin role so it works everywhere, including `tools_call`) AND re-check `"admin" in current_user()["roles"]` inside the body.
- [ ] Money is cents (int). Dates are ISO8601 strings. Emails are lowercased.
- [ ] Exposed tool descriptions are precise enough the LLM picks them correctly — same discipline as `creating-skills`.
- [ ] `app_run_script` works before `app_schedule_script` runs or `app_expose_tool` publishes.
- [ ] Budget-sensitive code early-returns on obvious noise before calling `llm_*`.
- [ ] You checked `app_script_stats` after the first day of real traffic.

---

This file is the canonical source. It is embedded into the binary as an admin-only built-in skill at `internal/skills/builtins/builder-admin-guide/SKILL.md`, which reproduces the same content with skill frontmatter. Edits here should be mirrored to the SKILL.md wrapper so the admin agent sees the current version.
