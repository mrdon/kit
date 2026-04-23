---
name: writing-monty-scripts
description: "Monty Python-WASM quirks and workarounds for authoring builder scripts — what isn't supported, Python idioms that break, helpers you have to write yourself (zero-pad, Zeller weekday, add-days, ISO normalisation), smoke-testing patterns for side-effecting actions, error-handling rules. Load this alongside builder-admin-guide whenever authoring, debugging, or updating a script body that will run in Kit's builder sandbox."
admin_only: true
---

# Writing Monty Scripts

Builder scripts run inside **Monty**, a Python interpreter compiled to WebAssembly. Most Python you write works. This skill catalogues the places it doesn't, with the standard workarounds. See `builder-admin-guide` for the meta-tool workflow and the full host-function reference; this skill focuses on the language surface.

## Not supported in v0.1

- **`class Foo:`** — no user-defined classes. Use plain dicts + module-level functions.
- **`import` of any kind** — scripts get only the allowlisted host builtins. No `requests`, `datetime`, `json`, `re`, `math`. If you need something standard-library-ish, either write it yourself (see workarounds below) or fetch it through an action / LLM builtin.
- **`"%04d" % n` and f-strings** — `%`-formatting errors with `TypeError: unsupported operand type(s) for %: 'str' and 'tuple'`; f-strings aren't supported either. Use string concatenation and helpers.
- **`try/except` around host calls** — any error from a `db_*`, `llm_*`, action, meta, or Slack call unwinds the interpreter and is NOT catchable from Python. See "Error handling" below. Python-raised errors (`raise ValueError("msg")`) ARE catchable.
- **Filter operators `$or`, `$and`, `$regex`, `$exists`, `$type`** — deferred to v0.2. Use multiple separate queries or narrow in Python.
- **Bulk writes (`insert_many` / `update_many` / `delete_many`)** — iterate in Python.
- **Positional args to `shared(...)` target functions** — the called function must be invoked with kwargs only: `shared("utils", "fn", kw=val)`, never `shared("utils", "fn", val)`.
- **Deeper than one level of `tools_call` nesting** — an exposed tool invoked via `tools_call` cannot itself call `tools_call`.
- **Weekday / "start of week" / "add N days" helpers** — none builtin. Use Zeller's on `YYYY-MM-DD` strings (helpers below).
- **`date_add` / `date_diff` on partial timestamps** — inputs must be full RFC3339 with seconds AND a `Z` or offset. `"2026-04-22T09:00"` (no seconds) errors. `today()` (no time at all) errors. Normalise before calling.

## What still works

- Dicts, lists, tuples, strings, ints, floats, bools, None.
- Control flow: `if / elif / else`, `for`, `while`, `break / continue`, `return`.
- Comprehensions: list, dict, set, generator expressions.
- Slicing, unpacking, `*args` / `**kwargs` on your own defs.
- `len`, `range`, `enumerate`, `sorted`, `reversed`, `min`, `max`, `sum`, `map`, `filter`, `zip`, `any`, `all`.
- `str.split`, `str.join`, `str.strip`, `str.lower`, `str.upper`, `str.replace`, `str.startswith`, `str.endswith`, `isinstance`, `round`, `abs`.
- Host APIs: `db_*`, `current_user`, `now`, `today`, `date_add`, `date_diff`, `log`, `shared`, `tools_call`, `create_*`, `update_*`, `complete_*`, `send_slack_message`, `post_to_channel`, `dm_user`, `find_user`, `add_memory`, `llm_*`. Full reference in `builder-admin-guide`.

## Standard workaround helpers

Paste these into your `utils` script if you need any of them.

### Zero-pad integer to width

```python
def _zp(n, width):
    s = str(int(n))
    while len(s) < width:
        s = "0" + s
    return s
# _zp(7, 4) -> "0007"
```

### Format year/month/day into `YYYY-MM-DD`

```python
def _fmt_date(y, m, d):
    return _zp(y, 4) + "-" + _zp(m, 2) + "-" + _zp(d, 2)
```

### Weekday (0=Mon .. 6=Sun) via Zeller's congruence

```python
def _weekday(date_str):
    """0=Mon .. 6=Sun, given a YYYY-MM-DD string."""
    y = int(date_str[:4]); m = int(date_str[5:7]); d = int(date_str[8:10])
    if m < 3:
        m += 12; y -= 1
    k = y % 100; j = y // 100
    h = (d + (13 * (m + 1)) // 5 + k + k // 4 + j // 4 + 5 * j) % 7
    return (h + 5) % 7
```

### Add N days to a `YYYY-MM-DD`

```python
def _days_in_month(y, m):
    if m in (1, 3, 5, 7, 8, 10, 12): return 31
    if m in (4, 6, 9, 11): return 30
    leap = (y % 4 == 0 and y % 100 != 0) or (y % 400 == 0)
    return 29 if leap else 28

def _add_days(date_str, delta):
    y = int(date_str[:4]); m = int(date_str[5:7]); d = int(date_str[8:10]) + delta
    while d < 1:
        m -= 1
        if m < 1:
            m = 12; y -= 1
        d += _days_in_month(y, m)
    while d > _days_in_month(y, m):
        d -= _days_in_month(y, m)
        m += 1
        if m > 12:
            m = 1; y += 1
    return _fmt_date(y, m, d)
```

### Start / end of week (Mon–Sun)

```python
def week_bounds(today_date):
    wd = _weekday(today_date)
    monday = _add_days(today_date, -wd)
    sunday = _add_days(monday, 6)
    return monday, sunday
```

### Normalise caller-supplied datetime to full RFC3339

`date_add` / `date_diff` reject inputs shorter than `YYYY-MM-DDTHH:MM:SSZ`. User input is often `YYYY-MM-DDTHH:MM`. Normalise on the way in:

```python
def normalise_iso(s):
    """Accept YYYY-MM-DDTHH:MM, YYYY-MM-DDTHH:MM:SS, or already-full RFC3339.
    Returns a string date_add/date_diff will accept, or None if unparseable."""
    if not isinstance(s, str) or len(s) < 16:
        return None
    if len(s) == 16:           # YYYY-MM-DDTHH:MM
        return s + ":00Z"
    if len(s) == 19:           # YYYY-MM-DDTHH:MM:SS
        return s + "Z"
    return s                   # already has Z or offset
```

A stricter version that actually validates the shape (used by the `timecards` example) is in `builder_examples("timecards")` — grab it there if you need input validation too.

### Extend `today()` to a full RFC3339

```python
# today() returns "YYYY-MM-DD". To pass it to date_add:
start_of_today_utc = today() + "T00:00:00Z"
```

## Smoke-testing patterns

### Parallel `preview_*` for Slack-sending functions

`dm_user`, `send_slack_message`, `post_to_channel` may fail with `user_not_found` or similar when you invoke the function via `run_script` — the admin harness's synthetic caller has no real Slack identity. Don't try to `try/except` around it (host errors are not catchable). Instead, expose a parallel `preview_*` function that returns the composed message body without the side effect:

```python
def weekly_briefing():
    summary = _compute_summary()
    dm_user(user_id=current_user()["id"], text=_format(summary))
    return {"ok": True, **summary}

def preview_weekly_briefing():
    summary = _compute_summary()
    return {"ok": True, "message": _format(summary), **summary}
```

Smoke-test with `preview_weekly_briefing` via `run_script`; leave `weekly_briefing` to fire from the scheduler or a real user invocation.

## Error handling

Two error classes:

| Error source | Catchable in Python? | What happens |
|---|---|---|
| Host call (`db_*`, `llm_*`, action, meta, Slack) | **No** — unwinds the interpreter | Script aborts; `status='error'` on the run |
| Python `raise ...` | **Yes** — `try/except` works | You control flow |

Consequence: you cannot retry a failed host call inside Python. Return early on conditions you expect (`if doc is None: return {"ok": False, ...}`) instead of letting a host call fail.

Mid-mutation aborts can be reversed with `rollback_script_run(run_id=..., confirm=true)` — that reverts the app_items mutations the run made. Side effects from actions (briefings, todos, DMs) are NOT rolled back; design so the mutations happen before any side effect, not after.

## Return values

Script function return values cross the WASM boundary as JSON. Supported: `dict`, `list`, `str`, `int`, `float`, `bool`, `None`. Tuples serialize to lists. Don't return objects with `__dict__`, generators, or host-object references.

Runtime-derived shapes like the one returned from `db_insert_one` (a dict with `_id`, `_created_at`, etc.) are plain dicts — safe to return or mutate.
