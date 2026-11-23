/* The OBS overlay's renderer. See render_overlay.go for what the page is.

   Concatenated as core.js + overlay.js + boot.js and nothing else, so the
   wall's screen files are absent: core.js's render() hands every frame here
   when window.__KIT_TRIVIA_OVERLAY__ is set, and nothing below may call a
   helper that lives in a screen file.

   The overlay is rebuilt from the frame every time rather than diffed. It
   has no entrance animations on its parts, so a chip landing mid-betting
   repaints the strip without anything on it visibly moving. */
var ovPhaseKey = '';

function renderOverlay() {
  var key = state.phase + ':' + (state.round ? state.round.id : '');
  var main = document.getElementById('ov-main');
  if (key !== ovPhaseKey) {
    ovPhaseKey = key;
    main.classList.remove('fade');
    void main.offsetWidth;   // restart the animation
    main.classList.add('fade');
  }
  document.getElementById('ov-practice').classList.toggle('on', !!state.practice);
  renderOvLeft();
  main.innerHTML = '';
  switch (state.phase) {
    case PHASE.SETUP:
    case PHASE.LOBBY: ovLobby(main); break;
    case PHASE.BOARD: ovBoard(main); break;
    case PHASE.INTERMISSION: ovBreak(main); break;
    case PHASE.WAGER: ovWager(main); break;
    case PHASE.QUESTION: ovQuestion(main); break;
    case PHASE.REVEAL:
    case PHASE.BETTING:
    case PHASE.SCORING: ovCards(main); break;
    case PHASE.PODIUM: ovPodium(main); break;
    default: ovLine(main, ['Waiting for the host']);
  }
}

function inCards() {
  return [PHASE.REVEAL, PHASE.BETTING, PHASE.SCORING].indexOf(state.phase) >= 0;
}

/* ---------- the left column ----------

   Under the join code, by phase:
     - the cards are up: the question they answer, with the clock above it
       if there is one, the answer once it is known, and the standings in
       whatever room is left;
     - something else is timed: the clock alone, big;
     - the board: the topics still on it;
     - the lobby: the teams in so far;
     - the break and the podium: the standings. */
function renderOvLeft() {
  var timed = !!(state.deadlineMs || state.pausedMs);
  var cards = inCards();
  var timer = document.getElementById('ov-timer');
  timer.classList.toggle('on', timed);
  timer.classList.toggle('solo', timed && !cards);
  document.getElementById('ov-timer-sub').textContent = timerSub();
  tickOv();

  var aside = document.getElementById('ov-aside');
  aside.classList.toggle('on', cards);
  aside.innerHTML = '';
  if (cards) {
    if (state.phase === PHASE.SCORING && state.scoring) {
      aside.appendChild(el('div', 'ans', 'ANSWER ' + state.scoring.correctText));
    }
    var q = el('div', '', state.round ? state.round.text : '');
    aside.appendChild(q);
    shrinkToFit(q, 30, 20);
  }

  var board = !timed && state.phase === PHASE.BOARD;
  renderOvStandings(cards || (!timed && !board));

  var box = document.getElementById('ov-topics');
  box.classList.toggle('on', board);
  box.innerHTML = '';
  var topics = boardTopics();
  if (!topics.length) { return; }
  box.appendChild(el('h2', '', 'TOPICS'));
  topics.forEach(function (t) {
    var row = el('div', 'ov-topic' + (t.left ? '' : ' spent'));
    row.appendChild(el('span', 'name', t.name));
    row.appendChild(el('span', 'left', t.left ? t.left + ' LEFT' : 'DONE'));
    box.appendChild(row);
  });
}

/* The teams, best first, with the points this round just moved at scoring.
   In the lobby nobody has a score yet, so it is a roll call. Rows that do
   not fit are dropped from the bottom -- the leaders matter most, and a
   team further down has its own score on its phone. */
function renderOvStandings(on) {
  var box = document.getElementById('ov-standings');
  box.innerHTML = '';
  var lobby = state.phase === PHASE.LOBBY || state.phase === PHASE.SETUP;
  var teams = lobby ? state.teams.slice() : sortedTeams();
  box.classList.toggle('on', on && teams.length > 0);
  if (!on || !teams.length) { return; }
  box.appendChild(el('h2', '', lobby ? 'TEAMS IN' : 'STANDINGS'));
  var deltas = state.phase === PHASE.SCORING && state.scoring ? state.scoring.deltas || {} : {};
  teams.forEach(function (t, i) {
    var row = el('div', 'ov-stand');
    if (!lobby) { row.appendChild(el('b', '', i + 1)); }
    row.appendChild(el('span', 'name', t.name));
    var d = deltas[t.id];
    if (d) { row.appendChild(el('span', 'delta' + (d < 0 ? ' neg' : ''), (d > 0 ? '+' : '−') + money(Math.abs(d)))); }
    if (!lobby) { row.appendChild(el('span', 'score', money(t.score))); }
    box.appendChild(row);
  });
  var rows = box.querySelectorAll('.ov-stand');
  for (var i = rows.length - 1; i >= 0 && box.scrollHeight > box.clientHeight + 1; i--) {
    box.removeChild(rows[i]);
  }
  // A heading over one squeezed row reads as a mistake; give the room back.
  if (box.querySelectorAll('.ov-stand').length < 2) { box.classList.remove('on'); }
}

function boardTopics() {
  var byCol = [];
  (state.board || []).forEach(function (c) {
    var t = byCol[c.col] || (byCol[c.col] = { name: c.topic, left: 0 });
    if (!c.played) { t.left++; }
  });
  return byCol.filter(Boolean);
}

function timerSub() {
  var r = state.round;
  switch (state.phase) {
    case PHASE.QUESTION: return r ? (r.answered + ' OF ' + r.eligible + ' IN') : '';
    case PHASE.WAGER: return 'LOCK YOUR WAGER';
    case PHASE.BETTING: return 'PLACE YOUR CHIPS';
    default: return '';
  }
}

/* Its own tick, not the wall's ring: there is one clock here and it is a
   numeral. A pause replaces the numeral rather than freezing it. */
function tickOv() {
  var node = document.getElementById('ov-secs');
  if (!node || !state) { return; }
  var paused = !!state.pausedMs;
  node.classList.toggle('paused', paused);
  if (paused) { node.textContent = '❚❚ PAUSED'; return; }
  var left = remainingMs();
  if (left === null) { node.textContent = ''; return; }
  var secs = Math.ceil(left / 1000);
  node.textContent = secs;
  node.classList.toggle('warn', secs <= 15 && secs > 5);
  node.classList.toggle('hot', secs <= 5);
}
setInterval(tickOv, 200);

/* ---------- the bottom strip, one per phase ---------- */
function ovLine(main, parts, sub) {
  var line = el('div', 'ov-line');
  parts.forEach(function (p) {
    line.appendChild(typeof p === 'string' ? document.createTextNode(p) : el('em', '', p.em));
  });
  main.appendChild(line);
  if (sub) { main.appendChild(el('div', 'ov-sub', sub)); }
  shrinkToFit(line, 64, 36);
}

function ovLobby(main) {
  var n = state.teams.length;
  ovLine(main, ['Scan the code to join'],
    n ? n + (n === 1 ? ' team in' : ' teams in') + ' so far' : 'Trivia starts soon');
}

function ovBoard(main) {
  var spent = state.board.length && state.board.every(function (c) { return c.played; });
  if (spent || !state.picker) {
    ovLine(main, [spent ? 'That\'s the board' : 'Next question coming up'], 'Stand by');
    return;
  }
  ovLine(main, [{ em: state.picker.name }, ' picks the next topic'], ovPickWhy(state.pickerReason));
}

/* The wall's pickWhy lives in board.js, which the overlay does not load. */
function ovPickWhy(reason) {
  if (reason === 'wrote_winner') { return 'Closest answer last round'; }
  if (reason === 'lowest') { return 'Lowest score picks'; }
  return '';
}

function ovBreak(main) {
  var top = sortedTeams()[0];
  ovLine(main, ['Break, back soon'], top ? top.name + ' leads with ' + money(top.score) : '');
}

function ovWager(main) {
  var cat = state.round ? state.round.category : '';
  ovLine(main, cat ? ['Final category: ', { em: cat }] : ['Final question'],
    'Lock in your wager on your phone');
}

function ovQuestion(main) {
  var q = el('div', 'ov-q', state.round ? state.round.text : '');
  main.appendChild(q);
  shrinkToFit(q, 72, 36);
}

function ovPodium(main) {
  var top = sortedTeams()[0];
  if (!top) { ovLine(main, ['Thanks for playing']); return; }
  ovLine(main, [{ em: top.name }, ' wins with ' + money(top.score)], 'Thanks for playing');
}

function sortedTeams() {
  return state.teams.slice().sort(function (a, b) { return b.score - a.score; });
}

/* The answers on the table, and nothing else: the question is in the left
   column. One row up to twelve, two past that. The numeral size is fitted
   to the narrowest card and then shared, so a row of numbers does not come
   out at six different sizes. */
function ovCards(main) {
  var slots = state.slots || [];
  var rows = slots.length <= 12 ? 1 : 2;
  var cols = Math.max(1, Math.ceil(slots.length / rows));
  var grid = el('div', 'ov-cards' + (rows === 2 ? ' two' : ''));
  grid.style.gridTemplateColumns = 'repeat(' + cols + ', minmax(0, 1fr))';
  grid.style.gridTemplateRows = 'repeat(' + rows + ', minmax(0, 1fr))';
  var win = state.phase === PHASE.SCORING && state.scoring ? state.scoring.winningSlot : '';
  var betting = state.phase !== PHASE.REVEAL;
  slots.forEach(function (s) {
    var cls = 'ov-card' + (s.value === null ? ' pseudo' : '');
    if (win) { cls += s.id === win ? ' win' : ' lose'; }
    var card = el('div', cls);
    card.appendChild(el('div', 'val', s.label));
    // Under the number: who wrote it at the reveal, the chips on it from
    // betting on. Never a mix -- a row reading "$200, Bar Flies, $600"
    // makes the room work out which is which. An empty pot keeps its line
    // so the numerals stay level.
    if (betting) {
      card.appendChild(el('div', 'pot', s.pot ? money(s.pot) : '\u00a0'));
    } else {
      card.appendChild(el('div', 'names', (s.teams || []).join(' & ') || '\u00a0'));
    }
    grid.appendChild(card);
  });
  main.appendChild(grid);
  fitOvCardValues(grid, rows === 1 ? 64 : 48);
}

function fitOvCardValues(grid, start) {
  var vals = grid.querySelectorAll('.ov-card:not(.pseudo) .val');
  var size = start;
  for (var i = 0; i < vals.length; i++) {
    var v = vals[i];
    v.style.fontSize = size + 'px';
    while (v.scrollWidth > v.parentNode.clientWidth - 16 && size > 16) {
      size -= 2;
      v.style.fontSize = size + 'px';
    }
  }
  for (var j = 0; j < vals.length; j++) { vals[j].style.fontSize = size + 'px'; }
}

/* Shrink a block's type until it fits its box. */
function shrinkToFit(node, size, floor) {
  node.style.fontSize = size + 'px';
  var box = node.parentNode;
  while ((box.scrollHeight > box.clientHeight + 1 || node.scrollWidth > node.clientWidth + 1) && size > floor) {
    size -= 2;
    node.style.fontSize = size + 'px';
  }
}
