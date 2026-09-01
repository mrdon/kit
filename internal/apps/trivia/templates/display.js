/* The TV display's whole client. Vanilla, because the page is render(state)
   over seven screens with big CSS-driven transforms -- React would earn
   nothing here and would cost a build step on a page that must paint from a
   cheap stick on flaky wifi.

   Two rules run through all of it:
     1. THE SERVER ALWAYS WINS. Choreography (the scoring beat) is driven by
        local setTimeout, but if a fresh frame lands mid-sequence the timers
        are cancelled and the new state renders immediately. Animation beats
        are never driven from the server; that would couple timing to bar
        wifi.
     2. THE COUNTDOWN IS LOCAL. Every frame carries an absolute deadline and
        the server's clock; we derive a skew and tick at 100ms ourselves.
        Countdown ticks are never sent over the wire.
*/
(function () {
  'use strict';

  var STREAM = window.__KIT_TRIVIA_STREAM__;
  var POLL = window.__KIT_TRIVIA_POLL__;
  var state = null;
  var skew = 0;              // serverNow - Date.now(), latest sample
  var timers = [];           // choreography timers, cancellable
  var lastPhaseKey = '';
  var lastVersion = -1;
  var es = null;
  var lastFrameAt = Date.now();

  /* ---------- stage scaling ---------- */
  function fit() {
    var el = document.getElementById('fit');
    var s = Math.min(window.innerWidth / 1920, window.innerHeight / 1080);
    el.style.transform = 'scale(' + s + ')';
    var wrap = document.getElementById('stagewrap');
    wrap.style.paddingLeft = Math.max(0, (window.innerWidth - 1920 * s) / 2) + 'px';
    wrap.style.paddingTop = Math.max(0, (window.innerHeight - 1080 * s) / 2) + 'px';
    el.style.left = Math.max(0, (window.innerWidth - 1920 * s) / 2) + 'px';
    el.style.top = Math.max(0, (window.innerHeight - 1080 * s) / 2) + 'px';
  }
  window.addEventListener('resize', fit);

  /* ---------- transport ---------- */
  function connect() {
    if (es) { es.close(); }
    es = new EventSource(STREAM);
    es.addEventListener('state', function (ev) {
      lastFrameAt = Date.now();
      try { apply(JSON.parse(ev.data)); } catch (e) { /* a malformed frame is not worth a blank wall */ }
    });
    es.addEventListener('open', function () { lastFrameAt = Date.now(); setDot(false); });
    es.addEventListener('error', function () { setDot(true); });
  }

  /* A suspended EventSource frequently LOOKS open and is dead, so silence is
     the signal rather than an error event: no frame and no keep-alive for
     20s means reopen. */
  setInterval(function () {
    var quiet = Date.now() - lastFrameAt;
    if (quiet > 20000) { setDot(true); connect(); poll(); }
  }, 5000);

  /* Poll fallback. A captive portal or a proxy that eats SSE should cost a
     few seconds of latency, not a frozen screen. */
  function poll() {
    var url = POLL + (lastVersion >= 0 ? '?since=' + lastVersion : '');
    fetch(url, { credentials: 'same-origin' }).then(function (r) {
      if (r.status === 204) { return null; }
      return r.json();
    }).then(function (data) {
      if (data) { lastFrameAt = Date.now(); apply(data); }
    }).catch(function () { /* offline; the watchdog will try again */ });
  }
  setInterval(poll, 5000);

  function setDot(on) {
    var d = document.getElementById('dot');
    if (d) { d.classList.toggle('on', on); }
  }

  /* ---------- state ---------- */
  function apply(next) {
    if (next.version <= lastVersion) { return; }   // a stale frame never repaints backwards
    lastVersion = next.version;
    skew = next.serverNow - Date.now();            // latest sample folds one-way delay in as a
                                                   // conservative bias, so we run slightly ahead
    var prev = state;
    state = next;
    clearTimers();                                 // the server always wins
    render(prev);
  }

  function clearTimers() {
    timers.forEach(clearTimeout);
    timers = [];
  }
  function later(ms, fn) { timers.push(setTimeout(fn, ms)); }

  function remainingMs() {
    if (!state || !state.deadlineMs) { return null; }
    return Math.max(0, state.deadlineMs - (Date.now() + skew));
  }

  /* ---------- rendering ---------- */
  function show(id) {
    var screens = document.querySelectorAll('.screen');
    for (var i = 0; i < screens.length; i++) {
      screens[i].classList.toggle('on', screens[i].id === id);
    }
  }

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) { e.className = cls; }
    if (text !== undefined && text !== null) { e.textContent = String(text); }
    return e;
  }

  function money(n) {
    var neg = n < 0;
    var s = String(Math.abs(n)).replace(/\B(?=(\d{3})+(?!\d))/g, ',');
    return (neg ? '-$' : '$') + s;
  }

  function render(prev) {
    if (!state) { return; }
    var key = state.phase + ':' + (state.round ? state.round.id : '');
    var phaseChanged = key !== lastPhaseKey;
    lastPhaseKey = key;

    switch (state.phase) {
      case 'setup':
      case 'lobby':   renderJoin(); break;
      case 'board':   renderBoard(prev); break;
      case 'question': renderQuestion(phaseChanged); break;
      case 'reveal':  renderCards('reveal'); break;
      case 'betting': renderCards('betting'); break;
      case 'scoring': renderScoring(phaseChanged); break;
      case 'podium':  renderPodium(phaseChanged); break;
      default:        show('s-hold');
    }
    // The corner shows the night's NAME as the host typed it ("Tuesday
    // Quiz"), not the two-word URL slug. The slug is only useful where
    // somebody has to type it, which is the join screen.
    var gn = document.querySelectorAll('.gamename');
    for (var i = 0; i < gn.length; i++) {
      gn[i].textContent = state.title || state.game;
    }
  }

  /* --- 1. join --- */
  function renderJoin() {
    show('s-join');
    // The night's name is already the hero here; repeating it in the corner
    // is noise.
    document.getElementById('gamename').style.visibility = 'hidden';
    document.getElementById('join-count').textContent =
      state.teams.length + (state.teams.length === 1 ? ' TEAM IN' : ' TEAMS IN');
    fitJoinHero();
    fitJoinURL();
    fitJoinRules();
    var host = document.getElementById('join-pills');
    // Only append what is new, so existing pills keep their entrance and the
    // wall does not re-animate every pill each time somebody joins.
    var have = host.childElementCount;
    for (var i = have; i < state.teams.length; i++) {
      host.appendChild(el('div', 'pill', state.teams[i].name));
    }
    while (host.childElementCount > state.teams.length) { host.removeChild(host.lastChild); }
  }

  /* The hero is the host's own words, so its size cannot be a constant: "Quiz
     night, 1 Sep" is four lines where "Trivia" is one. Measure and shrink
     until it fits the space it has, the same loop the menu board uses --
     clamp() guesses, a loop knows. */
  function fitJoinHero() {
    var box = document.getElementById('join-words');
    if (!box) { return; }
    var words = box.querySelectorAll('.join-word');
    /* Room for the URL and the team count beneath it. */
    var avail = 600;
    var size = 180;
    var apply = function (px) {
      for (var i = 0; i < words.length; i++) { words[i].style.fontSize = px + 'px'; }
    };
    apply(size);
    while ((box.scrollHeight > avail || box.scrollWidth > box.clientWidth) && size > 48) {
      size -= 6;
      apply(size);
    }
  }

  /* Five rules or six depending on whether the final is on, and the wording
     is fixed -- so the only variable is how much room is left beside the QR.
     Measure and shrink, with 22px as the floor: below that nobody reads it
     from the bar and the honest answer is that it does not fit. */
  function fitJoinRules() {
    var list = document.getElementById('join-rules');
    if (!list) { return; }
    var col = list.parentNode;
    var size = 30;
    list.style.fontSize = size + 'px';
    while (col.scrollHeight > col.clientHeight && size > 22) {
      size -= 1;
      list.style.fontSize = size + 'px';
    }
  }

  /* Somebody is reading this off a wall and typing it into a phone, so it has
     to stay on one line. Shrink to fit; never hyphenate mid-word. */
  function fitJoinURL() {
    var u = document.getElementById('join-url');
    if (!u) { return; }
    var box = u.parentNode.clientWidth;
    var size = 48;
    u.style.fontSize = size + 'px';
    while (u.scrollWidth > box && size > 24) {
      size -= 2;
      u.style.fontSize = size + 'px';
    }
  }

  /* --- 2. board --- */
  function renderBoard(prev) {
    show('s-board');
    document.getElementById('gamename').style.visibility = '';
    var grid = document.getElementById('board-grid');
    var cols = 0, rows = 0;
    state.board.forEach(function (c) {
      cols = Math.max(cols, c.col + 1);
      rows = Math.max(rows, c.row + 1);
    });
    grid.style.gridTemplateColumns = 'repeat(' + Math.max(cols, 1) + ', 1fr)';
    grid.style.gridTemplateRows = '120px repeat(' + Math.max(rows, 1) + ', 1fr)';
    grid.innerHTML = '';

    var topics = [];
    state.board.forEach(function (c) { topics[c.col] = c.topic; });
    for (var i = 0; i < cols; i++) { grid.appendChild(el('div', 'cat', topics[i] || '')); }

    for (var r = 0; r < rows; r++) {
      for (var c2 = 0; c2 < cols; c2++) {
        var cell = state.board.filter(function (x) { return x.col === c2 && x.row === r; })[0];
        var d = el('div', 'cell' + (cell && cell.played ? ' played' : ''));
        d.dataset.cellId = cell ? cell.id : '';
        d.appendChild(el('span', 'val', cell ? money(cell.points) : ''));
        grid.appendChild(d);
      }
    }
    if (prev && prev.phase === 'scoring') { /* returning from a round: no flip */ }
    fitCellValues();
  }

  /* $500 and $1,000 are different widths in Bungee, and 88px of the latter
     overflows the tile. Measure and shrink rather than picking a size that
     happens to work for one of them. */
  function fitCellValues() {
    var vals = document.querySelectorAll('#board-grid .cell .val');
    for (var i = 0; i < vals.length; i++) {
      var v = vals[i];
      var box = v.parentNode.clientWidth - 20;
      var size = 88;
      v.style.fontSize = size + 'px';
      while (v.scrollWidth > box && size > 28) {
        size -= 4;
        v.style.fontSize = size + 'px';
      }
    }
  }

  /* The FLIP: measure the picked tile, clone it, transform the clone to fill
     the stage, then swap in the question. This is what makes the screen feel
     like Jeopardy. */
  function flipFrom(cellId, done) {
    var tile = document.querySelector('[data-cell-id="' + cellId + '"]');
    var clone = document.getElementById('flip');
    if (!tile || !clone) { done(); return; }
    var r = tile.getBoundingClientRect();
    var stage = document.getElementById('fit').getBoundingClientRect();
    var scale = stage.width / 1920 || 1;
    var x = (r.left - stage.left) / scale, y = (r.top - stage.top) / scale;
    var w = r.width / scale, h = r.height / scale;

    clone.style.transition = 'none';
    clone.style.left = x + 'px'; clone.style.top = y + 'px';
    clone.style.width = w + 'px'; clone.style.height = h + 'px';
    clone.style.transform = 'none';
    clone.style.opacity = '1';
    clone.innerHTML = tile.innerHTML;
    // Force a reflow so the transition below starts from the measured rect.
    void clone.offsetWidth;
    clone.style.transition = '';
    clone.style.transform =
      'translate(' + (64 - x) + 'px,' + (64 - y) + 'px) scale(' + ((1920 - 128) / w) + ',' + ((1080 - 128) / h) + ')';
    later(620, function () { clone.style.opacity = '0'; done(); });
  }

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
  function startRing(where) {
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
      arc.style.strokeDashoffset = C * (1 - Math.min(1, left / total));
      ring.classList.toggle('warn', secs <= 15 && secs > 5);
      ring.classList.toggle('hot', secs <= 5);
    }, 100);
  }

  /* --- 4/5. cards --- */
  function renderCards(mode) {
    show('s-cards');
    var host = document.getElementById('cards');
    host.className = 'cards';
    host.innerHTML = '';
    var band = document.getElementById('answer-band');
    band.classList.remove('in');
    band.classList.remove('shown');
    document.getElementById('rail').classList.remove('in');

    (state.slots || []).forEach(function (s, i) {
      var card = el('div', 'card' + (s.value === null ? ' pseudo' : ''));
      card.style.animationDelay = (i * 120) + 'ms';   // a stagger, so they land like dealt cards
      card.dataset.slotId = s.id;
      card.appendChild(el('div', 'val', s.label));
      card.appendChild(el('div', 'names', (s.teams || []).join(' · ')));
      var tray = el('div', 'tray');
      if (mode === 'betting' || mode === 'scored') {
        (s.chips || []).forEach(function (c, ci) {
          var chip = el('div', 'chip ' + (c.amount >= 200 ? 'c200' : 'c100'), money(c.amount));
          chip.style.animationDelay = (ci * 60) + 'ms';
          tray.appendChild(chip);
        });
      }
      card.appendChild(tray);
      if ((mode === 'betting' || mode === 'scored') && s.pot) { card.appendChild(el('div', 'pot', money(s.pot))); }
      host.appendChild(card);
    });
    document.getElementById('cards-question').textContent = state.round ? state.round.text : '';
    // During betting the chips are deliberately not on the cards yet, so the
    // room needs some other sign of progress — otherwise the screen looks
    // frozen while five tables think.
    var tally = document.getElementById('bet-tally');
    if (mode === 'betting') {
      var want = (state.tokens || []).length || 1;
      var eligible = state.teams.filter(function (t) { return t.eligible; });
      var inCount = eligible.filter(function (t) { return t.chipsPlaced >= want; }).length;
      tally.textContent = inCount + ' OF ' + eligible.length + ' TABLES IN';
      tally.style.display = '';
    } else {
      tally.style.display = 'none';
    }
    // The footer holds either the countdown or the answer band, never both.
    var footer = document.getElementById('cards-footer');
    footer.classList.toggle('scored', mode === 'scored');
    document.getElementById('cards-screen').classList.remove('railed');
    startRing('cards');
  }

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
    var cards = document.querySelectorAll('#cards .card');
    for (var i = 0; i < cards.length; i++) {
      var isWin = cards[i].dataset.slotId === win;
      cards[i].classList.toggle('win', isWin);
      if (!isWin) {
        var chips = cards[i].querySelectorAll('.chip');
        for (var j = 0; j < chips.length; j++) { chips[j].classList.add('falling'); }
      }
    }
    document.getElementById('cards').classList.add('dim');
    var band = document.getElementById('answer-band');
    band.textContent = state.scoring.correctText || String(state.scoring.correctValue);
    band.classList.add('shown');
    // Force a reflow so the slide-in runs from off-stage rather than being
    // collapsed into the same frame as the display change.
    void band.offsetWidth;
    band.classList.add('in');
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
    rail.innerHTML = '';
    teams.forEach(function (t) {
      var row = el('div', 'row');
      row.dataset.teamId = t.id;
      var left = el('div', '', t.name);
      row.appendChild(left);
      var right = el('div', 'score', money(t.score));
      row.appendChild(right);
      var d = state.scoring && state.scoring.deltas ? state.scoring.deltas[t.id] : null;
      if (d) { left.appendChild(el('span', 'delta', '  ' + (d > 0 ? '+' : '') + money(d))); }
      rail.appendChild(row);
    });
    document.getElementById('rail').classList.add('in');
    document.getElementById('cards-screen').classList.add('railed');

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

  /* --- 8. podium --- */
  function renderPodium(phaseChanged) {
    show('s-podium');
    var host = document.getElementById('podium');
    if (!phaseChanged && host.childElementCount) { return; }
    host.innerHTML = '';
    var teams = state.teams.slice().sort(function (a, b) { return b.score - a.score; }).slice(0, 3);
    // Ranks 3, 2, 1 bottom-up: the winner lands last.
    var order = [2, 1, 0].filter(function (i) { return teams[i]; });
    var placed = {};
    order.forEach(function (idx, n) {
      later(n * 900, function () {
        if (placed[idx]) { return; }
        placed[idx] = true;
        var t = teams[idx];
        var p = el('div', 'plinth p' + (idx + 1));
        if (idx === 0) { p.appendChild(el('div', 'crown', '♛')); }
        p.appendChild(el('div', 'rank', '#' + (idx + 1)));
        p.appendChild(el('div', 'name', t.name));
        p.appendChild(el('div', 'score', money(t.score)));
        // Column order left-to-right is 2nd, 1st, 3rd, so the winner is
        // centre stage regardless of the order they appear in.
        if (idx === 0) { host.insertBefore(p, host.children[1] || null); }
        else if (idx === 1) { host.insertBefore(p, host.firstChild); }
        else { host.appendChild(p); }
        if (idx === 0) { burst(p); }
      });
    });
  }

  /* Twenty absolutely positioned divs on randomised keyframes. No library,
     and it reads as celebration from thirty feet. */
  function burst(node) {
    for (var i = 0; i < 20; i++) {
      var s = el('div', 'spark');
      var angle = Math.random() * Math.PI * 2;
      var dist = 180 + Math.random() * 260;
      s.style.left = '50%'; s.style.top = '30%';
      s.style.transition = 'transform ' + (700 + Math.random() * 600) + 'ms ease-out, opacity 1s';
      node.appendChild(s);
      (function (node2, angle2, dist2) {
        setTimeout(function () {
          node2.style.transform = 'translate(' + Math.cos(angle2) * dist2 + 'px,' + Math.sin(angle2) * dist2 + 'px)';
          node2.style.opacity = '0';
        }, 20);
      })(s, angle, dist);
    }
  }

  /* The version poll, same shape as the menu board's.
     The SSE stream carries live state, but the QR code, the join words and
     the heading are baked into the HTML at render time -- so a renamed night,
     or a newer game appearing on the stable address, leaves the screen
     showing something wrong until somebody walks over to it. A few bytes
     every 15s, and a reload only when they actually change. */
  setInterval(function () {
    fetch(window.__KIT_TRIVIA_VERSION_URL__, { credentials: 'same-origin' })
      .then(function (r) { return r.text(); })
      .then(function (v) {
        if (v && v.trim() && v.trim() !== window.__KIT_TRIVIA_VERSION__) {
          window.location.reload();
        }
      })
      .catch(function () { /* offline; try again next tick */ });
  }, 15000);

  /* ---------- clock ---------- */
  setInterval(function () {
    var d = new Date();
    var h = d.getHours() % 12 || 12;
    var m = String(d.getMinutes()).padStart(2, '0');
    document.getElementById('clock').textContent = h + ':' + m;
  }, 1000);

  document.addEventListener('visibilitychange', function () {
    if (!document.hidden) { connect(); poll(); }
  });

  fit();
  connect();
  poll();
})();
