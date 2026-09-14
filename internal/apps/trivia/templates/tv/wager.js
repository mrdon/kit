/* Screen 9: the blind bet.

   The one screen in the game that is deliberately NOT showing the question.
   The room is looking at a category, a clock and a row of pips, and the whole
   dramatic point is that nobody — not the wall, not a phone, not even the
   frame on the wire — has the prompt yet. If this screen ever renders
   state.round.text, the mechanic is gone. It does not exist in the frame to
   render: see publicRound in projection.go.

   The slam beat lives here now rather than on the final's question. It fires
   once, on the way IN to the wager, because that is the moment the night
   changes gear; by the time the question goes up the room has already had its
   announcement and a second slam would only delay the reveal. */
/* --- 9. wager --- */
function renderWager(phaseChanged) {
  var paint = function () {
    show('s-wager');
    var cat = document.getElementById('wager-cat');
    cat.textContent = state.round ? (state.round.category || '') : '';
    fitCategory(cat);
    renderLockStrip();
    startRing('wager');
  };
  if (phaseChanged) {
    show('s-final');
    later(1800, paint);
    return;
  }
  paint();
}

/* Measured, not clamped -- the same loop fitQuestion uses, and for the same
   reason: a category is one to four words and "Science & Nature" set at the
   size "Film" wants runs off both sides of a 1920 stage. */
function fitCategory(node) {
  var size = 200;
  node.style.fontSize = size + 'px';
  while ((node.scrollWidth > 1600 || node.scrollHeight > 320) && size > 72) {
    size -= 8;
    node.style.fontSize = size + 'px';
  }
}

/* The lock strip. Same furniture as the answered strip on the question screen
   -- one pip per eligible table, lighting as they commit -- and the same rule
   about what it may say: LOCK, never an amount. Not knowing whether the
   leader defended or sat out is most of the tension, and the amount is not on
   this frame anyway (publicTeams carries stakeLocked and stops). */
function renderLockStrip() {
  var strip = document.getElementById('wager-strip');
  strip.innerHTML = '';
  var eligible = state.teams.filter(function (t) { return t.eligible; });
  var locked = 0;
  eligible.forEach(function (t) {
    var bar = el('div', 'tbar' + (t.stakeLocked ? ' in locked' : ''));
    if (t.stakeLocked) { locked++; bar.appendChild(el('span', '', 'LOCK')); }
    bar.title = t.name;
    strip.appendChild(bar);
  });
  document.getElementById('wager-count').textContent =
    locked + ' OF ' + eligible.length + ' LOCKED';
}
