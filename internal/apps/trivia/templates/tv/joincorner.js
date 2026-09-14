/* The join corner: a way in, on every screen where joining still does
   something.

   The big QR on the lobby screen is seen by the people who were already there
   at eight o'clock. A bar fills up all night, and everybody who walks in after
   the first question sees a board, a question or a set of cards — screens that
   said nothing at all about how to join. So the corner rides along in the
   phases where a latecomer can actually act on it.

   Where it does NOT appear is as deliberate as where it does. The question and
   the betting belong to the clock: those two screens are a countdown with one
   thing to look at, and a QR competing with the ring is a QR nobody scans and
   a clock somebody misses. The lobby already has the code at 700px. The podium
   is over. And once the final is up the server refuses the join anyway, so
   showing the code there would be an invitation to a closed door. */

/* An ALLOW-list, not a deny-list, because a phase this file has never heard of
   must default to hiding the corner rather than painting it over whatever that
   screen turns out to be. `wager` is in here for the final's staking phase; if
   it never ships, the entry costs nothing. */
var JOIN_PHASES = { board: true, reveal: true, scoring: true, wager: true };

function joinCornerIsDue() {
  if (!state || !JOIN_PHASES[state.phase]) { return false; }
  // The door shuts at the final, server-side: a table that scanned this would
  // get a refusal and a screen telling it the game is closing.
  if (state.round && state.round.isFinal) { return false; }
  return true;
}

/* The corner is a sibling of the screens rather than a copy inside each one,
   so there is one QR in the DOM and one place that decides where it sits.

   It is laid over the stage, so every screen it appears on has to MAKE ROOM
   for it -- a QR with a board cell under it is a QR that does not scan, and
   the one thing this element has to do is scan. Hence the `cornered` classes:
   same trick the standings rail uses when it slides in and pushes the cards
   left. */
function renderJoinCorner() {
  var box = document.getElementById('joincorner');
  if (!box) { return; }
  var on = joinCornerIsDue();
  box.classList.toggle('on', on);

  // The board gets the corner at full size: it is the phase with the most
  // empty stage and the most time, so it is where a latecomer will actually
  // get their phone out, and a 220px code is one anybody can scan from a
  // table away.
  //
  // Every other phase gets the COMPACT form, which is short enough to sit
  // inside the cards screen's footer band. The first version put the full
  // corner beside the deck and took 320px off it, and at twenty-one cards
  // `fitCardValues` then had to shrink every numeral to fit the narrower box
  // -- a wall of three-digit numbers a size smaller, to make room for a code.
  // Wrong trade: the cards are what the room is looking at.
  var small = on && state.phase !== 'board';
  // Scoring is the one phase where the right-hand 540px already belongs to
  // the standings rail, so the corner crosses to the left.
  var left = on && state.phase === 'scoring';
  box.classList.toggle('small', small);
  box.classList.toggle('left', left);

  document.getElementById('s-board').classList.toggle('cornered', on && state.phase === 'board');
  var cards = document.getElementById('cards-screen');
  cards.classList.toggle('cornered', small && !left);
  cards.classList.toggle('cornered-left', left);
}

/* ---------- the arrivals toast ---------- */

/* Somebody scanned the corner and joined. That is worth four seconds of the
   wall, because it is the one thing that happens in this game with nobody
   watching: a table joins mid-question, its name appears in a list on a screen
   nobody is looking at, and the room never knows they arrived. A pill sliding
   in from the top says it out loud, and the new table sees its own name on the
   wall within seconds of typing it -- which is the whole welcome.

   The comparison is against the PREVIOUS frame, which core.js hands to
   render(). The first frame after a reload has no previous, so a TV that was
   restarted mid-game does not announce all twenty tables at once. */
var toastUntil = 0;

function noticeArrivals(prev) {
  var host = document.getElementById('jointoast');
  if (!host || !state) { return; }
  // The lobby is nothing but arrivals -- the pills there already say it, and a
  // toast per table would be a strobe.
  if (prev && state.phase !== 'lobby' && state.phase !== 'setup') {
    var had = {};
    (prev.teams || []).forEach(function (t) { had[t.id] = true; });
    var fresh = (state.teams || []).filter(function (t) { return !had[t.id]; });
    if (fresh.length) {
      host.textContent = arrivalLine(fresh);
      host.classList.add('on');
      toastUntil = Date.now() + 4000;
    }
  }
  armToast(host);
}

/* Re-armed on EVERY frame, not set once when the pill appears.
   `later` is the cancellable timer, and every frame clears the lot -- so a
   one-shot four-second timeout would be wiped by the next chip landing and the
   pill would stay on the wall for the rest of the night. Holding the deadline
   as an absolute and re-arming against it gets the cancel-safety without the
   stuck pill. */
function armToast(host) {
  if (!host.classList.contains('on')) { return; }
  var left = toastUntil - Date.now();
  if (left <= 0) { host.classList.remove('on'); return; }
  later(left, function () { host.classList.remove('on'); });
}

/* Names, up to two. Past that the pill would be a paragraph nobody reads from
   thirty feet, and "3 MORE TABLES JOINED" says the thing that matters. */
function arrivalLine(fresh) {
  if (fresh.length === 1) { return fresh[0].name + ' JOINED'; }
  if (fresh.length === 2) { return fresh[0].name + ' AND ' + fresh[1].name + ' JOINED'; }
  return fresh.length + ' MORE TABLES JOINED';
}
