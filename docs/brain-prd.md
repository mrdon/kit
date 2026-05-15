# Kit Brain — Product Requirements Document

**Status:** Draft v1
**Owner:** Don
**Target:** Replace skills-as-knowledge in Kit with a first-class, multi-tenant, agent-first knowledge store
**Audience:** Implementing engineer / maintainer review
**Related:** [`docs/knowledge-pack-proposal.md`](knowledge-pack-proposal.md) (technical design — read first)

---

## 1. Summary

The **brain** is a per-tenant, multi-pack knowledge store that the kit agent reads through tools, never by enumeration. Raw documents (PDFs, markdown, docx, txt, html) get uploaded into a brain pack; an LLM compile step turns them into a small, citation-bearing set of pages organized into topics; the agent searches and navigates pages through four MCP tools. The system prompt only ever sees pack-level metadata, never page content. This replaces today's pattern of stuffing every visible skill's frontmatter into every system prompt.

The strategic backdrop — Karpathy's LLM-Knowledge-Bases gist, YC's S26 RFS company-brain wishlist, GBrain, Hyper — is covered in §2 of the proposal. This PRD is the kit-internal companion: what we build, how we migrate, what we ship in what order, and what we still don't know.

## 2. Problem

### 2.1 The skills-as-knowledge failure mode

Kit's system prompt is assembled in `internal/services/context.go:18` via `BuildKnowledgeContext`, which calls `models.GetSkillCatalog` (`internal/models/skill.go:143`) and renders one line per skill in `buildSkillCatalog`:

```go
// internal/services/context.go:69-71
for _, s := range dbSkills {
    fmt.Fprintf(&b, "- [%s] %s — %s\n", s.ID, s.Name, s.Description)
}
```

Each row carries a UUID (36 chars), slug, and a description capped at 1024 bytes by `CreateSkill` (`internal/models/skill.go:302`). At ~150–400 bytes/line: 200 skills ≈ 40 KB; 500 ≈ 100 KB (catalog dominates the cacheable prefix); 2,000 ≈ 400 KB (exceeds Haiku's working budget; routing collapses).

`SearchSkills` (`internal/models/skill.go:584`) already exists — FTS over `skills.content`/`description` (`idx_skills_content_fts`, `idx_skills_description_fts` in `001_initial_schema.sql:127-128`) — but the model has no incentive to call it when the catalog is already in the prompt. The catalog dump is actively undermining the only path that scales.

### 2.2 What the user experiences today

A 60-page HR PDF gets compressed into one skill by `ConvertToSkill` (`internal/ingest/converter.go`) — structure lost, detail unrecoverable. 200 policy uploads → 200 catalog lines, all in every system prompt. No navigation API ("what's in our HR pack?") — the agent only has `search_skills` (`internal/tools/skills.go:31`) and one-at-a-time `load_skill`. No citation model — `skills.source` is one optional TEXT field. Editing the source PDF has no effect on the skill (see §2.3).

### 2.3 The raw-file gap

Kit does **not persist raw files at all**. `ingest.NewIngester.ProcessFile` (`internal/ingest/ingest.go:31`) downloads via `slack.GetFileContent`, runs `ExtractText` (markdown/txt passthrough, PDF via shell-out to `pdftotext`, docx XML strip, zip member expansion), feeds plaintext to Sonnet, writes one `skills` row. Raw bytes are not stored — only the LLM's rewrite is. No `app_files` table, no S3 wiring, no object-storage abstraction anywhere in `internal/`. Consequences: can't re-compile when the LLM gets better, can't show the user the original document, can't re-attribute a citation back to a real page, can't run a re-ingest cycle when a source updates.

### 2.4 Why now

Covered in proposal §2. Summary: the company-brain category is forming externally (Karpathy, GBrain, Hyper, a16z), every shipped competitor is single-user-local, and kit already owns the multi-tenant Postgres, agent loop, MCP surface, and ingest stub. The missing piece is storage + retrieval — what this PRD scopes.

## 3. Goals and non-goals

### Goals

1. The system prompt contains pack-level catalog only (O(packs)), never page-level content (O(pages)). Stable upper bound regardless of corpus size.
2. The agent reaches pages through exactly four MCP tools — `brain.search`, `brain.list-topics`, `brain.get-page`, `brain.related` — and the same surface is available to MCP clients per kit's tool-parity rule (`CLAUDE.md:53`).
3. Raw files have a first-class lifecycle: upload, store, compile, recompile, delete, all with provenance.
4. Hybrid retrieval (BM25 via Postgres FTS + cosine via pgvector, fused with RRF) at p95 < 500 ms on tenants with 10k pages.
5. Migrate today's "knowledge-shaped" skills off the skill catalog without breaking workflow-shaped skills.
6. Every compiled page has citations back to a stored raw source; uncited paragraphs are rejected by a deterministic Go linter.

### Non-goals

- **Client-side sync via sx.** sx is the kit CLI/desktop client; brain content does not sync to local disk in v1.
- **Obsidian / file-system export.** One-directional dump might come later; not v1.
- **Cross-tenant brain sharing.** Every brain row is `tenant_id NOT NULL` (per `CLAUDE.md:45`); we don't open this door.
- **Real-time collaborative editing.** Edits are tool calls; no multi-cursor live docs.
- **Versioned rollback UI.** `app_brain_page_events` makes it possible later; no UI ships with v1.
- **Per-tenant custom embedding models.** Single tenant-wide model, configured in env.
- **A new file-storage backend.** Raw files go into Postgres `bytea` for v1 (see §7); S3/object-store is a Phase 5+ concern.
- **Replacing skills.** Workflow-shaped skills (the ones with tools and procedures) stay. Only knowledge-shaped skills migrate.
- **Public/anonymous brain access.** Widget surface still answers with citations, but brain content is per-tenant; no public read.

## 4. Users and use cases

**Primary persona:** owner-operator of a small business (per kit's README and decisions PRD). Reaches kit through Slack first, MCP second, PWA `/app/` third. Knowledge today lives in Slack threads, Google Docs, emailed PDFs, and an unmanaged skills list.

**Secondary:** admin/office manager who actually does the uploads. **Tertiary:** developer/agent integrator using kit's MCP from Claude Code or Cursor.

### Use cases

- **UC1 — HR-on-Slack.** Admin drag-drops 200 policy PDFs to kit. Background compile yields ~300 cited pages across ~12 topics. Employee asks "can I expense business class?" → agent runs `brain.search`, replies in-thread with the answer + source filename.
- **UC2 — Brewing.** Head brewer uploads recipe binder + fermentation logs to a `brewing` pack. Bartender asks "fermentation temp for the IPA?" → scoped `brain.search`, correct page.
- **UC3 — Supersedes.** New vacation policy uploaded; compile writes an `app_brain_links` row with `rel='supersedes'` to the old page. `brain.related` exposes the link.
- **UC4 — Agent-authored.** Weekly cron summarizes last week's support emails into pages in a `support` pack. Citations remain auditable.
- **UC5 — MCP from Claude Code.** Developer asks "how does our deploy work?" → MCP client hits `brain.search` directly; no copy-paste markdown.
- **UC6 — Provenance audit.** Manager catches a wrong answer, opens the page in the PWA, follows the citation back to the source PDF, marks for re-ingest.

## 5. Proposed solution

The proposal at `docs/knowledge-pack-proposal.md` is the canonical technical spec for:

- The seven tables under `app_brain_*` (`packs`, `topics`, `pages`, `sources`, `page_events`, `links`, `pack_scopes`)
- The four MCP tools and their JSON schemas
- The RRF hybrid retrieval SQL
- The compile pipeline (Sonnet → JSON → Go validator → upsert)
- The catalog dump replacement: a short pack-level summary in `BuildKnowledgeContext`
- The anti-poisoning model (cite-or-reject linter, raw sources never injected directly)

This PRD does not re-state that material. The remainder of this document focuses on the things the proposal touches lightly: raw-file lifecycle (§7), migration from skills (§9), pgvector availability on Dokku (§10), UX flows (§11), phasing (§12), and risks.

## 6. Data model: brain content

The proposal defines the schema. The single addition this PRD makes is **raw file storage**.

### 6.1 Sources hold extracted text; blobs hold the bytes

Proposal's `app_brain_sources.content TEXT NOT NULL` keeps the extracted text (what `internal/ingest/extract.go` produces today). For PDFs/docx/html the extracted text loses layout and media we may want later, so add:

- `app_brain_sources.raw_blob_id UUID REFERENCES app_brain_raw_blobs(id)` (nullable).
- `app_brain_raw_blobs (id UUID PK, tenant_id UUID NOT NULL, mime TEXT, size BIGINT, bytes BYTEA, sha256 TEXT, created_at TIMESTAMP)`.

Postgres `bytea` is v1 storage; kit has no object-store abstraction today (§2.3). 50 MB/file cap (§7.7). Schema doesn't change when we later move to S3 — just swap the column type to a key reference.

### 6.2 Pages cite sources, not blobs

`app_brain_pages.citations` (JSONB) holds `[{source_id, span, url?}]`. The indirection lets one blob power multiple sources (e.g., zip-member expansion) and keeps citations valid if a blob is GC'd later.

### 6.3 Versioning and audit

`app_brain_page_events` is append-only with `created`/`recompiled`/`edited`/`cited`/`deleted` and an `actor`. The page row is the head; events are the timeline. No separate `page_versions` table in v1.

## 7. Raw file lifecycle (new)

This section is new in this PRD; the proposal mentions ingest only briefly.

### 7.1 Upload paths

Slack `kitslack.File` attachments (today's `internal/app.go:262` path, routed to brain when the agent calls `brain.add_source`), the MCP tool `brain.add_source(pack, kind='upload', name, mime, content_b64)`, a PWA drag-drop on a new "Knowledge" tab (POST `/app/api/brain/sources`), and direct bearer-token API. Slack stays primary because today's mention path already carries files. Integration auto-pull (Drive, GitHub, Notion) is post-v1.

### 7.2 Storage flow

`bytes → sha256 → dedupe → app_brain_raw_blobs → ExtractText (mirror of internal/ingest/extract.go) → app_brain_sources (text + raw_blob_id) → mark pack dirty`. SHA dedupe is per-tenant — the same blob uploaded to two packs reuses one `app_brain_raw_blobs` row via two source rows. Cross-tenant dedupe is off; tenant isolation supersedes storage savings.

### 7.3 Compile triggering

Proposal §5 describes a `scheduler.CronJob` (kit's existing pattern; see `internal/scheduler/runner.go:43` `RegisterJobRunner`) on dirty packs with ~60 s debounce. Concretely: `app_brain_packs.dirty_since TIMESTAMP NULL`; source INSERTs/updates set `dirty_since = now()` on the parent pack in the same transaction; a `brain_compile` `JobRunner` wakes every 30 s, picks packs whose `dirty_since` is older than 60 s, compiles, clears on success. Failures bump a retry counter; 5 strikes → `compile_error` state + admin ping.

### 7.4 The compile step itself

Mirrors proposal §5. One Sonnet call per pack per dirty cycle, prompted to read all sources, cluster into topics, and emit `{topic_slug, title, pages: [{slug, title, summary, body, citations: [{source_id, span}]}]}` with one `is_index=true` page per topic and at least one citation per paragraph. Go validates: reject malformed JSON; reject unresolved `source_id`; upsert pages by `(tenant_id, pack_id, slug)`; emit `app_brain_page_events`; re-embed changed pages; extract `[[wikilinks]]` into `app_brain_links`. Compile is idempotent — same sources, same pages. No fallback to uncited pages (per `coding-standards-1.md`: "Never 'fallback to basic data'").

### 7.5 Re-ingest on source update

Same path + new bytes + new sha256 → new `app_brain_raw_blobs` row; `app_brain_sources` updated in place with new `content`/`raw_blob_id`/`ingested_at`; pack marked dirty; pages derived from this source re-compile. Old blob stays referenced by `app_brain_page_events` for citation history; a GC job cleans blobs with no live source ref and no event ref newer than 90 days.

### 7.6 Deletion semantics

**Source delete:** pages citing only this source are tombstoned (`app_brain_pages.tombstoned BOOLEAN`); excluded from `brain.search`; hard-deleted after 30 days. Old citation links resolve to a "tombstoned because source removed" response. **Page delete:** admin/agent via `brain.delete_page`; same tombstone + 30-day GC. **Pack delete:** cascades; pack-level summary kept in `app_brain_deleted_packs` for 1 year. Direct blob delete is not a user operation.

### 7.7 File types and limits (v1)

**Supported:** `.md`/`.txt`/`.csv` (passthrough, already in `extract.go:20`), `.pdf` (`pdftotext` — `poppler-utils` is already in the Dockerfile runtime stage), `.docx` (XML strip in `extractDocx`), `.zip` (member expansion, one source per member). **New for v1:** `.html` (simple strip-to-text). **Deferred:** images (vision pass, Phase 5+), audio (kit has whisper at `internal/transcribe/` but transcripts-as-sources is post-v1), `.xlsx`/`.pptx` (need a thoughtful extractor).

**Limits:** 50 MB/file, 500 MB/pack raw storage, 5000 sources/pack. These match Postgres `bytea` ergonomics, not product ambition — revisited when object storage lands.

## 8. Retrieval (brief)

Proposal §6 has the detail. Recap:

- **Four MCP tools**, identical surface on agent side (`internal/tools/brain.go`) and MCP side (`internal/mcp/brain.go`) per kit's tool-parity rule.
- **Hybrid retrieval**: tsvector FTS (BM25-ish via `ts_rank_cd`) + pgvector cosine, fused with RRF.
- **pgvector** is required for v1. See §10 for production availability.
- The agent **never** sees a page in its system prompt — only via `brain.get-page`.

## 9. Migration from current skills

The proposal §7 sketches the categorical migration. This PRD expands.

### 9.1 The `kind` column

Migration `058_skills_kind.sql`:

```sql
ALTER TABLE skills ADD COLUMN kind TEXT NOT NULL DEFAULT 'workflow'
    CHECK (kind IN ('workflow', 'knowledge'));
CREATE INDEX idx_skills_kind ON skills(tenant_id, kind);
```

Default `'workflow'` means today's skills behave identically until reclassified. The immediate scaling win comes from updating `models.GetSkillCatalog` (`internal/models/skill.go:143`) to filter `WHERE s.kind = 'workflow'`. The instant any skill is reclassified `'knowledge'`, it leaves the catalog.

### 9.2 Discovery

An admin-only MCP tool `brain.classify_skills` runs cheap deterministic heuristics: `LENGTH(content) > 3000` with no procedure pattern → knowledge; `skills.source LIKE 'upload:%'` → likely knowledge; zero references from `jobs.skill_id` (`internal/models/job.go`) → not a workflow; no fenced tool-call code blocks → reference content. Returns ranked `(skill_id, score, reason)`. UI: a "Migrate to brain" tab in the PWA admin section.

### 9.3 Reclassification

Two admin-gated paths. **Single:** `brain.reclassify_skill(skill_id)` flips `kind='knowledge'`, creates one `app_brain_sources` row in a `legacy` pack with `content = skills.content` and `metadata={"migrated_from_skill_id": "..."}`, triggers compile. Original `skills` row stays. **Bulk:** `brain.bulk_reclassify_skills(skill_ids, pack_slug?)` does N at once, optionally pooled into a named pack; compile batches one Sonnet call per dirty cycle.

### 9.4 Backwards compatibility

`skills` rows stay during migration. `load_skill` keeps working; if the skill has `metadata.migrated_to_brain_page_id`, the tool returns a redirect message including the brain page ID so the agent learns to call `brain.get-page` instead. `jobs.skill_id` references continue to resolve.

### 9.5 Sunset timeline

Six months after Phase 4 GA, a migration **deletes** all `skills.kind='knowledge'` rows that have a successful brain-migration record. Workflow skills untouched forever. Conservative on purpose; per-tenant aggressive cleanup is available via CLI.

### 9.6 Not migrated

`rules` (behavioral guidelines), `memories` (per-thread facts), and the contents of `skill_references` (folded into the parent's sources at migration). Each stays as-is.

## 10. Dokku / pgvector infrastructure (new)

This is the riskiest infrastructure question. The proposal §6 assumes pgvector is available. This section investigates what the kit repo actually says about production.

### 10.1 What the repo confirms

| Source | Statement |
|---|---|
| `compose.yaml:3` | Dev DB image is `pgvector/pgvector:pg16` |
| `.github/workflows/test.yml:18` | CI DB image is `pgvector/pgvector:pg16` |
| `README.md:33` | "PostgreSQL 16 (pgvector image)" listed as a prerequisite |
| `CLAUDE.md:35,38` | "Postgres 16 (pgvector image)" and "Deployed on Dokku (apps.twdata.org)" |
| `.claude/skills/architecture.md:453` | "Postgres 16 with pgvector (`pgvector/pgvector` image, pgvector available but not required for MVP)" |
| `docs/knowledge-pack-proposal.md:268` | "no pgvector tables despite `pgvector/pgvector:pg16` running in `compose.yaml`" — implies extension never enabled |

### 10.2 What is not confirmed in the repo

The actual image and tag used by Dokku's `postgres` plugin on `apps.twdata.org`; whether prod was created with the default `dokku/postgres` (which is `postgres:alpine`, **no pgvector**) or with `POSTGRES_IMAGE=pgvector/pgvector POSTGRES_IMAGE_VERSION=pg16` set before `postgres:create kit-db`; whether `CREATE EXTENSION vector` has ever been run inside the kit DB.

### 10.3 Hypothesis

Given (a) `CLAUDE.md` repeatedly says "Postgres 16 (pgvector image)", (b) `.claude/skills/architecture.md` explicitly says "`pgvector/pgvector` image, pgvector available", and (c) dev/CI standardized on pgvector, **the most likely state is: prod was created with the pgvector image but `CREATE EXTENSION vector` was never issued**, consistent with proposal §6's "no pgvector tables despite pgvector running in compose.yaml." This is a hypothesis, not a confirmation — the repo cannot resolve it alone.

### 10.4 What evidence would resolve it

One SSH call (the connect pattern is the same one used in `CLAUDE.md:104-110`):

```bash
ssh dokku@apps.twdata.org 'postgres:connect kit-db' <<'SQL'
SELECT current_setting('server_version');
SELECT * FROM pg_available_extensions WHERE name = 'vector';
SELECT * FROM pg_extension WHERE extname = 'vector';
SQL
```

Three outcomes: **(1)** `pg_extension` already has `vector` — done. **(2)** `pg_available_extensions` lists `vector` but it isn't installed in the kit DB — one-line migration `CREATE EXTENSION vector;`. **(3)** `vector` isn't even available — recreate the dokku postgres service with `POSTGRES_IMAGE=pgvector/pgvector POSTGRES_IMAGE_VERSION=pg16` and restore from backup (disruptive, needs a maintenance window), or run a separate Postgres for vectors (worse — no cross-DB joins).

### 10.5 Concrete plan

Phase 0 (§12) resolves §10.4 before any schema lands: (1) SSH check, record outcome in `docs/brain-pgvector-status.md`, (2) remediate to outcome 1 if needed, (3) ship migration `058_enable_pgvector.sql` (`CREATE EXTENSION IF NOT EXISTS vector;`), (4) then ship `059_brain_tables.sql`.

### 10.6 Fallback if pgvector cannot be enabled

If §10.4 outcome (3) and recreating the dokku postgres service is unacceptable, v1 runs **FTS-only**: `embedding` column exists but stays NULL, `brain.search` runs the FTS half of RRF only. Recall degrades, system functions. Enabling pgvector later is a non-disruptive backfill (background job embeds existing pages). This is an infrastructure-driven degraded mode, not the "silent fallback to basic data" the coding standards prohibit.

## 11. UX flows (sketches)

- **Manual page.** PWA `/app/brain/<pack>/new` — title, optional topic, markdown body. Writes `app_brain_sources kind='manual'`, marks pack dirty, page appears after the next compile (~60 s).
- **Upload.** Drag-drop on pack page or Slack DM. Slack: thinking indicator → compile → in-thread reply with page links. PWA: pending state until compile finishes.
- **Search.** PWA search box → `brain.search` → ranked hits with summaries → click loads via `brain.get-page`. Slack/MCP: agent runs the same tool surface.
- **Delete.** PWA trash icon on a source row → confirmation modal with "X pages depend on this source — they will be tombstoned" warning. Slack: admin-gated.
- **Provenance.** Page view shows `citations[]` as anchor links into the source view; the raw PDF can be downloaded but in-page spans are over the extracted text in v1.

## 12. Phasing

**Phase 0 — pgvector confirmation (≤2 days).** §10.4 SSH check + remediation + migration `058_enable_pgvector.sql`. Exit: `pg_extension` has `vector` in prod. Size: 0.5–2 days depending on §10.4 outcome.

**Phase 1 — schema + tools + manual CRUD (≤2 weeks).** Migration `059_brain_tables.sql`, `internal/apps/brain/` module, four MCP tools through `services.BrainService`, agent-side `internal/tools/brain.go`, PWA pack/topic/page CRUD. No raw ingest, no compile yet. Exit: admin writes pages by hand; agent answers via `brain.search`/`brain.get-page`; catalog dump updated. Size: ~10 engineer-days.

**Phase 2 — raw ingest + compile (≤2 weeks).** `app_brain_raw_blobs`, source ingest paths (Slack/PWA/MCP), compile `JobRunner`, embedder client, citation validator. Exit: upload a PDF, get cited pages within ~60 s. Size: ~10 engineer-days.

**Phase 3 — skills migration (≤1 week).** `kind` column, catalog filter, `brain.classify_skills`, single/bulk reclassify tools, PWA "Migrate to brain" tab. Exit: 200-skill migration in <30 min of admin time. Size: ~5 engineer-days.

**Phase 4 — legacy cleanup (post-soak, 6 months).** Redirects for migrated skills; eventual deletion. Exit: no `skills.kind='knowledge'` rows in any catalog path.

**Phase 5 (post-v1).** Object storage, OCR/audio sources, integration auto-pull, versioned rollback UI.

## 13. Success criteria

Baseline: kit today with skills-as-knowledge.

1. **System prompt size:** on tenants with ≥100 reclassified knowledge skills, `BuildKnowledgeContext` output drops by ≥**80%** (measured by byte count over a 100-session sample).
2. **Search latency:** `brain.search` p95 < **500 ms** on a synthetic 10k-page tenant in CI.
3. **Compile cost:** ≤ **$0.05/source** average (Sonnet usage logs).
4. **Citation coverage:** ≥**99%** of compiled-page paragraphs cite a resolved source (Go linter, fail-and-retry).
5. **Migration adoption:** on tenants with >100 skills, ≥**60%** of skills reclassified or deleted within 90 days of Phase 3 GA.
6. **Answer accuracy:** on a 50-question eval set per tenant, correct-cited-answer rate up by ≥**15 pp** vs. skill-catalog baseline (5 dogfood tenants).

## 14. Risks

1. **pgvector unavailability in prod.** Mitigated by Phase 0 + FTS-only fallback (§10.6). Residual: fallback is worse than design point; acceptable for soft launch.
2. **Self-referential knowledge / LLM-of-LLM corruption** (Falconer's poisoning mode). If kit's own outputs get auto-ingested back as sources, content degrades. Mitigated: raw sources required, citations deterministic, agent outputs not auto-ingested. Watch: integration auto-pull (Phase 5) is where this risk re-enters.
3. **Migration breakage on heavy skill users.** Skills wired into `jobs.skill_id` may break if migrated and redirect is incomplete. Mitigated by §9.4 — skills stay until explicitly deleted in Phase 4.
4. **Compile cost at scale.** A pack hit 50 times in a day is 50 Sonnet calls. Mitigated by 60 s debounce + retry budget; may need a daily-only mode for very large packs.
5. **Compile quality drift.** LLM topic clustering can shift between cycles, churning pages. Mitigated by upsert-by-slug + idempotent prompts; a "topic stability" metric triggers a rebalance cron.
6. **Storage bloat from `bytea`.** Raw files in Postgres bloat WAL/backups. Mitigated by per-file (50 MB) / per-pack (500 MB) caps + 90-day GC. Phase 5 object storage is the structural fix.
7. **Tenant isolation bug in hybrid retrieval.** RRF + FTS + vector CTEs are harder to review than a single SELECT. Mitigated: every CTE filters on `tenant_id`; a cross-tenant fixture test in `services/brain_test.go` asserts zero leakage; query-plan check in CI.
8. **Prompt injection via compiled pages.** A malicious source could embed instructions that survive compile. Mitigated: compile has its own system prompt; agent prompt declares page content as data; raw sources never reach the agent. Add a per-page "instruction-like content" linter in Phase 2.

## 15. Open questions

1. **Embedding model.** Kit has no OpenAI dep today. (a) add OpenAI as optional, (b) Voyage/Cohere/forthcoming Anthropic embeddings, (c) self-host BGE via ONNX. **Recommend:** OpenAI `text-embedding-3-small` for v1.
2. **`brain.search` default scope.** All visible packs vs. require `pack` arg. **Recommend:** all-visible; agent passes `pack` when the user names one.
3. **PWA tab.** New top-level "Knowledge" vs. fold into renamed "Skills". **Recommend:** new top-level — clearer mental model.
4. **Compile batching.** Per-pack vs. per-source LLM call. **Recommend:** per-pack for v1; switch if topic drift bites.
5. **Mobile editing.** Markdown on phone is rough. **Recommend:** add/delete/comment on phone, edit on desktop.
6. **Reclassification UX.** Per-skill review vs. bulk-by-heuristic. **Recommend:** bulk-approve with a "review first" toggle.
7. **No-raw-storage compliance mode.** Some tenants can't store raw bytes in Postgres. **Recommend:** tenant flag `brain_store_raw_blobs BOOLEAN DEFAULT TRUE`; when false, only `content` text persists.
8. **Pgvector index type.** IVFFlat vs. HNSW. **Recommend:** IVFFlat for v1; revisit with real data after Phase 2.
9. **`brain.classify_skills` surface.** Admin-only MCP + PWA "Migrate to brain" tab. Not exposed to the agent on a regular turn.

---

*This PRD lives alongside the technical proposal at [`docs/knowledge-pack-proposal.md`](knowledge-pack-proposal.md). Schema and tool specifications in the proposal are normative; this PRD is the product framing, raw-file lifecycle, migration strategy, infrastructure investigation, phasing, and risk register.*
