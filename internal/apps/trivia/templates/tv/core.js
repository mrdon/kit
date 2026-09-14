/* The TV display's client: state, transport, the local countdown and the
   render dispatch. Vanilla, because the page is render(state) over seven
   screens with big CSS-driven transforms -- React would earn nothing here
   and would cost a build step on a page that must paint from a cheap stick
   on flaky wifi.

   ONE FILE PER SCREEN under templates/tv/, concatenated by render.go into a
   single IIFE in a fixed order (see tvScripts). Function declarations hoist
   across the whole closure, so a screen file may call helpers from any
   other; only top-level STATEMENTS are order-sensitive, which is why the
   shared vars live here at the top and the boot lives in boot.js at the end.

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

  // BEFORE the screen, not after. The corner narrows the board and the deck,
  // and both of those measure-and-shrink their own type against the width they
  // have been given -- so a corner applied afterwards left every numeral fitted
  // to a box 320px wider than the one it ended up in, and a 21-card reveal came
  // out with "104" clipped to "10".
  renderJoinCorner();

  switch (state.phase) {
    case 'setup':
    case 'lobby':   renderJoin(); break;
    case 'board':   renderBoard(prev); break;
    case 'wager':   renderWager(phaseChanged); break;
    case 'question': renderQuestion(phaseChanged); break;
    case 'reveal':  renderCards('reveal'); break;
    case 'betting': renderCards('betting'); break;
    case 'scoring': renderScoring(phaseChanged); break;
    case 'podium':  renderPodium(phaseChanged); break;
    default:        show('s-hold');
  }
  noticeArrivals(prev);
  // The corner shows the night's NAME as the host typed it ("Tuesday
  // Quiz"), not the two-word URL slug. The slug is only useful where
  // somebody has to type it, which is the join screen.
  var gn = document.querySelectorAll('.gamename');
  for (var i = 0; i < gn.length; i++) {
    gn[i].textContent = state.title || state.game;
  }
}
