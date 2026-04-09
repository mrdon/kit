---
name: creating-skills
description: "How to author a high-quality Kit skill: choosing names and descriptions, writing dense reference content for Haiku, and attaching long source material as references. Use whenever creating, editing, reviewing, or summarizing a skill — including when ingesting an uploaded document."
---

# Creating Skills

Read this before calling `create_skill`, editing a skill, or turning an uploaded document into a skill.

## Who reads your skill

Kit's chat agent runs on **Claude Haiku**, answering questions in Slack. Haiku is fast and cheap but has a tight context budget. Every loaded skill competes with the system prompt, rules, memories, conversation history, and the user's actual question.

A bloated skill literally pushes other skills out of context. The skill's job is to let Haiku produce a **useful, short answer** by lifting a fact and quoting it. **Density beats completeness.**

## Two flavors of skill

Pick the right shape before you start writing. Most skills are reference, not workflow.

**Reference skills (the common case — ~90%).** Factual knowledge: policies, FAQs, prices, hours, contact lists, product specs, handbook excerpts. Optimize for *lookup*. Structure as bullets, tables, and short Q&A pairs. The bot grabs a fact and quotes it.

**Workflow skills (less frequent).** Step-by-step procedures where the *order* matters: closing checklists, onboarding sequences, escalation flows. Use numbered steps; one action per step. Only reach for this shape when sequencing actually matters — otherwise write a reference skill.

The rest of this guide assumes reference unless noted.

## Anatomy of a Kit skill

Three fields plus optional attachments:

- **`name`** — slug, used for addressing.
- **`description`** — what the skill is and when to use it. Pre-loaded into the agent's catalog.
- **`content`** — the body. Fetched on demand when the agent decides this skill is relevant.
- **References (`skill_references`)** — additional text files attached to the skill. Also fetched on demand.

Only `name` and `description` live in the catalog. Everything else costs context the moment it loads. That is why descriptions are the single most important thing to get right, and why `content` must be ruthlessly dense.

## Naming

- Lowercase letters, digits, hyphens. 1–64 chars. Validator: `^[a-z0-9]+(-[a-z0-9]+)*$`.
- Prefer **gerund form** (verb + -ing) or a clear noun phrase.
- Be specific. The name is part of how the agent and humans recognize the skill.

**Good:** `closing-checklist`, `payroll-process`, `handling-refunds`, `employee-handbook`, `holiday-hours`, `vendor-contacts`

**Bad:** `helper`, `stuff`, `doc1`, `notes`, `info`, `things`

Kit auto-slugifies on create, but garbage in → garbage out. Pick the slug deliberately.

## Writing the description

This field decides whether your skill ever gets loaded. Get it right.

- **Third person, present tense.** "Documents the…", "Lists the…", "Explains how to…". Never "I can…" or "You can…".
- **Say what AND when.** Both halves. The "when" is how the agent knows to pull it in.
- **Use the words a real user would say in Slack.** If people ask about "refunds" and "returns", put both in the description.
- 1024-char hard cap. Aim for 1–2 sentences.

**Good:**

> Lists the store's return and refund policy, including the 30-day window, receipt requirements, and exceptions for sale items. Use when a customer asks about returns, refunds, exchanges, or "can I bring this back".

**Bad:**

> Helps with returns.

> I can help you understand our return policy and answer questions about it.

> Returns information.

## Keeping content dense

Haiku will quote from `content` directly. Write it so the answer is easy to lift.

- **One topic per skill.** Split rather than balloon.
- **Only facts specific to this business.** Assume Claude already knows general world knowledge — don't explain what a PDF is, what payroll is, or how email works.
- **Bullets and tables, not prose.** A bullet of 8 words beats a sentence of 30.
- **Lead with the answer.** Background only if it changes the answer.
- **No throat-clearing.** Don't write "This skill describes…" or restate the description. Don't add marketing copy.
- **Use absolute dates.** "Effective 2026-01-01", not "starting next month". Kit skills outlive the moment they were written.
- **Consistent terminology.** Pick one word for each thing ("refund", not "refund/return/credit") so both Postgres FTS and Haiku find it.

## Handling large source documents

**Kit references are text-only.** There is no binary blob storage today. So when you start from a PDF, DOCX, or other binary:

1. Extract the text (Kit's ingest does this for PDFs, DOCX, and markdown automatically on upload).
2. Write a **dense summary** into the skill `content`.
3. Save the **full extracted text** as a `skill_reference` named after the source — e.g. `employee-handbook-full.md`.
4. Keep the **original binary** in your own document store (Drive, Dropbox, email). Kit holds the text, not the file.

Recommended shape for the summarized `content`:

- One-line purpose.
- Key facts as bullets.
- Tables and numbers preserved verbatim where they matter (prices, dates, codes, phone numbers).
- A pointer at the bottom: `Full text: see attached reference employee-handbook-full.md. Original PDF: Drive → HR → Handbook.`

**Do not paste 50 pages of OCR'd PDF text into `content`.** It blows Haiku's context on every single question. Summary in `content`, full text in a reference, original binary outside Kit.

## Worked example

A user uploads `employee-handbook.pdf`.

**Bad skill:**

```
name: handbook
description: Employee handbook.
content: <12,000 words of OCR'd PDF text, headers and footers and page numbers and all>
```

The description is useless for selection. The content torches Haiku's context every time someone asks about *anything* HR-related.

**Good skill:**

```
name: employee-handbook
description: Summarizes the employee handbook: PTO accrual, sick leave, dress code, harassment policy, and the disciplinary process. Use when an employee asks about time off, leave, conduct, dress code, or "what does the handbook say".
content:
  # Employee Handbook (summary)

  - PTO: accrues at 1.5 days/month, max 30-day balance, use-it-or-lose-it on Dec 31.
  - Sick leave: 10 days/year, separate from PTO, no carryover.
  - Dress code: business casual M–Th, jeans OK Fri.
  - Harassment: zero tolerance. Report to HR (hr@example.com) or any manager.
  - Discipline: verbal warning → written warning → final warning → termination. HR sits in on written and after.

  Full text: see attached reference employee-handbook-full.md.
  Original PDF: Drive → HR → Handbook.
references:
  - employee-handbook-full.md  # the complete extracted text
```

The description names every Slack-realistic trigger word. The content is short enough to load on every HR question without crowding anything else out. The full text is one fetch away if Haiku needs to dig deeper.

## Pre-flight checklist

- [ ] Name is a specific slug (gerund or clear noun phrase), not `helper` or `notes`.
- [ ] Description is third person and says **what** and **when**.
- [ ] Description includes the words a user would actually say in Slack.
- [ ] Skill covers one topic.
- [ ] Content is dense — bullets and tables, no filler.
- [ ] Content is specific to this business, not generic knowledge.
- [ ] Long source text is in a reference, not pasted into `content`.
- [ ] Original binaries (PDF, DOCX) live outside Kit; the skill points to where.
- [ ] No relative dates ("next month") — absolute dates only.
- [ ] Terminology is consistent throughout.
