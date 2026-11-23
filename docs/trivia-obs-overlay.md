# Trivia OBS overlay — design notes

Status: shipped behind `?overlay=1`, advertised on the console's Trivia,
setup and live pages (`overlay_url`) and in the user guide. Not yet tried in
real OBS against a live game.

## Goal

Run trivia while the bar still watches the game. A laptop runs OBS: the
broadcast underneath, and the trivia TV page on top as a browser source with
a transparent hole for the game.

## Decisions so far

- **Trigger:** `?overlay=1` on the existing TV addresses
  (`/{slug}/trivia/tv` and `/{slug}/trivia/{game}/tv`). Same stream, poll,
  watchdog and version-reload as the wall.
- **Shape:** transparent hole at the top right, **1440x810** (exactly 75%
  of 1080p, same 16:9) — scale the game to 75% in OBS, place at x=480 y=0,
  nothing cropped. Left column 480 wide (810 tall); bottom strip **full
  width**, 270 tall (the bottom-left corner belongs to the strip — more
  room for cards).
- **Don't replicate the wall.** Only the critical stuff: QR, current
  question, the guesses to bet on, list of topics. No wheel, board grid,
  dealt-card animation, podium, awards, explainer video, clock.
- **No tag line** (topic/points). People only need the numbers to bet on.
  PRACTICE goes under the title in the left column; PAUSED replaces the
  countdown numeral; the answer at scoring goes in the left column.
- **Question is not repeated above the cards.** During reveal/betting/
  scoring it moves to the left column under the timer.
- **Card numbers use Exo 2 bold, not Bungee** (Bungee ~1.7x wider).
  One row up to 12 cards, two rows past that. No short forms like "78K" —
  closest-without-going-over needs exact values.

## Layout by phase

Left column (under title + QR + join URL):
- cards phases: countdown (if timed) + question; answer at scoring; then
  STANDINGS in whatever room is left (rows dropped from the bottom, hidden
  under 2 rows), with this round's +/- at scoring
- other timed phases (question, wager): big countdown alone
- board: TOPICS list with "N LEFT" / DONE
- lobby: TEAMS IN (names only)
- break/podium: STANDINGS

Bottom strip:
- lobby: "Scan the code to join" / N teams in
- board: "<Team> picks the next topic" / why
- question: question text, fitted
- wager: "Final category: X" / lock in your wager
- reveal/betting/scoring: the guess cards. Under the number: who wrote it
  at reveal, the pot from betting on (blank when empty, never a mix);
  winner gold + others dimmed at scoring
- break: "Break, back soon" / who leads
- podium: "<Team> wins with $X" / thanks for playing

## Previews

`node docs/trivia-obs-overlay/preview.mjs` renders every phase from fake
state over a fake football broadcast into `dist/trivia-obs-overlay/`, plus
`sheet.png` tiling them all.

## Open items seen in the previews

1. ~~Betting cards mix pot and team name~~ — pots only, blank if none.
2. Question phase wastes the strip — bigger text or 2 lines at ~90px.
3. ~~Left column dead space~~ — standings fill it (not during the question
   phase, where the countdown stays solo and big).
4. Countdown is only 96px when it shares the column with the question.
5. ~~Phone shows the question during betting?~~ — yes, `play/betting.tsx`.
6. Try it in real OBS against a live game.

## Code

- `internal/apps/trivia/render_overlay.go` — `RenderOverlay`
- `internal/apps/trivia/render.go` — shared `displayDataFor` / `renderPage`
- `internal/apps/trivia/web_pages.go` — `isOverlay(r)` picks the page, and
  a transparent placeholder before the first game
- `internal/apps/trivia/web_overlay_test.go` — page, stamp parity, placeholder
- `internal/apps/trivia/templates/overlay.html.tmpl`, `overlay.css`
- `internal/apps/trivia/templates/tv/overlay.js` — the renderer
- `internal/apps/trivia/templates/tv/core.js` — 3-line hook in `render()`

The overlay must pass the explainer link into the version stamp even though
it doesn't show the video, or it never matches `tv.version` and reloads
every 15s.

## Regenerating previews

`node docs/trivia-obs-overlay/preview.mjs [outdir] [phase...]` (needs
google-chrome and ImageMagick). `TestDumpOverlayPreview` writes the page
when `TRIVIA_OVERLAY_DUMP` is set; the script stubs `EventSource` with a
fake `state` frame per phase and screenshots it. Headless Chrome's viewport
is 87px shorter than its window, so it shoots at 1920x1167 and crops.
