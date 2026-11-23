# Trivia OBS overlay — design notes (WIP)

Status: sketch on branch `trivia-obs-overlay`. Not wired into docs, not
tested against a live game, not deployed. Picking this up on another box.

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
- cards phases: countdown (if timed) + question; answer at scoring
- other timed phases (question, wager): big countdown alone
- untimed: TOPICS list with "N LEFT" / DONE

Bottom strip:
- lobby: "Scan the code to join" / N teams in
- board: "<Team> picks the next topic" / why
- question: question text, fitted
- wager: "Final category: X" / lock in your wager
- reveal/betting/scoring: the guess cards (pot under each when betting,
  winner gold + others dimmed at scoring)
- break/podium: headline + top scores

## Previews

`docs/trivia-obs-overlay/*.png` — rendered over a fake football broadcast
from fake state (betting with 9 and 21 cards, question, board, scoring).

## Open items seen in the previews

1. Betting cards mix pot ($200) and team name under the number depending on
   whether chips are down — pick one (leaning: pots only, blank if none).
2. Question phase wastes the strip — bigger text or 2 lines at ~90px.
3. Left column has dead space during betting/scoring/question — standings?
4. Countdown is only 96px when it shares the column with the question.
5. Verify the phone shows the question during betting.
6. Not done yet: tests (render + version stamp parity), user guide line,
   try it in real OBS, check against a live game.

## Code

- `internal/apps/trivia/render_overlay.go` — `RenderOverlay`
- `internal/apps/trivia/render.go` — shared `displayDataFor` / `renderPage`
- `internal/apps/trivia/web_pages.go` — `isOverlay(r)` picks the page
- `internal/apps/trivia/templates/overlay.html.tmpl`, `overlay.css`
- `internal/apps/trivia/templates/tv/overlay.js` — the renderer
- `internal/apps/trivia/templates/tv/core.js` — 3-line hook in `render()`

The overlay must pass the explainer link into the version stamp even though
it doesn't show the video, or it never matches `tv.version` and reloads
every 15s.

## Regenerating previews

Dump the page with a throwaway test calling `RenderOverlay`, inject a stub
`EventSource` that emits a fake `state` frame, put a fake "game" div behind
`#fit`, then:
`google-chrome --headless=new --window-size=1920,1080 --virtual-time-budget=1500 --screenshot=out.png file://page.html`
