---
name: trivia-pack
description: Research, write, rate and upload a new question pack on any topic (football, pizza, local companies, a holiday...) for Gravity's trivia night (Wits & Wagers style, numeric answers). Covers the question-writing rules (fun, surprising, bar-simple, answer at least 5, real cited numbers only), research subagents, the approval list, 1-10 difficulty rating calibrated against the bank, and uploading it through Kit's trivia MCP tools. Use when asked for "more trivia questions", "a pack on X", "questions for <event>", "trivia on <topic>", or similar.
---

# Make a trivia pack

Gravity Brewing (Louisville, CO) runs a Wits & Wagers style trivia night.
Every answer is a single number. Teams write a guess, and the **closest guess
without going over** wins. The game shows the answer as a bare number, with
no commentary, so the question text is all the context anyone gets.

A pack is a named dataset in the workspace's question bank. Everything this
skill needs from Kit goes through the workspace's Kit MCP server (for
Gravity, the `kit-gravity` tools):

- `trivia_list_questions`: lists the packs, and with `questions: true`, every question with its difficulty, answer and topics.
- `trivia_import_pack`: uploads a CSV as a pack. It is the same import as the console's "Upload a set".

Never read or write the database directly.

## The rules for a good question

The user cares about these most. Tell every research subagent all of them.

**Fun first.**
- Pick facts a regular person enjoys hearing: "no way" or "huh, cool" when the answer is read out.
- Cut dry stats and specs: heights, weights, counts and dates nobody cares about.
- Cut corporate figures: headcount, revenue, contracts.
- Keep a stat only when it's surprising. Examples: a spaceplane's insides stay at 350°F while its heat shield hits 3,000°F; a famous record that was set by a tiny margin; a then-vs-now contrast.

**Mostly not guessable.**
- Most answers should be ones teams can't simply know.
- One or two easy questions per pack is fine. Mark them.

**Clear and simple. This is a bar.**
- One or two short sentences, plain words, no jargon.
- Don't build a setup with several clauses.
- A question should read cleanly aloud.

**Context goes in the question.** Teams can't judge an unfamiliar unit or scale without help. "NASA requires at least 60.8 psi. At what psi did it burst?" works; "At what psi did it burst?" doesn't. Write anchors like that into the question. Never rely on a reveal line, because there isn't one.

**The answer must be at least 5.** Small answers (3 stories, 2 Super Bowl wins) bunch every team on the same guess, so the round decides nothing.

**Use the real number.**
- Prefer the exact sourced figure (7,170 lb) over a rounded one (7,000).
- The answer doesn't need to be guessable. It needs to be right.

**Make scoring unambiguous.**
- State units and rounding in the question.
- Never ask "more than how many" or anything else with a threshold answer. A team that writes the true, higher figure goes over. Say "round down to the nearest hundred" instead.
- Watch decimals: 18.5 is fine, but think about whether whole-number guessers get a fair shot.

**Make sure the premise is true today.**
- Check the current status of anything that can change: records, rosters, seasons, launches, prices, renames, ownership.
- Pin a figure that will change to a date ("As of June 2025, ...").

**Dated questions are great; aim for some in every pack.** "As of 2018, how many..." or "In 2004, how many..." are harder to look up on a phone, because a quick search shows today's figure. They also tell the room the answer is exact and sourced. A then-vs-now contrast can make a dull stat fun. Never cut a question for being old. Ask research agents to look for dated figures on purpose.

**No downers.** Cut disasters, deaths, injuries, failures, war and anything grim. Be extra careful with local tragedies (the Marshall Fire hit Louisville).

**Don't overlap.** Skip anything close to another question in the pack or already in the bank.

**For local topics, name the local angle** ("Louisville-based Sierra Space"), and verify it's really local first. For example, Blue Origin's nearest office is in Highlands Ranch, not Boulder County.

## 1. Scope it

- Agree the topic with the user and split it into 3–5 subtopics that each have fun numbers. For football, that might be records, the Super Bowl, the Broncos and stadiums. For pizza, history, records, eating contests and the business of pizza.
- Ask about any local angle or audience the user has in mind: a team, a company whose people come in, an event night.

Call `trivia_list_questions` with `questions: true` and save the output to
`dist/trivia-<theme>/bank.txt`. You need it to check for overlap and to
calibrate difficulty.

## 2. Research in parallel

Spawn one general-purpose agent per subtopic or group of subtopics, all in one message. Brief each one with:

- **The game and audience:** the format, the rules above, and what's already in the bank to skip.
- **Leads to chase:** a few ideas per subtopic, framed as "verify, don't assume". Ask for some dated figures ("as of 2018") alongside current ones.
- **Hard sourcing rules:**
  - Every number comes from a page the agent fetched and read this session.
  - Nothing from memory or from search snippets alone.
  - It returns the exact URL and a short verbatim quote containing the number.
  - When sources disagree, the question is dropped or the disagreement is reported.
- **Today's date:** have it check the latest news so no premise is stale.
- **What to return:** question, answer, URL, quote, and why it's surprising, plus a list of leads it rejected.

Ask for about 6–10 candidates per agent. Expect to keep about half.

## 3. Curate, then list here first

Don't write anything until the user approves the list.

- Cut questions that break the rules above, that are close to each other or to the bank, or whose sources are weak.
- Rewrite the survivors for clarity and to put the needed context in.
- Show the user a numbered list grouped by subtopic: question, **answer**, and a source link.
- Follow it with a short list of what you cut and why, and anything borderline.
- Expect one or two rounds of edits. If a group the user cares about is thin, send another agent to dig there specifically.

## 4. Rate difficulty

The import takes an optional `difficulty` column, a whole number from 1 to 10. Every pack should have one, because the board uses it to place questions in rows.

1. Use the bank listing from step 1 as the calibration file. Each question line starts with its difficulty in brackets.
2. Write the approved questions to an unrated CSV.
3. Spawn one agent to rate them. Give it:
   - the scale: 1 = everybody knows it, 10 = jelly beans in the jar
   - the bank's spread, which centers on 5–7, and each pack's average from the listing's header lines
   - an instruction to judge only, not change wording or research
   - a request for `row,difficulty,reason` back, plus any wording that gives the answer away or would confuse a bar crowd
4. Apply its wording flags if they fix a real problem, such as a threshold answer or a clunky read-aloud.

## 5. Write the CSV and upload

Put the deliverable at `dist/trivia-<theme>.csv`, with working files in `dist/trivia-<theme>/`. `dist/` is gitignored.

The columns are `question,topics,answer,difficulty,source`:
- **Topics** are `;`-separated. Reuse broad topics already in the bank where they fit (`Space`, `Colorado`, `Sport`), plus one for the pack's theme (`Local Space`, `Pizza`), so a game can run a column of it.
- **Answers** are plain numbers with no commas or units. Write the CSV with Python's `csv` module, so commas inside questions get quoted.

Check that no pack already uses the name (the listing from step 1 has
every pack). Then upload the CSV with `trivia_import_pack`:

- **`name`:** the pack's name, e.g. "Pizza".
- **`csv`:** the whole file's text.
- **`notes`:** "Researched and source-checked <date>; sources in the CSV source column."

The tool reads `difficulty`, ignores `source`, and reports what it did: the
count, the average difficulty, skipped duplicates, rows it couldn't parse,
and the topics. Fix any bad rows and upload again with `replace: true`.

Replacing is refused for a pack any game has already asked from, for a
starter pack, and for a pack on the board of a live game. Those cases go
to the user in the console.

## 6. Report

Tell the user:
- the pack name, question count and average difficulty
- any wording you changed after approval, and why
- the easiest and hardest questions
- the CSV path, and the import tool's summary

The pack isn't attached to any game. The host picks it in the console.
