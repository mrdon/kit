# Kit Posters App — spec

**Status:** Implemented (2026-10-10), all six phases. One deviation from the design below: the renderer runs as a child process of the kit binary (same image, `POSTER_RENDERER_DIR`) rather than a separate Dokku app; `POSTER_RENDERER_URL` still allows a split later. Prototype lives in `~/dev/marketing` (commit `3ab2496`).
**Audience:** Implementing agent / Don

## Problem

A tenant adds an event and wants a poster for it right then. Today posters are made
outside Kit and uploaded to the event (`hero_attachment_id`). We want: press
**Generate poster** on the event, see about 7 different on-brand options in seconds,
pick one, then chat with an agent to change it ("bigger title", "photo on the left",
"add a monkey in the middle"), and attach the result to the event. The designs come
from a per-tenant **template library** that grows from use: good posters are saved
back as templates, and an uploaded reference image can become a new one.

Two hard rules carry over from the prototype and are non-negotiable:

1. **No generated imagery.** Every image in a poster is a real photo from the tenant's
   Drive library (or Pixabay, if the tenant turns stock on) or an approved logo file. No image-generation model is ever called, and the
   agent cannot draw imagery (no inline SVG). Photos may be cropped and scaled.
2. **No invented facts.** Copy comes from the event's public fields. `prep_notes`
   (staff notes) never reach public copy.

Kit is multi-tenant, so nothing Gravity-specific may be hard-coded: colors, fonts,
logos, photo sources and voice are per-tenant settings and skills.

## Principle: Kit's own model use stays minimal

Anything expensive happens in Claude Code or another local harness, over Kit's MCP
tools, on the caller's model and account. Kit's server makes only small, bounded
Sonnet calls that a person directly triggered, and nothing runs on a schedule with a
model. The complete list of Kit model calls:

| Call | When | Size |
|---|---|---|
| Write copy from the event | Generate poster | one call, plus one retry on validation errors |
| Choose the hero photo | Generate poster | one call over the top 5 search results |
| Chat edit | each chat message on a poster or template | a few tool turns, capped (e.g. 3 render attempts per message) |
| Update facts | Update poster on a stale poster | one call |
| Derive the brand | only if the guide has no `json` tokens block | one call per skill version |

Everything else is either deterministic (option generation, validation, safe-area
checks, rendering, Drive sync, full-text search) or harness work over MCP: photo
indexing, authoring new templates, big redesigns, bulk regeneration. If an
implementation needs a model call that isn't in this table, it belongs on the harness
side.

## What the prototype proved

`~/dev/marketing` renders Gravity posters and is the reference implementation. Read
its `CLAUDE.md` first. Facts established there:

- **Stack:** React layouts → Satori (`satori@0.47.1`, pinned) → SVG → resvg-js → PNG.
  sharp crops and scales photos first. No browser. About 1s and ~300MB peak per
  1080×1350 poster. Text becomes paths, so no fonts need installing.
- **Satori 0.38+ is required** for inline text (mono numerals inside a line). It was
  releasing several times a day when pinned; bump only with a golden-image comparison.
- **Static fonts only.** Satori cannot read variable fonts; instance them to the
  weights used (`fonttools varLib.instancer`).
- **Seven layouts** in `src/compositions/Poster.tsx`: `split`, `full`, `type`, `band`,
  `strip`, `inset`, `when`. Images reach layouts through a `media` prop
  (`src/components/media.ts`), because Satori has no React context or hooks.
- **Option generator** `src/options.ts`: deterministic, no LLM. It varies layout,
  ground and photo, never the words. Photo pool = the content's photos plus the hero's
  library folder (not the library root).
- **Safe-area check** in `src/satori/renderPoster.tsx`: Satori's `onNodeDetected`
  gives every node's box. Any text node or the logo outside the canvas's safe margins
  fails the render, and the generator skips that option. This catches long titles
  without any LLM.
- **Gotchas found the hard way:** Satori defaults to `box-sizing: content-box` (padded
  canvases overflow); any `div` with a component child needs `display: flex`; a bare
  text run in a flex parent loses adjacent spaces (wrap mixed runs in one `<span>`);
  `textWrap: balance` is skipped when text contains inline elements; `objectPosition`
  takes percentages but `background-size: cover` is ignored.

## What Kit already has (from a codebase survey)

- **App framework:** `internal/apps/apps.go` `App` interface plus `DescribableApp`
  for per-tenant toggling (`enablement.go`, `tenant_app_enablement`). Tool metadata in
  `[]services.ToolMeta`, one `dispatchCore` switch shared by agent and MCP (see
  `internal/apps/events/{tools,core,agent,mcp}.go`). Routes: SPA `/{slug}/web/...`,
  JSON `/{slug}/api/<app>/...`, binary `/{slug}/apps/<app>/...`.
- **Event posters:** `app_events.hero_attachment_id` → `attachments` (encrypted BYTEA,
  10 MiB cap, `internal/attachment`). `events/poster.go` accepts JPEG/PNG/WebP/GIF up to
  8MB and refuses SVG. Upload route `POST /{slug}/api/events/{id}/poster`. The drawer is
  `EventDrawer` in `web/console/src/pages/Events.tsx` (poster section ~L726–772).
- **Chat:** shared widget `web/shared/src/chat/`, SSE via `internal/sse`,
  `chat.Execute` → `agent.Agent.Run` with `WidgetAllowedTools` to restrict tools.
  **Object-scoped chat only exists for swipe-card items** (`threadKey(card, …)`); a
  poster needs a new scope. Claude calls are non-streaming (status/tool events only).
- **Models:** `internal/anthropic/models.go` (Opus, Sonnet, Haiku). Vision via
  `DescribeImage` (Sonnet).
- **Settings:** per-app settings tables + admin page (pattern:
  `app_event_settings`, `EventsSettings.tsx`). Tenant integrations with encrypted
  tokens (`integrations.RegisterTypeSpec`). Google Calendar uses a **service account**.
- **No** Google Drive integration, **no** object storage, **no** Node in the production
  image.
- **Scheduled work:** `scheduler.RegisterScheduledTask` from the app's `Init`;
  `LLMBound` tasks run on the serialised agent lane.
- **Skills:** `services.ResolveByName(ctx, caller, "branding-guide")` resolves tenant
  skills (DB) before builtins.
- **Rules:** tenant_id on every table with `ON DELETE CASCADE`, `app_` table prefix,
  agent/MCP tool parity in the same commit, files ≤500 lines, functions ≤60 lines,
  prompts in `prompts/*.tmpl`, update the user guide skill, `make prepush`.

## Design

### Shape

```
Events drawer ── Generate ──► 7 portrait options ── pick ──► event poster (portrait)
      │                                 ▲
      └── "Open in Posters" ──┐         │ copy + photo pick (Kit, Sonnet)
                              ▼         │ option plan over active templates
Posters tab (console)                   │
  posters by event · templates · photo library · settings
  poster page: preview, formats, versions, chat ──► poster agent (Sonnet)
                                                        │ edit_poster_source
                                                        ▼
                              poster-renderer (Node, separate Dokku app)
                                compile TSX → run in isolate → validate → Satori → PNG

Claude Code / any harness ── Kit MCP ──► same poster, template and indexing tools
  (heavier models for template work and photo indexing; Kit itself only runs Sonnet)
```

### Who can do what

Anyone in the tenant can generate posters, edit them in chat, set one on an event,
and create, edit, activate or archive templates. The admin page (Drive links, logo
mapping, stock setting) is admin-only, like every Kit settings page.

### The renderer: a separate service

A stateless Node service, `poster-renderer`, deployed as **its own Dokku app** from a
`renderer/` directory in the kit repo (`dokku builder-dockerfile:set ... dockerfile-path
renderer/Dockerfile`). It's separate because:

- It runs **LLM-written code**. Keep that out of the process holding tenant secrets.
- Kit's image stays Go-only and Node never touches Kit's memory budget (renderer peaks
  around 300–400MB per render).
- It can be restarted, scaled or rate-limited on its own.

Kit calls it over HTTP with `POSTER_RENDERER_URL` and a shared secret
(`POSTER_RENDERER_TOKEN`, header auth). It keeps no records. Kit sends the source, brand
and **image references** (Drive file ids, Pixabay ids with a current download URL); the
renderer's own host code (never the sandboxed poster code) downloads them and keeps a
disk LRU of prepared copies (upright, ≤2400px) keyed by id + Drive `modifiedTime`. A
cold render pays the Drive download once; warm renders don't.

Endpoints:

| Endpoint | In | Out |
|---|---|---|
| `POST /render` | `source` (TSX), `format`, `brand` (tokens, grounds, fonts by hash, logo refs), `photos` (id → image ref + focus) | `png`, `tree_summary`, `problems[]` |
| `POST /options` | `content` (JSON), `brand`, `photo_pool`, `templates`, `count`, `format` | up to `count` × {`source`, `png`, `problems`} |
| `POST /inspect` | an image ref | thumbnail JPEG (for the vision description), width/height, C2PA verdict |

Port from the prototype: `src/compositions/Poster.tsx`, `src/components/*`,
`src/options.ts`, `src/satori/renderPoster.tsx`, `src/schema.ts`, the copy rules in
`scripts/check.mjs`. Replace `src/brand.ts` constants with the per-request `brand`.
Pin `satori@0.47.1`, `@resvg/resvg-js@2.6.2`, `sharp@0.34.4`.

### A poster is TSX source

When the user picks an option, the poster becomes **its own TSX file**: the template's
code with the content inlined, stored as a version. From then on the poster is
independent; editing it never changes the template (that's what Save as template is for). Every chat edit produces a new
version from the previous one. Code, not JSON, because the requests are open-ended and
only code absorbs anything.

**Running that code safely** (all inside the renderer):

1. **Compile, don't bundle.** esbuild transforms the TSX. An import allowlist rejects
   anything except `react` and the poster kit (`@poster/kit`: `Text`, `Logo`, `Photo`,
   `CopyHead`, `CopyFoot`, `Eyebrow`, `fitTitle`, the tenant's `tokens`, `grounds`,
   `accents`, `formats`). No `fetch`, no `require`, no dynamic import.
2. **Run in a V8 isolate** (`isolated-vm`): ~64MB memory, ~2s timeout, no filesystem,
   no network, no timers. Inside, `React.createElement` builds a plain element tree.
   The only output is that tree as JSON.
3. **Images are references, not URLs.** Code writes `<Photo id="p_123" />` and
   `<Logo variant="white" />`. The host resolves ids against the photos Kit sent; an
   unknown id is a validation error. Source never sees bytes or URLs.
4. **Validate the tree** (host side, before Satori):
   - element types only `div`, `span`, `p`, `h1`–`h3`, `img` (from Photo/Logo); **no
     `svg`, `path`, or other drawing elements**
   - every color value is one of the tenant's tokens; no gradients, shadows, opacity
   - every `fontFamily` is one of the tenant's families
   - accent rules from the tenant brand (Gravity: exactly one amber element, never
     amber with ember)
   - every text node containing a digit is set in the mono family
   - copy rules (port of `check.mjs`: banned words, dashes, exclamation marks, caps)
5. **Render** with Satori, then run the **safe-area check** on the node boxes.

Every failure is a plain-language problem string. The agent gets them back and retries;
the user sees them if it gives up.

### Templates: a library that evolves

A **template** is TSX that takes content and draws a poster. The prototype's seven
layouts become the first seven templates, but the point is that the library grows: a
good edited poster gets saved back as a template, an uploaded reference image becomes a
new one, and templates nobody picks get retired. Over time each tenant ends up with a
look of its own.

**Contract.** Every template default-exports one component:

```tsx
export const meta = {
  name: "Band",
  description: "Headline on top, full-width photo band, details underneath",
  photos: { min: 1, max: 1 },     // 0 for type-only; strip wants 3
  needs: ["eyebrow", "title"],    // content fields it can't do without
};
export default function Template({ content, format, photos }: TemplateProps) { … }
```

`content` is the copy (eyebrow, title, summary, details, action), `format` the canvas
with safe margins, `photos` the photo ids chosen for it. Templates import only from
`@poster/kit`, like posters. They must work on every canvas format (tall and wide);
validation renders each one at portrait, story and screen with sample content before it
can be activated.

**Lifecycle.** `draft` → `active` → `archived`. Only active templates feed the option
generator. Kit ships with the prototype's seven as built-ins; new ones come from the
sources below, and template authoring from a harness over MCP (with a stronger model) is
the expected way to make the bigger ones. Each save is a new template version, so a bad change can be rolled back.
Built-in templates ship with Kit and can be hidden per tenant but not edited in place;
"duplicate and edit" makes a tenant copy.

**Where new templates come from:**

1. **From a poster.** On the poster page, **Save as template**. The agent turns the
   poster's source back into a template: inlined copy becomes `content` fields, chosen
   photos become `photos` slots, one-off tweaks stay as layout. It renders the result
   with two other events' content to show it generalises, then saves it as a draft.
2. **From an image.** In template chat (or poster chat), the user uploads a reference
   image, a poster they like, through the chat component's existing image upload.
   Sonnet vision describes its structure (grid, type scale, where the photo sits, how
   much empty space), and the agent writes a new template that reproduces the
   **structure** in the tenant's own tokens, fonts and logos. The image itself is never
   placed in a poster or stored in the photo library (that would bypass the "real photo
   from our library" rule), and the agent must not copy another brand's artwork, logo or
   text. The reference is kept on the template as `reference_attachment_id` for context,
   visible to anyone who can see the template.
3. **From chat.** "Make me a template with a huge date down the left side" writes one
   from scratch.
4. **Edited directly.** Template chat edits a template the same way poster chat edits
   a poster (same sandbox, same validation, new version per change).

**Evolving by use.** Record which template every picked option came from, and what
the user changed afterwards. The option generator weights active templates by how
often they're picked (with a floor so new ones still get shown). The templates page
shows pick rate and "usually edited to…" summaries, so an admin can see which ones to
fix or retire. No auto-editing of templates; changes are always a human decision.

**Templates page** `/posters/templates` (console):

- **Grid** of every template rendered with real content: the tenant's next upcoming
  event, switchable to any event. Status badge, origin (built-in, from poster, from
  image, chat), pick count, last used.
- **Filters:** active / draft / archived, needs photo or not, origin.
- **Template detail:** preview across formats side by side, the reference image if it
  has one, version history with diffs, and a chat panel scoped to the template. Actions:
  activate, archive, duplicate, rename, roll back to a version, "try on event…".
- **New template** button: opens template chat with an empty draft, upload a
  reference image or describe one.

### Brand: derived from the branding-guide skill

The tenant's `branding-guide` skill is the **only** source of truth for the visual
system. There is no brand form. Kit derives a brand object from the skill
automatically and caches it by the skill's content hash, so editing the skill changes
every poster rendered after it.

- **Deriving it.** If the skill has a fenced `json` block with tokens (Gravity's
  section 9 does: `color`, `approved_pairs`, `type`, `logo`, `canvas`, `constraints`),
  parse that, no model call. Otherwise one Sonnet call extracts the same shape from the
  prose, once per skill version. Validate the result against a schema; anything missing
  is reported, not guessed.
- **What it yields:** color tokens; which tokens are grounds; the approved
  foreground/background pairs (the validator rejects any other text pairing); accent
  rules (Gravity: one amber element, never with ember); the three type roles with family
  and weight; logo variant per ground; canvas sizes and safe margins; prohibitions
  (gradients, shadows, pure white/black).
- **Fonts** come from Google Fonts: exactly the families and weights the guide names,
  nothing more. The
  renderer fetches static per-weight TTFs (Satori can't read variable fonts or WOFF2)
  and caches them on disk. A family Google doesn't serve is an error on the admin page;
  a font-upload tool can come later if a tenant needs one.
- **The admin page shows the derived brand read-only**: swatches, pairings, type
  specimens and canvases, with "derived from branding-guide (updated …)" and any
  problems. To change it, edit the skill.

**For Gravity** this means `paper` and `ink` backgrounds only: the guide's checklist
says so and lists forest for panels and rules. The prototype also used `forest` as a
full background; that goes away, as decided.

### Admin page

`/admin/posters` (pattern: `EventsSettings.tsx`), stored in `app_poster_settings`:

- **Photo folder** and **logo folder**: paste a Google Drive link for each. The page
  checks it can list the folder and shows what it found.
- **Logos:** the logo folder's files as thumbnails, each mapped to a variant the guide
  names (Gravity: `color`, `white`, `black`, `forest`). Files named after the variant
  (`gravity-white.png`) map themselves; others are mapped by hand, since names vary
  (Gravity's are `Gravity-Logo1-03.png` and `gravity_logo_white_transparentbg.png`).
  Only the mapping is stored; the renderer fetches, trims padding and caches the files.
- **Stock photos:** `allow_stock_photos` (default **off**). Controls whether the agent
  gets the Pixabay tool and what its prompt says about stock. No approval step.
- **Derived brand** (read-only, above) and **photo index status** ("12 waiting for
  descriptions").

The `writing-copy` skill is the source of truth for voice; the agent loads it for every
copy change.

### Photo library

**Kit stores an index, not images.** Drive is where the photos live. Kit keeps one
row per photo so it can search and choose without downloading anything:

`app_poster_photos`: `id, tenant_id, drive_file_id, drive_modified_at, folder,
filename, width, height, orientation, status (pending|indexed|removed), description,
tags[], focus_x, focus_y, notes, c2pa (none|camera|ai|unknown), indexed_by, indexed_at`,
with a GIN full-text index over description, tags, folder and notes (the same
`to_tsvector` approach Kit uses for skills and memories; Kit has no embeddings).

Indexing is split so that **Kit never spends its own model on it**:

- **Sync (Kit, no LLM).** Listing the folder tree happens on "sync now", on the
  `sync_poster_photos` tool, or hourly from a scheduled task (function lane) that is
  off by default and switched on per workspace with the auto-sync setting. New or changed files get a `pending` row with size,
  orientation and C2PA verdict from `/inspect`. Files gone from Drive become `removed`
  (kept, so old poster versions can say why they can't re-render).
- **Describe (Claude Code, over MCP).** Someone runs Claude Code against Kit's MCP
  server and asks it to index new photos. Claude Code's model looks at the photos and
  writes description, tags, focus point and notes back through the tools below. This is
  the prototype's `make describe` loop, moved behind MCP.
- **Pending photos are not offered** to the option generator or `search_photos` until
  indexed. The admin page shows "12 photos waiting for descriptions" with the prompt to
  paste into Claude Code.

**Indexing MCP tools** (MCP only; not registered for Kit's in-app agent, an intended
exception to agent/MCP parity, because the point is to use the caller's model):

| Tool | Does |
|---|---|
| `list_pending_photos(limit?)` | Pending photos: id, folder, filename, orientation |
| `get_photo_sheet(ids[])` | One contact-sheet image (MCP image content) of up to 12 numbered thumbnails with their ids, rendered by the renderer. Cheap way to look at many at once |
| `get_photo(id, size?)` | One photo as image content (default 1024px) for placing the focus point precisely |
| `index_photos(entries[])` | Batch write `{id, description, tags[], focus_x, focus_y, notes}`; marks them `indexed`, records `indexed_by` |
| `update_photo_index(id, …)` | Correct an indexed photo later |

A builtin skill, `indexing-poster-photos`, carries the description rules so every
indexer writes the same way: describe the subject literally; note light (warm, low,
daylight), where there's clean space for type, faces (and whether they're prominent),
duplicates and blur; tags from a small fixed vocabulary plus subject words; focus point
on the subject, not the frame's center. The tool descriptions point at the skill.
- **Drive access:** the admin pastes a link, so the folder must be shared "anyone with
  the link: viewer". Listing and download use a Kit-wide Google API key
  (`GDRIVE_API_KEY`, restricted to the Drive API), as the prototype's
  `scripts/sync-photos.mjs` does. If a tenant can't share publicly, the alternative is
  sharing with a service account, the pattern Calendar already uses.
- **Library UI** (on the admin page): thumbnails straight from Drive's `thumbnailLink`,
  filterable by status, with each photo's description, tags and focus point editable by
  hand, since they drive photo choice.
- **C2PA:** a photo whose manifest says AI-generated is indexed but never offered.
- The folder is the **set**: the option generator borrows siblings only from the hero's
  folder, never from the root.

### Generating options

From the event drawer, **Generate poster** (shown once the event has an id; a new event
saves as draft first). The same flow backs "New options" on the poster page.

1. **Copy.** Sonnet, with `branding-guide` and `writing-copy` loaded, turns the event's
   public fields into content JSON: `eyebrow, title, summary, details[], action`.
   Rules: only public fields, never `prep_notes`, leave out what isn't known (no
   invented price or booking method), repeat cadence as the event repeats ("Every
   Monday"). Validate with the schema and the copy rules; on failure, one retry with the
   errors.
2. **Photo.** Full-text search over indexed photos with the event title, description
   and labels, top 5 to Sonnet with descriptions to pick a hero and focus point, or none
   (`type`/`when` layouts only). Never a photo of something else standing in. The
   prototype's Tsar Bomba case: the only honest match was a barrel photo; a golden
   lager pint would have misrepresented an 11% stout.
3. **Options.** `/options` with `count: 7` and the tenant's **active templates**.
   Deterministic spread of template × ground × photo, weighted by pick rate (see
   Templates), templates whose `photos.min` can't be met skipped, unsafe renders
   skipped. Store each as a version in one batch, recording its `template_id`.
4. **Progress** over SSE (copy → photo → rendering 3/7). Target under 20s end to end.

### Two UIs

**1. The events drawer: a simple picker.** In the poster section of `EventDrawer`:

- **Generate poster** → a grid of 7 portrait options (from the render cache) → click
  one → it becomes the event's poster (portrait PNG to `hero_attachment_id`). No chat
  here.
- **More options** for another batch, avoiding templates already shown.
- **Open in Posters** for anything more.
- When the event's facts change after a poster is set, an **Out of date** banner with
  **Update poster** (see below).

**2. The Posters tab: the full app.** A console nav entry, `/posters`, with sub-tabs:

- **Posters:** every poster, grouped by event, with status (on event, draft, out of
  date) and thumbnail. One poster per event, and for a repeating event one per series,
  exactly as event posters work today.
- **Poster page** `/posters/:id`:
  - large preview, a **version strip** (undo, redo, branch from here)
  - a **chat panel** (shared widget, `web/shared/src/chat/`) for any change, copy
    included: "fix the typo in the title", "move the photo left", "make it feel like
    autumn". There's no copy form; a model call per fix is fine.
  - **Set on event** (portrait), **New options**, **Save as template**
  - **Formats:** render the current version at any canvas the brand guide defines
    (Instagram feed and portrait, story, Facebook cover, screen, website hero) and
    download them. Only portrait goes on the event for now.
- **Templates** (see Templates).
- **Photos:** the photo index, filterable by status, descriptions and focus points
  editable.

**Chat scope.** Today object chat requires a `CardProvider`. Add a generic scope to
`chat.Execute` (app, kind, id) rather than making posters cards. Endpoint
`POST /{slug}/api/posters/{id}/chat/execute`, restricted with `WidgetAllowedTools` to
the poster tools below. Kit's agent always uses **Sonnet**. Work that needs a stronger
model (new templates, big redesigns) is done from Claude Code or another harness over
MCP, with whatever model that harness runs.

### Out-of-date posters

When a poster is set on an event, store a snapshot of the facts its copy used (title,
dates, times, price, location, booking) with a hash. On every event update, compare; if
they differ, mark the poster `stale`. **Update poster** asks Sonnet to change only the
affected facts in the poster's source, renders a new version, and shows it next to the
current one. Nothing replaces the event's poster until someone clicks Set on event.

### Poster agent tools

Agent and MCP in parity (`tools.go` ToolMetas, one `dispatchCore`), so everything the
in-app chat can do, a harness can do over MCP with its own model. Tools that return a
render return it as MCP image content, so the harness's model can look at it.

| Tool | Does | Gate |
|---|---|---|
| `get_poster` | Current version's source, content, format, last problems | — |
| `edit_poster_source(source, summary)` | Render + validate. On success a new version; returns the PNG (attached for the agent to look at) and problems | — |
| `set_poster_fields(layout?, ground?, photo?, zoom?, focus?)` | Quick edits without rewriting code | — |
| `render_poster_formats(formats[])` | Render other canvases of the current version | — |
| `search_photos(query)` | Full-text search over indexed photos, with descriptions | — |
| `find_stock_photos(query)` | Pixabay search (below). Only registered when `allow_stock_photos`; results usable directly as `<Photo pixabay="…"/>` | — |
| `ask_for_photo(what)` | Ask the user to add one to the Drive folder (or upload in chat for this poster only), then re-index | — |
| `load_skill` | `branding-guide`, `writing-copy` | — |
| `list_templates(status?)` | Templates with meta, origin, pick stats | — |
| `get_template(id)` | Template source and versions | — |
| `edit_template_source(id, source, summary)` | Validate on portrait, story and screen with sample content; new template version | — |
| `create_template(source, name, origin, reference_attachment_id?)` | New **draft** template (from poster, from image, from chat) | — |
| `set_template_status(id, status)` | Activate or archive. MCP only: someone driving a harness decides; Kit's in-app agent makes drafts and a person clicks Activate on the templates page | — |
| `update_poster_facts(id)` | The out-of-date flow: refresh facts in the source from the event | — |

The agent **must look at its render** before replying (the PNG comes back from
`edit_poster_source`) and check it against the brand checklist. The prototype's
review step caught crop and break problems no validator did.

**Worked example: "add a monkey in the middle".**
`search_photos("monkey")` → nothing. If `allow_stock_photos` is off: reply that the
library has no monkey photo, offer `ask_for_photo`, and change nothing. If on:
`find_stock_photos("monkey")` → the agent picks one (screening for logos, trademarks
and recognisable people) → `edit_poster_source` places `<Photo pixabay="…"/>` →
validate, render, look, reply, naming Pixabay as the source. An SVG
monkey fails validation regardless of how the code is written.

### Pixabay

- `GET https://pixabay.com/api/?key=…&q=…&image_type=photo&safesearch=true&per_page=20`.
  Kit-wide key in env (`PIXABAY_API_KEY`).
- **No approval step and no import.** Whether stock is allowed is the tenant setting;
  the agent decides which result to use. A poster's source refers to the Pixabay id;
  Kit looks up a current download URL (from the 24h cache, else the API) when rendering.
- **Terms that shape the code:** cache API responses 24h (Redis); say where images come
  from when showing them (the agent names Pixabay in its reply); **no hotlinking**, so
  the renderer downloads rather than linking; rate limit 100 requests/60s per key
  (honor `X-RateLimit-*`).
- **Size:** without approved full API access the largest is `largeImageURL` (1280px).
  Fine for 1080 canvases, soft on 2400px heroes. Request full access before relying on
  it for large formats.
- **License:** Pixabay Content License, commercial use, no attribution required. You
  may not use images with recognisable **trademarks or logos** commercially, and must
  not use recognisable people in misleading ways. The agent prompt must screen for both.
- **AI-generated stock:** the API has **no field or filter for AI content**. Defences:
  `image_type=photo` and the renderer's C2PA check on download (a flagged image fails
  validation). Neither catches everything, so the settings page says plainly that stock
  images can't be guaranteed camera-made.

### Data model (one migration)

- `app_poster_settings` (tenant_id PK): brand JSON, font attachment ids, logo variant →
  Drive file id map, photo and logo folder ids, `allow_stock_photos`.
- `app_poster_photos`: the index above. No image bytes.
- `app_poster_templates`: `id, tenant_id NULL (NULL = built-in), name, description,
  status (draft|active|archived), origin (builtin|poster|image|chat), meta JSONB,
  current_version_id, parent_template_id NULL, source_poster_id NULL,
  reference_attachment_id NULL, created_by, created_at`. `reference_attachment_id` is
  the chat upload that inspired it (already stored by chat). Built-ins hidden per tenant
  via `app_poster_template_hidden (tenant_id, template_id)`.
- `app_poster_template_versions`: `id, tenant_id, template_id, parent_id, source TEXT,
  summary, author (user|agent), created_at`.
- `app_posters`: `id, tenant_id, event_id NULL, title, current_version_id,
  created_by, created_at`.
- `app_poster_versions`: `id, tenant_id, poster_id, parent_id NULL, batch_id NULL,
  template_id, template_version_id, source TEXT, content JSONB, ground, format,
  problems JSONB, instruction TEXT, author (user|agent), picked BOOL, created_at`.
  Options are versions sharing a `batch_id` with no parent; `picked` and the edits that
  follow feed template stats.

**What's stored as images:** only the poster attached to an event, through the existing
events path (`hero_attachment_id`). Options and edit renders live in a render cache
(Redis or renderer disk, 24h) keyed by version id + format, and re-render from source on
a miss. Unpicked option versions are pruned after 30 days; picked ones and their edit
history stay, since they're text and they're what Save as template and stats need.

## Phases

1. **Renderer service.** Port the prototype with brand as input: `/render`, `/options`,
   `/inspect`, Google Fonts fetching, the sandbox and validator. Golden-image tests using
   the prototype's `content/*.json`. Deploy as its own Dokku app.
2. **Brand, settings, index.** Brand derivation from the `branding-guide` skill, admin
   page (Drive links, logo mapping, stock setting, derived brand view), Drive sync task,
   indexing MCP tools and skill, full-text search. Seed Gravity's descriptions and focus
   points from the marketing repo's `library/catalog.json` (match by Drive file id,
   which the catalog already records).
3. **Simple picker in the events drawer.** The seven built-in templates, copy, photo
   pick, 7 portrait options, set on event, pick tracking, out-of-date flag and Update
   poster. Stop here and use it for a few real events.
4. **Posters tab.** Poster list, poster page with versions and formats (downloads),
   generic chat scope and the sandboxed TSX edit loop, all poster tools over MCP.
5. **Templates.** Templates sub-tab, template detail and chat, save poster as template,
   template from an uploaded image, activate/archive, pick-rate weighting, template
   tools over MCP.
6. **Stock photos.** Pixabay behind `allow_stock_photos`.

## Decisions (2026-10-10)

1. **Access:** anyone in the tenant can generate, edit and manage templates; the admin
   page is admin-only.
2. **Brand:** derived automatically from the `branding-guide` skill, which is the only
   source of truth. No brand form; fonts from Google Fonts. A tool to override can come
   later if needed.
3. **Event poster:** portrait only, set from the drawer's simple picker or the Posters
   tab. Other formats are generated and downloaded from the Posters tab.
4. **Series:** one poster per event, one per series for repeating events, as today.
5. **Stale posters:** flagged when the event's facts change, with Update poster.
6. **Copy fixes:** through chat; a model call is fine. No copy form.
7. **Templates:** start with the seven built-ins. Managed in the Posters tab and over
   MCP, so templates can be authored from a local harness.
8. **Reference images:** visible to all template viewers.
9. **Cost:** no usage tracking. Nothing here should be expensive: Kit makes a few
   Sonnet calls per poster, and anything heavier runs on the caller's model over MCP.
10. **Models:** Kit uses Sonnet only. Opus, Fable or anything else is used from a
    harness over MCP, never by Kit.
11. Photos aren't stored (index only, Drive is the store); option and edit renders are
    temporary; only the poster set on an event is kept as an image; Pixabay has no
    approval step; photo descriptions are written over MCP; Remotion isn't used.

12. **Grounds follow the guide.** For Gravity that means paper and ink backgrounds,
    with forest only for panels and rules, as the guide says. No exception for the
    prototype's forest posters.
13. **Fonts:** just the families and weights the guide names (Gravity: Archivo Narrow
    700, Archivo 400 and 600, IBM Plex Mono 500), fetched from Google Fonts. Nothing
    beyond that until a tenant needs it.
