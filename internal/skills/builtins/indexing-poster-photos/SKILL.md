---
name: indexing-poster-photos
description: "How to describe photos in the poster library so every indexer writes the same way: literal subjects, light, clean space for type, faces, duplicates, a small fixed tag vocabulary, and a focus point on the subject. Load before list_pending_photos / index_photos."
---

# Indexing poster photos

Kit keeps an index of the photo library, not the photos. The description you write is what chooses a photo for a poster later, by full-text search and by a model reading the top hits. Write for that reader: literal, specific, and honest about flaws.

## The loop

1. `sync_poster_photos` once, so new Drive files are in the index. Kit does not poll Drive on its own unless an admin turned on auto-sync.
2. `list_pending_photos` for a batch (up to 48).
3. `get_photo_sheet` with up to 12 ids at a time: one numbered contact sheet. Use `get_photo` on a single id when you need to place a focus point precisely or check a detail.
4. `index_photos` with one entry per photo. Repeat until nothing is pending.
5. `update_photo_index` later to correct one.

Do not skip a photo because it is poor. Index it and say why in `notes`; the chooser needs to know what to avoid.

## Description (one or two sentences)

- **Say what is literally there.** "Two stemmed glasses of amber beer on a wooden bar, taps blurred behind, warm overhead light." Not "a cozy evening" or "the perfect pint".
- **Name the subject first**, then setting, then light. If the subject is a thing on tap or a named event (a band, a trivia host, a book signing), say so if the folder tells you; never guess a name.
- **Light**: warm or cool, low or bright, daylight or indoor, where it comes from.
- **Clean space for type**: say where there is plain, low-detail area a headline could sit on ("clean dark wall on the left third", "busy everywhere, no clean space").
- **People**: say how many and whether faces are prominent. A prominent face is a permission question for a public poster; note it.
- **Flaws**: blur, noise, tilted horizon, cut-off subjects, text in frame (menus, signs, phone screens), near-duplicates of another photo in the set ("near duplicate of dsc04320, this one is sharper").

## Tags

A small fixed vocabulary, plus a few subject words. Lowercase, hyphenated.

- Subject: `beer`, `pint`, `can`, `taps`, `barrel`, `brewhouse`, `tank`, `food`, `pizza`, `people`, `crowd`, `staff`, `kids`, `dog`, `music`, `band`, `trivia`, `vinyl`, `patio`, `taproom`, `bar`, `exterior`, `sign`, `logo`
- Framing: `wide`, `detail`, `portrait`, `landscape`, `overhead`
- Light: `warm-light`, `cool-light`, `daylight`, `low-light`, `backlit`
- Suitability: `clean-space` (room for type), `faces-visible`, `busy`, `blurry`, `duplicate`, `text-in-frame`

Add two or three subject words of your own when the vocabulary has none ("book-signing", "cask", "hops").

## Focus point

`focus_x` and `focus_y` are fractions of the frame, 0 to 1, left to right and top to bottom. Put the point **on the subject**: the glass, the face, the tap handle. Not the frame's centre unless the subject is there. For a wide shot of a room, pick the part that should survive a tight portrait crop.

## Notes

Caveats for the chooser, short: "faces prominent, check permission before ads", "near duplicate of dsc04319", "menu text readable in frame", "flash glare on glass". Empty when there is nothing to say.

## What never goes in

No opinions about quality beyond flaws, no marketing words, no guesses about who people are or what event it was unless the folder name says so.
