/* Screen 3: the question, the answered strip, and the countdown ring
   (which the cards screens share). */
/* --- 3. question --- */
var ringTimer = null;
function renderQuestion(phaseChanged) {
  var paint = function () {
    show('s-question');
    var q = document.getElementById('q-text');
    q.textContent = state.round ? state.round.text : '';
    q.classList.toggle('final', !!(state.round && state.round.isFinal));
    fitQuestion(q);
    renderAnsweredStrip();
    startRing('question');
  };
  if (phaseChanged && state.round && !state.round.isFinal) {
    var cell = null;
    state.board.forEach(function (c) { if (c.played && !cell) { cell = c; } });
    // Flip from the tile that was just consumed, if it is still on screen.
    var played = state.board.filter(function (c) { return c.played; });
    var last = played.length ? played[played.length - 1] : null;
    if (last && document.querySelector('[data-cell-id="' + last.id + '"]')) {
      flipFrom(last.id, paint);
      return;
    }
  }
  if (phaseChanged && state.round && state.round.isFinal) {
    show('s-final');
    later(1800, paint);
    return;
  }
  paint();
}

/* Measured, not clamped: shrink until it fits, because clamp() guesses and
   a loop knows. Floor at 64px -- below that it stops reading from the
   back of the room and a shorter question is the real fix. */
function fitQuestion(node) {
  var size = 96;
  node.style.fontSize = size + 'px';
  while (node.scrollHeight > 400 && size > 64) {
    size -= 4;
    node.style.fontSize = size + 'px';
  }
}

function renderAnsweredStrip() {
  var strip = document.getElementById('answered-strip');
  strip.innerHTML = '';
  var eligible = state.teams.filter(function (t) { return t.eligible; });
  eligible.forEach(function (t) {
    var cls = 'tbar' + (t.answered ? ' in' : '') + (t.stakeLocked ? ' locked' : '');
    var bar = el('div', cls);
    // In the final each pip flips to LOCKED as the stake lands -- WITHOUT
    // the amount. Not knowing whether the leader defended or sat out is
    // most of the tension.
    if (t.stakeLocked) { bar.appendChild(el('span', '', 'LOCK')); }
    bar.title = t.name;
    strip.appendChild(bar);
  });
  var r = state.round;
  document.getElementById('answered-count').textContent =
    r ? (r.answered + ' OF ' + r.eligible + ' IN') : '';
}

/* An SVG ring around the numeral, because a ring reads from across a room
   and a bare number does not. */
var ringKey = '';
function startRing(where) {
  // Same screen, same deadline: leave the ring alone. It is restarted from
  // scratch on every call otherwise, which re-baselines the arc to full --
  // during betting that is once per chip, and the countdown appeared to
  // jump backwards every time somebody bet.
  var key = where + ':' + (state ? state.deadlineMs : 0);
  if (ringTimer && key === ringKey) { return; }
  ringKey = key;
  if (ringTimer) { clearInterval(ringTimer); }
  var ids = where === 'cards'
    ? ['cards-ring-arc', 'cards-ring', 'cards-countdown']
    : ['ring-arc', 'ring', 'countdown'];
  var arc = document.getElementById(ids[0]);
  var ring = document.getElementById(ids[1]);
  var label = document.getElementById(ids[2]);
  var C = 2 * Math.PI * 130;
  arc.style.strokeDasharray = C;
  var total = null;
  ringTimer = setInterval(function () {
    var left = remainingMs();
    if (left === null) {
      label.textContent = '';
      arc.style.strokeDashoffset = C;
      return;
    }
    if (total === null) { total = Math.max(left, 1); }
    var secs = Math.ceil(left / 1000);
    label.textContent = secs;
    // Three digits are wider than the ring's inside; a host who sets a
    // two-minute clock, or leans on +15s, gets a numeral scaled to fit
    // rather than one that spills out both sides of the track.
    label.classList.toggle('wide', secs >= 100);
    arc.style.strokeDashoffset = C * (1 - Math.min(1, left / total));
    ring.classList.toggle('warn', secs <= 15 && secs > 5);
    ring.classList.toggle('hot', secs <= 5);
  }, 100);
}
