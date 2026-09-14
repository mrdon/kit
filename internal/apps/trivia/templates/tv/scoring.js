/* Screen 6: the scoring beat and the standings rail. */
/* --- 6. scoring: the TV owns the choreography --- */
function renderScoring(phaseChanged) {
  renderCards('scored');
  if (!phaseChanged) { paintScored(); renderRail(false); return; }

  var host = document.getElementById('cards');
  later(800, function () {
    host.classList.add('dim');
    paintScored();
  });
  later(2200, function () { renderRail(true); });
}

function paintScored() {
  if (!state.scoring) { return; }
  var win = state.scoring.winningSlot;
  var winner = null;
  var cards = document.querySelectorAll('#cards .card');
  for (var i = 0; i < cards.length; i++) {
    var isWin = cards[i].dataset.slotId === win;
    cards[i].classList.toggle('win', isWin);
    if (isWin) { winner = cards[i]; }
    if (!isWin) {
      var chips = cards[i].querySelectorAll('.chip');
      for (var j = 0; j < chips.length; j++) { chips[j].classList.add('falling'); }
    }
  }
  document.getElementById('cards').classList.add('dim');
  paintBand();
  // The card is already gold and scaled; the sparkle is what makes the room
  // look at it rather than at the band sliding in underneath.
  if (winner) { sparkleOver(winner); }
}

/* The band says the number and then who wrote it. Two nodes rather than one
   textContent, because the credit sets smaller than the answer and a single
   string cannot be two sizes. */
function paintBand() {
  var band = document.getElementById('answer-band');
  band.innerHTML = '';
  band.appendChild(el('div', 'band-answer', state.scoring.correctText || String(state.scoring.correctValue)));
  band.appendChild(el('div', 'band-who', winnerCredit()));
  band.classList.add('shown');
  // Force a reflow so the slide-in runs from off-stage rather than being
  // collapsed into the same frame as the display change.
  void band.offsetWidth;
  band.classList.add('in');
}

/* Who wrote the winning answer.

   The pseudo-slot ("smaller than all of these") has no teams by definition --
   nobody wrote it, the room simply all went high -- and that is worth saying
   out loud rather than leaving a blank line where a name goes. The empty
   `teams` case is the same sentence for the same reason. */
function winnerCredit() {
  var win = state.scoring.winningSlot;
  var slots = state.slots || [];
  for (var i = 0; i < slots.length; i++) {
    if (slots[i].id !== win) { continue; }
    var teams = slots[i].teams || [];
    if (!teams.length) { break; }
    return teams.join(' & ') + ' wrote it';
  }
  return 'Nobody had it';
}

/* Rows reorder by FLIP so an overtake is visible AS MOTION rather than as
   a list that is suddenly different. */
function renderRail(animate) {
  var rail = document.getElementById('rail-rows');
  var before = {};
  var existing = rail.querySelectorAll('.row');
  for (var i = 0; i < existing.length; i++) {
    before[existing[i].dataset.teamId] = existing[i].getBoundingClientRect().top;
  }
  var teams = state.teams.slice().sort(function (a, b) { return b.score - a.score; });
  var best = bestSwing();
  rail.innerHTML = '';
  teams.forEach(function (t) {
    var row = el('div', 'row');
    row.dataset.teamId = t.id;
    row.appendChild(el('div', 'name', t.name));
    var d = state.scoring && state.scoring.deltas ? state.scoring.deltas[t.id] : null;
    row.appendChild(el('div', 'delta', d ? ((d > 0 ? '+' : '') + money(d)) : ''));
    row.appendChild(el('div', 'score', money(t.score)));
    // Only on the choreographed pass. The rail is rebuilt on every frame that
    // lands during scoring, and a flash that re-fires each time would be a
    // strobe rather than a callout.
    if (animate && best > 0 && d === best) { row.classList.add('mover'); }
    rail.appendChild(row);
  });
  // Before the FLIP reads the new rects, or every row is measured at a size
  // it is about to stop being.
  fitRail();
  document.getElementById('rail').classList.add('in');
  document.getElementById('cards-screen').classList.add('railed');
  // The rail takes 540px off the cards row, so every value that was sized
  // against the full stage is now too big for its card. Re-fit once the
  // padding transition has landed -- measuring mid-transition would size
  // them against a width that is still moving.
  later(600, fitCardValues);

  if (!animate) { return; }
  var rows = rail.querySelectorAll('.row');
  for (var k = 0; k < rows.length; k++) {
    var id = rows[k].dataset.teamId;
    if (before[id] === undefined) { continue; }
    var dy = before[id] - rows[k].getBoundingClientRect().top;
    if (!dy) { continue; }
    rows[k].style.transition = 'none';
    rows[k].style.transform = 'translateY(' + dy + 'px)';
    void rows[k].offsetWidth;
    rows[k].style.transition = '';
    rows[k].style.transform = '';
  }
}

/* The biggest positive swing of the round, or 0 if nobody gained.

   Ties flash together, deliberately: two tables that both took $300 both had
   the round of it, and picking one of them by map order would be arbitrary in
   a way the room can check against the rail it is reading. */
function bestSwing() {
  var deltas = state.scoring && state.scoring.deltas;
  if (!deltas) { return 0; }
  var best = 0;
  Object.keys(deltas).forEach(function (id) {
    if (deltas[id] > best) { best = deltas[id]; }
  });
  return best;
}

/* Twenty tables is what the game advertises, and twenty rows at the
   five-table size are half again taller than the stage -- the tables at the
   bottom of the standings, which are the ones most likely to be watching
   the standings, simply were not on the screen. Measure and shrink, the
   same loop as fitJoinRules: the row size and its padding move together so
   the list stays a list rather than becoming a stack of thin stripes, and a
   small room never enters the loop at all. */
function fitRail() {
  var rail = document.getElementById('rail');
  var rows = document.getElementById('rail-rows');
  if (!rail || !rows || !rows.childElementCount) { return; }
  // The rail is absolutely positioned, so it is the rows' offsetParent and
  // offsetTop already carries the padding and the heading above them. The
  // 48 is the rail's own bottom padding, which nothing else accounts for.
  var avail = rail.offsetHeight - rows.offsetTop - 48;
  var size = 38;
  var apply = function (px) {
    rows.style.setProperty('--row-size', px + 'px');
    rows.style.setProperty('--row-pad', Math.max(3, Math.round(px * 0.37)) + 'px');
  };
  apply(size);
  // 16px is the floor: below it the rail stops being readable from the bar
  // and clipping the last row is the more honest failure.
  while (rows.scrollHeight > avail && size > 16) {
    size -= 2;
    apply(size);
  }
  fitRailNames(size);
}

/* A name only gets the width the score and the swing leave it, and at the
   full row size "Norwegian Wood" does not fit that -- which would be a
   three-table room paying for a twenty-table rule. So each name gives up a
   little type of its own before it gives up letters; the ellipsis is for
   names no size saves. */
function fitRailNames(rowSize) {
  var names = document.querySelectorAll('#rail-rows .row .name');
  var floor = Math.max(14, Math.round(rowSize * 0.7));
  for (var i = 0; i < names.length; i++) {
    var n = names[i];
    var size = rowSize;
    n.style.fontSize = '';
    while (n.scrollWidth > n.clientWidth && size > floor) {
      size -= 1;
      n.style.fontSize = size + 'px';
    }
  }
}
