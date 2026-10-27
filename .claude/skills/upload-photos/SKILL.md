---
name: upload-photos
description: Add Don's darktable exports (~/Pictures/darktable_exported/<set>/) to Kit's poster photo library for Gravity — find sets not yet in Drive, ask which to include, look at them, ask what beer/event they show, upload to the Drive Photos folder with gws, then sync and index in Kit. Use when asked to "add the new images/photos to kit", "upload the darktable exports", or similar.
---

# Upload darktable exports to Kit's photo library

Kit's poster photos are an index over a shared Google Drive folder
("Photos", id `1lP9MBD6nQILxlxrvtT4rDDlnV02Cq6fq`). Each subfolder is a set,
and its slug becomes the photo's `folder` (`2026-10-26 Magic night` →
`2026-10-26-magic-night`). Kit never sees the Mac's disk; a photo exists for
Kit once it is in Drive and `sync_poster_photos` has run.

## 1. Discover, then ask which sets to include

Exports land in `~/Pictures/darktable_exported/`, usually one dated folder
per shoot (`beer-10-26`), but not everything there is meant for Kit: a set
may be a one-off export for something else, and loose files at the top level
are usually one-offs. So discover first, and never upload without asking.

```bash
ls -lt ~/Pictures/darktable_exported | head -20
gws drive files list --params '{"q":"'"'"'1lP9MBD6nQILxlxrvtT4rDDlnV02Cq6fq'"'"' in parents and trashed=false","fields":"files(name)","pageSize":100}'
```

Uploaded files are renamed, so match on dates, not filenames: a set whose
date has no matching Drive subfolder (or no `search_photos` hit on its
date) is a candidate. Then one `AskUserQuestion` with `multiSelect: true`
listing the candidates newest first, each with its photo count and date
(e.g. "beer-10-26 — 4 photos, Oct 26"). Group loose top-level files into one
option per export date. Only the sets picked go on to the next steps; if none
are picked, stop.

## 2. Look before asking

Exports are 30+ MB PNGs; make thumbnails in the scratchpad and Read them:

```bash
sips -Z 800 -s format jpeg <src.png> --out <scratchpad>/thumbs/<set>-<name>.jpg
```

## 3. Ask (one AskUserQuestion, up to 4 questions)

Ask only what the pixels can't tell you, and offer your best guesses from
the current tap list (`get_menu_board`) as options:

- **Which beer** is in each glass? Colour is a poor guide (Little Dipper, a
  dark mild, has photographed amber outdoors and near-black indoors).
- **What the set is for**: an event (Magic night, trivia, vinyl night) or
  general/seasonal stock. This sets the Drive folder name, which posters
  search on. Mixed sets can go in one folder; mark the general-purpose shots
  in tags/notes rather than splitting hairs.
- Anything else in frame you can't name (a band, a guest), never guess.

## 4. Upload

```bash
.claude/skills/upload-photos/upload.sh "<YYYY-MM-DD Set name>" \
  <src.png>:<BeerName_Subject.jpg> ...
```

Name files for what is in them (`LittleDipper_Pumpkins_MagicCards.jpg`); the
indexer and later choosers read filenames. The script converts to full-size
JPEG (q90), creates the subfolder if needed, and skips names already there.

## 5. Sync and index in Kit (kit-gravity MCP)

1. `sync_poster_photos`
2. `load_skill indexing-poster-photos` and follow it
3. `list_pending_photos`, then `index_photos`: put the beer name and the
   set's purpose in the description and tags. Note date-sensitive content
   (tap list, prices) as going stale.

Report the Drive folder(s) and how many photos were indexed.
