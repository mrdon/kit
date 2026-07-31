# Binary file inputs for skills

## Status

Design statement, not a feature request.

## TL;DR

**Kit does not store binaries.** Anything that arrives as a binary file gets converted to markdown at ingestion time and stored as text in `skill_references.content`. If a user needs the original bytes (signed PDF for legal citation, raw image, source spreadsheet), the canonical copy lives in their own document store — Google Drive, Dropbox, S3, wherever — and the skill should reference it by URL or ID.

The `add_skill_file` MCP and agent tools remain text-only. No `bytes` column, no `download_skill_file`, no base64 transit. The simplest API that solves the actual problem.

## Why not store binaries

The temptation to add a `bytes BYTEA` column to `skill_references` came from one use case: attaching a signed PDF (operating agreement, executed contract) as the "exact-wording escape hatch" for a hand-authored skill summary. The reasoning was *"the dense summary covers 95% of questions; load the source PDF when you need clause 7.4 verbatim."*

That reasoning is wrong for kit specifically. Three reasons:

1. **Kit is a Slack bot, not a document store.** Every tenant already has a canonical place for authoritative documents — Google Drive for most, sometimes Dropbox or a legal-counsel portal. Duplicating bytes into kit creates a second source of truth. When the original is amended or re-signed, the kit copy goes stale silently. Linking to the canonical store keeps a single source of truth.

2. **Binaries don't help the agent answer questions.** The LLM consumes text. The cheap path is the dense summary in the skill body. The medium path is searchable extracted text. The expensive path — *retrieve the actual PDF and look at it* — only matters to a human, and a human is better served by a Drive URL than by a base64 round-trip through MCP and back.

3. **Storage and operational cost.** PDFs are big. Storing the full binary multiplies tenant storage for content the LLM never reads. Backups, replication, restore times all suffer for a feature with no inference-time payoff.

## What kit does instead

### Existing path (Slack ingest, unchanged)

When a user drops a PDF into Slack, `internal/ingest/` already does the right thing:

1. Download the binary.
2. `internal/ingest/extract.go` runs the appropriate extractor (`pdftotext` for PDFs, DOCX extractor, etc.) to produce raw text.
3. `ConvertToSkill` runs the extracted text through Claude Sonnet to produce a structured markdown skill.
4. The original binary is **discarded**. Only the markdown skill is persisted.

This is the right shape. Kit's job is to turn unstructured uploads into searchable, LLM-consumable knowledge — not to be a file server.

### MCP / agent tool path (`add_skill_file`)

Already text-only. Callers that have a binary on hand are responsible for converting it to markdown before calling the tool. Two reasonable conversion paths:

- **Client-side conversion.** A Claude Code session, a script, or any client with PDF tooling extracts the text, lightly formats it as markdown (preserving headings, tables, signature blocks), and uploads via `add_skill_file` with `content: <markdown string>`. The client knows the canonical Drive URL or file ID and references it in the skill body.
- **Server-side conversion (future, optional).** If we want to let MCP callers upload a PDF without doing the conversion themselves, the right move is *not* to add a binary column — it's to expose the existing ingest pipeline as an MCP tool. Something like `ingest_file_into_skill(skill_id, filename, binary_content_b64)` that runs `extract.go` + `ConvertToSkill` server-side and persists the resulting markdown via the same `skill_references` row that any other text upload would create. Same storage, same FTS, same load path. The binary is decoded, extracted, and immediately discarded.

We don't need the server-side path yet. Client-side conversion is sufficient for the current use cases (founder hand-authoring primary-doc skills from Drive PDFs). Add the server-side ingest tool only when we have a real caller that can't do the conversion themselves.

## What this means for skill authors

When you want to attach a binary source document to a skill:

1. **Find the canonical home.** It's in Drive, Dropbox, somewhere with stable IDs. If it isn't, fix that first.
2. **Convert the document to markdown.** Preserve headings, tables, signature info, anything searchable. Strip cosmetic formatting.
3. **Upload the markdown via `add_skill_file`.** Filename should reflect what it is (`tsl-operating-agreement.md`), not the source format.
4. **Reference the original in the skill body.** Include the canonical URL or store-specific ID. Make it clear that the markdown is a derived artifact and the linked original is authoritative.
5. **Recompile when the source changes.** If the PDF gets re-signed or amended, re-run steps 2–3. The Drive link stays stable.

## Open questions / future work

- **Server-side ingest tool.** If we ever get callers that can't do client-side conversion (custom integrations, third-party MCP clients without extraction tooling), add an `ingest_file_into_skill` tool that wraps the existing `internal/ingest/` pipeline. Don't add a binary column to do it — pipe straight from upload through extraction to text storage in a single operation.
- **Stale-link detection.** Skills that reference Drive URLs eventually point at moved or deleted files. A periodic crawler that resolves linked URLs and flags broken ones would be valuable, but is out of scope here.
- **Image-only PDFs and OCR.** `pdftotext` doesn't handle scanned/image-only PDFs. If we encounter these, the extractor needs an OCR fallback (Tesseract or a hosted OCR API). Same pipeline, just a different extractor — no change to the storage philosophy.
