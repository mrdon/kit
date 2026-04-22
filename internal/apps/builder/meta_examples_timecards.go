// Package builder: meta_examples_timecards.go houses the `timecards`
// starter bundle. Pulled into its own file because the three scripts
// (utils / dal / main) together push the bodies well past what fits
// comfortably in meta_examples.go without blowing the 500-LOC cap.
//
// The bundle was produced end-to-end by a naive subagent driving the
// MCP meta-tools under the `builder-admin-guide` skill — it's a real
// shipped app, not a hand-crafted teaching aid. Patterns worth copying:
//   - Per-user scoping via current_user() (user_id + user_name on every row).
//   - Admin-only edit/delete with both visible_to_roles=["admin"] at the
//     registry and an in-script current_user()["is_admin"] guard.
//   - Lenient input parsing + strict normalisation before storage so
//     date_add / date_diff and lexical sort both work.
//   - A `preview_weekly_briefing` companion to the scheduled DM function
//     so run_script smoke tests don't fail on `dm_user` side effects.
//   - Weekday / "start of week" math done in pure Python (Zeller's)
//     because Monty has no datetime module and $gte / $lte won't work
//     on string fields in v0.1.
package builder

// timecardsExample returns the canonical per-user time-tracking starter.
// Discovered via subagent dogfood: a plausible admin prompt produced
// this shape, so it's the most honest benchmark of the substrate's UX.
func timecardsExample() exampleDefinition {
	memberAndAdmin := []string{"admin", "member"}
	adminOnly := []string{"admin"}

	return exampleDefinition{
		ID:          "timecards",
		Title:       "Timecards — per-user shifts, admin edits, weekly DM",
		Description: "End-to-end per-user app: record shifts (start+end OR start+hours), list your own, admin edit/delete, weekly standup DM on Mondays. Demonstrates current_user() scoping, admin-only mutations, date normalisation, and the preview_* pattern for safe smoke-testing of Slack-side effects.",
		Apps: []exampleAppSpec{{
			AppName: "timecards",
			Scripts: []exampleScriptSpec{
				{Name: "utils", Body: timecardsUtilsBody},
				{Name: "dal", Body: timecardsDalBody},
				{Name: "main", Body: timecardsMainBody},
			},
			Expose: []exampleExposeSpec{
				{Script: "main", Fn: "record_shift", ToolName: "timecards_record_shift", VisibleToRoles: memberAndAdmin},
				{Script: "main", Fn: "list_my_shifts", ToolName: "timecards_list_my_shifts", VisibleToRoles: memberAndAdmin},
				{Script: "main", Fn: "my_week_hours", ToolName: "timecards_my_week_hours", VisibleToRoles: memberAndAdmin},
				{Script: "main", Fn: "list_shifts", ToolName: "timecards_list_shifts", VisibleToRoles: adminOnly},
				{Script: "main", Fn: "admin_edit_shift", ToolName: "timecards_admin_edit_shift", VisibleToRoles: adminOnly},
				{Script: "main", Fn: "admin_delete_shift", ToolName: "timecards_admin_delete_shift", VisibleToRoles: adminOnly},
				{Script: "main", Fn: "preview_weekly_briefing", ToolName: "timecards_preview_weekly_briefing", VisibleToRoles: adminOnly},
			},
			Schedule: []exampleScheduleSpec{
				{Script: "main", Fn: "weekly_standup_briefing", Cron: "0 9 * * 1"},
			},
		}},
	}
}

// timecardsUtilsBody — pure helpers: date parsing/normalisation, hours
// math, weekday/week-window computation. No DB, no LLM, no actions.
//
//nolint:dupword // Python tuple returns legitimately repeat None
const timecardsUtilsBody = `
# Pure helpers for the timecards app.
# Monty/WASM quirks addressed here:
#   - "%04d" % n formatting is NOT supported; use _zp manual zero-pad.
#   - date_add/date_diff require FULL RFC3339 (seconds + 'Z' or offset),
#     so parse_iso_datetime normalises to "YYYY-MM-DDTHH:MM:SSZ".

def _zp(n, width):
    s = str(int(n))
    while len(s) < width:
        s = "0" + s
    return s

def _fmt_date(y, m, d):
    return _zp(y, 4) + "-" + _zp(m, 2) + "-" + _zp(d, 2)

def _is_digit_str(s, n):
    if len(s) != n:
        return False
    for ch in s:
        if ch < "0" or ch > "9":
            return False
    return True

def parse_iso_datetime(s):
    """Return (ok, normalised_rfc3339, error). Accepts HH:MM or HH:MM:SS."""
    if not isinstance(s, str) or len(s) < 16:
        return False, None, "datetime must be YYYY-MM-DDTHH:MM at minimum"
    date_part = s[:10]
    if date_part[4] != "-" or date_part[7] != "-":
        return False, None, "date portion must be YYYY-MM-DD"
    if not (_is_digit_str(date_part[:4], 4) and _is_digit_str(date_part[5:7], 2) and _is_digit_str(date_part[8:10], 2)):
        return False, None, "date portion must be YYYY-MM-DD"
    if s[10] not in ("T", " "):
        return False, None, "expected 'T' between date and time"
    time_part = s[11:16]
    if time_part[2] != ":" or not _is_digit_str(time_part[:2], 2) or not _is_digit_str(time_part[3:5], 2):
        return False, None, "time portion must be HH:MM"
    if int(time_part[:2]) > 23 or int(time_part[3:5]) > 59:
        return False, None, "hour/minute out of range"
    rest = s[16:]
    seconds = "00"
    if len(rest) >= 3 and rest[0] == ":" and _is_digit_str(rest[1:3], 2):
        seconds = rest[1:3]
        if int(seconds) > 59:
            return False, None, "seconds out of range"
        rest = rest[3:]
    if rest == "" or rest == "Z":
        suffix = "Z"
    elif rest[0] in ("+", "-") and len(rest) == 6 and rest[3] == ":" and _is_digit_str(rest[1:3], 2) and _is_digit_str(rest[4:6], 2):
        suffix = rest
    else:
        return False, None, "unrecognised timezone suffix: " + rest
    return True, date_part + "T" + time_part + ":" + seconds + suffix, None

def parse_date(s):
    if not isinstance(s, str) or len(s) != 10 or s[4] != "-" or s[7] != "-":
        return False, None, "date must be YYYY-MM-DD"
    if not (_is_digit_str(s[:4], 4) and _is_digit_str(s[5:7], 2) and _is_digit_str(s[8:10], 2)):
        return False, None, "date must be YYYY-MM-DD"
    return True, s, None

def round_hours(h):
    return int(h * 100 + 0.5) / 100.0

def _days_in_month(year, month):
    if month in (1, 3, 5, 7, 8, 10, 12):
        return 31
    if month in (4, 6, 9, 11):
        return 30
    leap = (year % 4 == 0 and year % 100 != 0) or (year % 400 == 0)
    return 29 if leap else 28

def _add_days_to_date(date_str, delta):
    y = int(date_str[:4])
    m = int(date_str[5:7])
    d = int(date_str[8:10]) + delta
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

def _weekday(date_str):
    """0=Mon .. 6=Sun via Zeller's congruence."""
    y = int(date_str[:4]); m = int(date_str[5:7]); d = int(date_str[8:10])
    if m < 3:
        m += 12; y -= 1
    k = y % 100; j = y // 100
    h = (d + (13 * (m + 1)) // 5 + k + k // 4 + j // 4 + 5 * j) % 7
    return (h + 5) % 7

def week_bounds(today_date):
    wd = _weekday(today_date)
    monday = _add_days_to_date(today_date, -wd)
    sunday = _add_days_to_date(monday, 6)
    return monday, sunday

def day_bounds_iso(date_str):
    """Return ('YYYY-MM-DDT00:00:00Z', 'YYYY-MM-DDT23:59:59Z')."""
    return date_str + "T00:00:00Z", date_str + "T23:59:59Z"

def validate_new_shift(doc):
    """Validate inputs. Exactly one of end_at/hours required alongside start_at."""
    errors = []
    description = doc.get("description", "")
    if not isinstance(description, str) or len(description.strip()) == 0:
        errors.append("description required")
    elif len(description) > 500:
        errors.append("description too long (max 500 chars)")

    start = doc.get("start_at")
    end = doc.get("end_at")
    hours = doc.get("hours")
    if not start:
        errors.append("start_at required")
        return errors, None, None, None
    ok, norm_start, err = parse_iso_datetime(start)
    if not ok:
        errors.append("start_at: " + (err or "invalid"))
        return errors, None, None, None
    if end is None and hours is None:
        errors.append("must provide either end_at or hours")
        return errors, norm_start, None, None
    if end is not None and hours is not None:
        errors.append("provide either end_at or hours, not both")
        return errors, norm_start, None, None

    norm_end = None
    computed_hours = None
    if end is not None:
        ok_e, norm_end, err_e = parse_iso_datetime(end)
        if not ok_e:
            errors.append("end_at: " + (err_e or "invalid"))
            return errors, norm_start, None, None
        if norm_end <= norm_start:
            errors.append("end_at must be after start_at")
            return errors, norm_start, norm_end, None
        hs = date_diff(norm_end, norm_start) / 3600.0
        computed_hours = round_hours(hs)
        if computed_hours > 24:
            errors.append("shift longer than 24 hours — split into multiple shifts")
    else:
        if not isinstance(hours, (int, float)) or hours <= 0 or hours > 24:
            errors.append("hours must be a number in (0, 24]")
            return errors, norm_start, None, None
        computed_hours = round_hours(float(hours))
        norm_end = date_add(norm_start, seconds=int(hours * 3600 + 0.5))

    return errors, norm_start, norm_end, computed_hours
`

// timecardsDalBody — data access over the `shifts` collection. Note the
// find_in_range_* helpers: $gte/$lte won't accept strings in v0.1, so we
// fetch by user_id (cheap equality) and filter in Python.
const timecardsDalBody = `
# Data access for timecards. Every doc carries user_id + user_name so
# reports never re-resolve identity.

COLLECTION = "shifts"

def insert_shift(doc):
    return db_insert_one(COLLECTION, doc)

def get_shift(shift_id):
    return db_find_one(COLLECTION, {"_id": shift_id})

def find_by_user(user_id, limit=50):
    return db_find(COLLECTION, {"user_id": user_id}, limit=limit,
                   sort=[("start_at", -1)])

def find_all(limit=200):
    return db_find(COLLECTION, {}, limit=limit, sort=[("start_at", -1)])

def find_in_range_for_user(user_id, start_iso, end_iso, limit=2000):
    """$gte/$lte compare lexically on strings, so RFC3339 range works."""
    return db_find(COLLECTION,
                   {"user_id": user_id,
                    "start_at": {"$gte": start_iso, "$lte": end_iso}},
                   limit=limit, sort=[("start_at", 1)])

def update_shift(shift_id, update):
    return db_update_one(COLLECTION, {"_id": shift_id}, update)

def delete_shift(shift_id):
    return db_delete_one(COLLECTION, {"_id": shift_id})
`

// timecardsMainBody — business logic. Notes:
//   - record_shift derives user identity from current_user(), never args.
//   - admin_edit_shift / admin_delete_shift enforce admin at both layers.
//   - weekly_standup_briefing DMs the installer (not a role-scoped
//     briefing, since v0.1 briefings scope by role not individual).
//   - preview_weekly_briefing returns the same body without the DM so
//     run_script smoke tests don't fail on a synthetic caller's Slack id.
const timecardsMainBody = `
# Timecards business logic.
#
# Doc shape in "shifts":
#   user_id / user_name      denormalised caller identity
#   description              str (1..500)
#   start_at / end_at        RFC3339 "YYYY-MM-DDTHH:MM:SSZ"
#   hours                    float (2dp)
#   input_mode               "start_end" | "start_hours"
#   closed                   bool (always True at insert in v0.1)

def _require_admin():
    me = current_user()
    if not me["is_admin"]:
        raise PermissionError("admin only")
    return me

def _fmt_hours(h):
    s = str(round(h, 2))
    if "." in s:
        whole, frac = s.split(".")
        if len(frac) == 1:
            frac = frac + "0"
        return whole + "." + frac[:2]
    return s + ".00"

# ----- user-facing ---------------------------------------------------------

def record_shift(description, start_at, end_at=None, hours=None):
    """Record a completed shift for the calling user."""
    me = current_user()
    doc = {"description": description, "start_at": start_at}
    if end_at is not None: doc["end_at"] = end_at
    if hours is not None: doc["hours"] = hours

    errors, norm_start, norm_end, computed_hours = shared(
        "utils", "validate_new_shift", doc=doc)
    if errors:
        return {"ok": False, "errors": errors}

    input_mode = "start_end" if end_at is not None else "start_hours"
    row = shared("dal", "insert_shift", doc={
        "user_id": me["id"],
        "user_name": me["display_name"],
        "description": description.strip(),
        "start_at": norm_start,
        "end_at": norm_end,
        "hours": computed_hours,
        "input_mode": input_mode,
        "closed": True,
    })
    log("info", "shift recorded", shift_id=row["_id"],
        user_id=me["id"], hours=computed_hours)
    return {"ok": True, "shift_id": row["_id"], "hours": computed_hours,
            "start_at": norm_start, "end_at": norm_end}

def list_my_shifts(limit=20):
    me = current_user()
    rows = shared("dal", "find_by_user", user_id=me["id"], limit=limit)
    return {"count": len(rows), "shifts": [{
        "shift_id": r["_id"], "description": r.get("description"),
        "start_at": r.get("start_at"), "end_at": r.get("end_at"),
        "hours": r.get("hours"),
    } for r in rows]}

def list_shifts(limit=50, user_id=None):
    """Admin-only tool via registry; non-admins see only their own."""
    me = current_user()
    if me["is_admin"] and user_id:
        rows = shared("dal", "find_by_user", user_id=user_id, limit=limit)
    elif me["is_admin"]:
        rows = shared("dal", "find_all", limit=limit)
    else:
        rows = shared("dal", "find_by_user", user_id=me["id"], limit=limit)
    return {"count": len(rows), "shifts": [{
        "shift_id": r["_id"], "user_id": r.get("user_id"),
        "user_name": r.get("user_name"), "description": r.get("description"),
        "start_at": r.get("start_at"), "end_at": r.get("end_at"),
        "hours": r.get("hours"),
    } for r in rows]}

# ----- admin-only ----------------------------------------------------------

def admin_edit_shift(shift_id, description=None, start_at=None, end_at=None, hours=None):
    _require_admin()
    existing = shared("dal", "get_shift", shift_id=shift_id)
    if existing is None:
        return {"ok": False, "error": "shift not found"}
    if hours is not None and end_at is not None:
        return {"ok": False, "errors": ["provide either end_at or hours, not both"]}

    merged = {
        "description": description if description is not None else existing.get("description"),
        "start_at": start_at if start_at is not None else existing.get("start_at"),
    }
    if hours is not None:
        merged["hours"] = hours
    elif end_at is not None:
        merged["end_at"] = end_at
    else:
        merged["end_at"] = existing.get("end_at")

    errors, norm_start, norm_end, computed_hours = shared(
        "utils", "validate_new_shift", doc=merged)
    if errors:
        return {"ok": False, "errors": errors}

    new_fields = {
        "description": merged["description"].strip(),
        "start_at": norm_start, "end_at": norm_end, "hours": computed_hours,
    }
    shared("dal", "update_shift", shift_id=shift_id,
           update={"$set": new_fields})
    log("info", "admin edited shift", shift_id=shift_id,
        admin_id=current_user()["id"])
    return {"ok": True, "shift_id": shift_id, "fields": new_fields}

def admin_delete_shift(shift_id):
    _require_admin()
    existing = shared("dal", "get_shift", shift_id=shift_id)
    if existing is None:
        return {"ok": False, "error": "shift not found"}
    shared("dal", "delete_shift", shift_id=shift_id)
    log("info", "admin deleted shift", shift_id=shift_id,
        admin_id=current_user()["id"])
    return {"ok": True, "shift_id": shift_id}

# ----- reporting / briefing -----------------------------------------------

def my_week_hours(week_of=None):
    me = current_user()
    anchor = week_of or today()
    ok, anchor, err = shared("utils", "parse_date", s=anchor)
    if not ok:
        return {"ok": False, "error": err}
    monday, sunday = shared("utils", "week_bounds", today_date=anchor)
    start_iso, _ = shared("utils", "day_bounds_iso", date_str=monday)
    _, end_iso = shared("utils", "day_bounds_iso", date_str=sunday)
    rows = shared("dal", "find_in_range_for_user",
                  user_id=me["id"], start_iso=start_iso, end_iso=end_iso,
                  limit=500)
    total = 0.0
    by_day = {}
    for r in rows:
        h = r.get("hours") or 0
        total += h
        day = (r.get("start_at") or "")[:10]
        by_day[day] = by_day.get(day, 0) + h
    days = []
    d = monday
    for _i in range(7):
        days.append({"date": d, "hours": round(by_day.get(d, 0.0), 2)})
        d = shared("utils", "_add_days_to_date", date_str=d, delta=1)
    return {
        "ok": True, "user_id": me["id"], "user_name": me["display_name"],
        "week_of_monday": monday, "week_of_sunday": sunday,
        "total_hours": round(total, 2), "shift_count": len(rows),
        "days": days,
    }

def _format_week_message(summary):
    lines = []
    lines.append("*Timecards weekly standup for " +
                 (summary.get("user_name") or "you") + "*")
    lines.append("Week of " + summary["week_of_monday"] +
                 " to " + summary["week_of_sunday"])
    lines.append("Total: *" + _fmt_hours(summary["total_hours"]) +
                 " hours* across " + str(summary["shift_count"]) + " shifts")
    lines.append("")
    labels = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"]
    for i, day in enumerate(summary["days"]):
        lines.append("• " + labels[i] + " " + day["date"] + ": " +
                     _fmt_hours(day["hours"]) + " h")
    return "\n".join(lines)

def weekly_standup_briefing():
    """Scheduled Monday 9am — DM the installer their weekly hours."""
    me = current_user()
    summary = my_week_hours()
    if not summary.get("ok"):
        log("error", "weekly briefing failed", error=summary.get("error"))
        return summary
    dm_user(user_id=me["id"], text=_format_week_message(summary))
    log("info", "weekly DM sent", total=summary["total_hours"],
        shift_count=summary["shift_count"])
    return {"ok": True, "total_hours": summary["total_hours"],
            "shift_count": summary["shift_count"]}

def preview_weekly_briefing():
    """Same body weekly_standup_briefing would send, without the DM.

    Use this for smoke tests — run_script against weekly_standup_briefing
    may fail on the Slack call when invoked from the admin harness.
    """
    summary = my_week_hours()
    if not summary.get("ok"):
        return summary
    return {"ok": True, "message": _format_week_message(summary),
            "total_hours": summary["total_hours"],
            "shift_count": summary["shift_count"]}
`
