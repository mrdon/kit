---
name: trivia-pack
description: Research, write, rate and upload a new question pack on any topic (football, pizza, local companies, a holiday...) for Gravity's trivia night (Wits & Wagers style, numeric answers). Covers the question-writing rules (fun, surprising, bar-simple, short whole-number answers of at least 5, real cited numbers only), research subagents, difficulty rating, the approval list, and uploading through Kit's trivia MCP tools. Use when asked for "more trivia questions", "a pack on X", "questions for <event>", "trivia on <topic>", or similar.
---

# Make a trivia pack

Gravity Brewing (Louisville, CO) runs a Wits & Wagers style trivia night.
Every answer is a single number. Teams type a guess on their phones, and the
**closest guess without going over** wins. The game shows the answer as a
bare number, with no commentary, so the question text is all the context
anyone gets.

A pack is a named dataset in the workspace's question bank. Everything this
skill needs from Kit goes through the workspace's Kit MCP server
(`kit-gravity` for Gravity):

- `trivia_list_questions`: lists the packs with their counts and average difficulty. `search` (comma-separated terms) lists only questions whose text or topics match. `pack` narrows to one pack, and `questions: true` lists that pack's questions.
- `trivia_import_pack`: uploads a CSV as a pack, the same import as the console's "Upload a set".

The Kit tools are deferred. Load them with ToolSearch before calling them
(`select:mcp__kit-gravity__trivia_list_questions,mcp__kit-gravity__trivia_import_pack`).
The server name follows the workspace, so search for `trivia_list_questions`
if unsure. Never read or write the database directly, and never call
`questions: true` without `pack`: the whole bank is too big for one result.

Files next to this one:
- `brief-template.md`: the research brief. Fill it in for each research agent.
- `check_pack.py`: checks a pack CSV against the rules below. Run it on every CSV you write.

## The rules for a good question

The user cares about these most. The brief template carries them to every research agent.

**Fun first.**
- Pick facts a regular person enjoys hearing: "no way" or "huh, cool" when the answer is read out.
- Cut dry stats and specs: heights, weights, counts and dates nobody cares about.
- Cut corporate figures: headcount, revenue, contracts.
- Keep a stat only when it's surprising. Examples: a spaceplane's insides stay at 350°F while its heat shield hits 3,000°F; a famous record set by a tiny margin; a then-vs-now contrast.

**Mostly not guessable.**
- Most answers should be ones teams can't simply know.
- One or two easy questions per pack is fine. Mark them.

**Clear and simple. This is a bar.**
- One or two short sentences, plain words, no jargon.
- Don't build a setup with several clauses.
- A question should read cleanly aloud.

**Explain anything obscure.** If most people in a Colorado bar wouldn't know a place, person, custom or thing, say what it is in a few words. "Ireland's 5,000-year-old Newgrange tomb" works; "Newgrange" alone leaves the room lost. The same goes for non-English words (diya, sufganiyot) and niche events. Keep it to a short phrase.

**Context goes in the question.** Teams can't judge an unfamiliar unit or scale without help. "NASA requires at least 60.8 psi. At what psi did it burst?" works; "At what psi did it burst?" doesn't. Never rely on a reveal line, because there isn't one.

**Answers are short whole numbers.**
- **At least 5.** Small answers (3 stories, 2 Super Bowl wins) bunch every team on the same guess.
- **No decimals.** Ask in a unit that makes the answer whole: "in dollars", "what percent", "in minutes".
- **No more than 4 digits where possible, 5 at most.** Teams type their guess on a phone. For big numbers, ask in a natural unit so the answer stays short: "In millions, how many trips...?" (9,410), "In thousands of pounds...?". Pick the unit that reads naturally for the subject.
- **Drop the remainder.** The answer is the sourced figure in that unit with the remainder dropped (2,617,215 lamps in thousands is 2617). Don't write "round down" or "so 171,000 would be 171" into the question; "in thousands" is enough.
- **Year answers** are fine, at most two per pack.

**Use the real number.** Prefer the exact sourced figure over a rounded one the source didn't give. The answer doesn't need to be guessable. It needs to be right.

**Make scoring unambiguous.**
- State the unit in the question.
- Never ask "more than how many" or anything else with a threshold answer. A team that writes the true, higher figure goes over.

**Make sure the premise is true today.**
- Check the current status of anything that can change: records, rosters, seasons, prices, renames, ownership.
- Pin a figure that will change to a date ("As of June 2025, ...").
- Records broken often (Guinness records, yearly festival counts) always get a year ("In 2025, ..."). Never say "the current record" or "today's".

**Dated questions are great; aim for some in every pack.** "As of 2018, how many..." or "In 2004, how many..." are harder to look up on a phone, because a quick search shows today's figure. They also tell the room the answer is exact and sourced. A then-vs-now contrast can make a dull stat fun. Never cut a question for being old.

**Sources.**
- Prefer official and primary sources: the organization itself, Guinness, government, NASA, major news outlets.
- Wikipedia and fan or entertainment sites are acceptable when nothing better turns up. Flag them.
- Cut a figure when sources disagree, or when it is itself a loose estimate.
- If the page supports only part of a claim, reword to what it literally says ("first song in space" becomes "played in space"), or drop it. A search snippet alone never counts.

**No downers.** Cut disasters, deaths, injuries, failures, war and anything grim. Be extra careful with local tragedies (the Marshall Fire hit Louisville).

**Don't overlap.** Skip anything close to another question in the pack or already in the bank.

**For local topics, name the local angle** ("Louisville-based Sierra Space"), and verify it's really local first. Blue Origin's nearest office is in Highlands Ranch, not Boulder County.

## 1. Scope it

Agree these with the user before researching:

- **The topic and subtopics.** Split the topic into subtopics that each have fun numbers. For football: records, the Super Bowl, the Broncos and stadiums. For pizza: history, records, eating contests and the business of pizza.
- **The split.** For a mixed theme, settle roughly how many questions each subtopic gets.
- **The target count.** Packs are usually 15–25 questions.
- **The pack name.** Call `trivia_list_questions` (no arguments) and make sure no pack already has it. The slug for file names is the name, lowercased and hyphenated: "Winter Holidays" becomes `winter-holidays`.
- **Any local angle or audience:** a team, a company whose people come in, an event night.

Work files go in `dist/trivia-<slug>/` (gitignored). Write them freely; only
the upload waits for approval.

**Find the overlap.** Call `trivia_list_questions` with `search` set to the
topic, each subtopic and a few specific keywords, comma-separated (for
winter holidays: `christmas, santa, hanukkah, menorah, diwali, kwanzaa, new
year, solstice`). Avoid broad words (tree, snow, gift) that match unrelated
questions. Save the result to `dist/trivia-<slug>/bank-overlap.txt`.

## 2. Research in parallel

Group the subtopics into 2–4 research agents. Group small subtopics by
theme: Hanukkah, Diwali and Kwanzaa can share a "festivals of light"
agent. Spawn the agents together, in one message.

For each agent, fill in `brief-template.md` and save the filled copy to
`dist/trivia-<slug>/brief-<group>.md`. The template carries the rules, the
sourcing rules and the return format. Fill in:

- **The subtopics** and how many questions the pack wants from them.
- **The candidate count:** about twice that number, spares included. For a 10-question pack, the agents' counts should add up to about 20.
- **The bank overlap:** the hits from `bank-overlap.txt` on its subtopics.
- **Leads:** a few hunches per subtopic. Write them as questions to check ("Is there a Guinness record for X?"), never as facts to confirm.

## 3. Curate and rate

- Cut questions that break the rules above, that are close to each other or to the bank, or whose sources are weak.
- Rewrite the survivors for clarity, to explain anything obscure, and to put the needed context in.
- If a subtopic ended up with no question, send one more agent at it before going on, or flag the gap in the list.

Write the survivors to `dist/trivia-<slug>/unrated.csv` with the columns
`question,topics,answer,source`. Topics are `;`-separated, and each one can
become a board column, so use few, broad ones:
- the pack's theme topic on every question (`Winter Holidays`, `Pizza`),
- plus a broad topic already in the bank where it fits (`Space`, `Colorado`, `Sport`).
- Don't add a topic per subtopic (`Hanukkah`, `Diwali`); a column needs several questions to fill.

Run `python3 .claude/skills/trivia-pack/check_pack.py dist/trivia-<slug>/unrated.csv` and fix what it reports.

**Rate difficulty.** Spawn one agent to rate the questions 1–10:
1 = everybody knows it, 10 = jelly beans in the jar. The board uses the
rating to place questions in rows. Tell it:
- to judge only, not research or rewrite,
- to return `row,difficulty,reason`,
- to flag any wording that gives the answer away, is hard to read aloud, or would confuse a bar crowd.

Apply its wording flags if they fix a real problem. Re-rate a question only
if its meaning or numbers changed, not for a clarity fix.

Write the final wording and ratings to `dist/trivia-<slug>/rated.csv`
(`question,topics,answer,difficulty,source`), and run `check_pack.py` on it.

## 4. List here for approval

Don't upload until the user approves. Save the list to
`dist/trivia-<slug>/candidates.md` too, so each round of edits has
something to compare against. Use this shape:

```
**<Pack name>**: N questions, average difficulty X.

## <Subtopic>
1. <Question>
   - **Answer: 1234** · difficulty 6 · *easy* / *borderline: <why>*
   - Source: <url>
   - Quote: "<verbatim text containing the number>"
   - Reworded after rating: <what changed>   (only if it did)

## Cut
- **<Subtopic>:** <candidate> (<answer>): <reason>. One line each.
- **Gaps:** <subtopics with no question, and why>.
```

Expect one or two rounds of edits. If a subtopic the user cares about is
thin, send another agent to dig there specifically. Keep `rated.csv` in
step with every edit.

## 5. Upload

After approval, copy `rated.csv` to `dist/trivia-<slug>.csv`, run
`check_pack.py` on it once more, and upload it with `trivia_import_pack`:

- **`name`:** the pack's name.
- **`csv`:** the whole file's text.
- **`notes`:** "Researched and source-checked <date>; sources in the CSV source column."

The tool reads `difficulty`, ignores `source`, and reports the count, the
average difficulty, skipped duplicates, rows it couldn't parse, and the
topics. Fix any bad rows and upload again with `replace: true`.

Replacing is refused for a pack any game has already asked from, for a
starter pack, and for a pack on the board of a live game. Those cases go
to the user in the console.

## 6. Report

Tell the user:
- the pack name, question count and average difficulty
- any wording you changed after approval (there should be none), and why
- the CSV path, and the import tool's summary

The pack isn't attached to any game. The host picks it in the console.
