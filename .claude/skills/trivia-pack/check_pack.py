"""Check a trivia pack CSV against the trivia-pack skill's rules.

    python3 .claude/skills/trivia-pack/check_pack.py dist/trivia-<slug>/rated.csv

Errors are things the import or the game would get wrong (a missing
column, a decimal answer, a difficulty off the 1-10 scale). Warnings are
rule breaks worth a second look (a long answer, a threshold phrasing).
Exits 1 if there are any errors.
"""

import csv
import re
import sys

REQUIRED = ("question", "topics", "answer")
MAX_PROMPT = 500  # internal/apps/trivia/csv.go maxPromptLength
THRESHOLD = re.compile(r"\bmore than how many\b|\bat least how many\b|\bover how many\b", re.I)
HELPER = re.compile(r"\bso [\d,]+ would be\b|\bround(ed)? down\b", re.I)


def check(path):
    errors, warnings = [], []
    with open(path, newline="", encoding="utf-8-sig") as f:
        rows = list(csv.reader(f))
    if not rows:
        return ["the file is empty"], []
    header = [h.strip().lower() for h in rows[0]]
    for col in REQUIRED:
        if col not in header:
            errors.append(f"missing column {col!r}")
    if errors:
        return errors, warnings
    idx = {name: header.index(name) for name in header}

    seen, years, topics_seen = set(), 0, {}
    for line, rec in enumerate(rows[1:], start=2):
        if not any(c.strip() for c in rec):
            continue
        if len(rec) != len(header):
            errors.append(f"line {line}: {len(rec)} fields, header has {len(header)} (unquoted comma?)")
            continue
        q = rec[idx["question"]].strip()
        a = rec[idx["answer"]].strip()
        where = f"line {line} ({q[:50]}...)"

        if not q:
            errors.append(f"line {line}: empty question")
        if len(q) > MAX_PROMPT:
            errors.append(f"{where}: question is {len(q)} characters, limit {MAX_PROMPT}")
        key = re.sub(r"[^a-z0-9]+", " ", q.lower()).strip()
        if key in seen:
            errors.append(f"{where}: duplicate question")
        seen.add(key)

        if not re.fullmatch(r"\d+", a):
            errors.append(f"{where}: answer {a!r} must be a whole number with no commas, units or decimals")
        else:
            n = int(a)
            if n < 5:
                errors.append(f"{where}: answer {n} is under 5")
            if len(a) > 5:
                errors.append(f"{where}: answer {a} has {len(a)} digits; ask in thousands or millions")
            elif len(a) == 5:
                warnings.append(f"{where}: answer {a} has 5 digits; could a bigger unit keep it to 4?")
            if 1000 <= n <= 2100 and re.search(r"\b(what|which) year\b|\bin what year\b", q, re.I):
                years += 1

        topics = [t.strip() for t in rec[idx["topics"]].split(";") if t.strip()]
        if not topics:
            errors.append(f"{where}: no topics")
        for t in topics:
            topics_seen[t] = topics_seen.get(t, 0) + 1

        if "difficulty" in idx:
            d = rec[idx["difficulty"]].strip()
            if d and not (d.isdigit() and 1 <= int(d) <= 10):
                errors.append(f"{where}: difficulty {d!r} must be a whole number 1-10")

        if THRESHOLD.search(q):
            warnings.append(f"{where}: threshold phrasing; a true answer above it would go over")
        if HELPER.search(q):
            warnings.append(f"{where}: helper text like 'round down' or 'so X would be Y'; 'in thousands' is enough")

    if years > 2:
        warnings.append(f"{years} year-answer questions; the skill allows two per pack")
    lonely = [t for t, c in topics_seen.items() if c < 3]
    if lonely:
        warnings.append(f"topics on fewer than 3 questions can't fill a column: {', '.join(sorted(lonely))}")
    return errors, warnings


def main():
    if len(sys.argv) != 2:
        print(__doc__.strip())
        sys.exit(2)
    errors, warnings = check(sys.argv[1])
    for e in errors:
        print("ERROR  " + e)
    for w in warnings:
        print("warn   " + w)
    if not errors and not warnings:
        print("ok")
    sys.exit(1 if errors else 0)


if __name__ == "__main__":
    main()
