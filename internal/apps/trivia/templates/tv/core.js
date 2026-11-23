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
var state = null;
var skew = 0;              // serverNow - Date.now(), latest sample (from KitLive)
var timers = [];           // choreography timers, cancellable
var lastPhaseKey = '';

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

/* ---------- transport ----------
   The connection itself -- EventSource, the silence watchdog, the stuck
   countdown check, backoff, the poll fallback and the version reload -- is
   KitLive (internal/livegame/tvlive.js), shared with every live game's TV and
   inlined ahead of this script. boot.js opens it; apply() below is what it
   hands each newer frame to. */
function setDot(on) {
  var d = document.getElementById('dot');
  if (d) { d.classList.toggle('on', on); }
}

/* ---------- state ---------- */
/* KitLive has already dropped any frame older than the last one seen, so a
   stale frame never repaints backwards. */
function apply(next, sampledSkew) {
  skew = sampledSkew;
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
  // A paused clock is a frozen number, not an absence: the room should see
  // how long it will have when the host carries on.
  if (state && state.pausedMs) { return state.pausedMs; }
  if (!state || !state.deadlineMs) { return null; }
  return Math.max(0, state.deadlineMs - (Date.now() + skew));
}

/* ---------- rendering ---------- */
function show(id) {
  var screens = document.querySelectorAll('.screen');
  for (var i = 0; i < screens.length; i++) {
    screens[i].classList.toggle('on', screens[i].id === id);
  }
  // The lobby video only plays on the lobby; renderJoin starts it again.
  var v = document.getElementById('join-video');
  if (v && id !== 's-join' && !v.paused) { v.pause(); }
}

function el(tag, cls, text) {
  var e = document.createElement(tag);
  if (cls) { e.className = cls; }
  if (text !== undefined && text !== null) { e.textContent = String(text); }
  return e;
}

/* The phase vocabulary, as constants rather than forty string literals
   scattered over twelve files. Mirrors Phase in models.go and PHASE in
   triviaPhases.ts -- the wire values are the Go constants, so this is the
   third and last copy, and it is the one a screen file should reach for. */
var PHASE = {
  SETUP: 'setup',
  LOBBY: 'lobby',
  BOARD: 'board',
  INTERMISSION: 'intermission',
  WAGER: 'wager',
  QUESTION: 'question',
  REVEAL: 'reveal',
  BETTING: 'betting',
  SCORING: 'scoring',
  PODIUM: 'podium'
};

function money(n) {
  var neg = n < 0;
  var s = String(Math.abs(n)).replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  return (neg ? '-$' : '$') + s;
}

/* The two strips across the top of the wall that say the game is not running
   normally: the warm-up is up, or the host has stopped the clock. Both are
   things the host is saying out loud, so the wall only has to agree. */
function renderBanners() {
  var practice = document.getElementById('practice-banner');
  var paused = document.getElementById('paused-banner');
  if (practice) { practice.classList.toggle('on', !!state.practice); }
  if (paused) { paused.classList.toggle('on', !!state.pausedMs); }
}

function render(prev) {
  if (!state) { return; }
  // The OBS overlay loads core.js without the screen files below and draws
  // the frame itself. See overlay.js.
  if (window.__KIT_TRIVIA_OVERLAY__) { renderOverlay(prev); return; }
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
    case PHASE.SETUP:
    case PHASE.LOBBY:   renderJoin(); break;
    case PHASE.BOARD:   renderBoard(prev); break;
    case PHASE.INTERMISSION: renderBreak(); break;
    case PHASE.WAGER:   renderWager(phaseChanged); break;
    case PHASE.QUESTION: renderQuestion(phaseChanged); break;
    case PHASE.REVEAL:  renderCards(PHASE.REVEAL); break;
    case PHASE.BETTING: renderCards(PHASE.BETTING); break;
    case PHASE.SCORING: renderScoring(phaseChanged); break;
    case PHASE.PODIUM:  renderPodium(phaseChanged); break;
    default:        show('s-hold');
  }
  noticeArrivals(prev);
  renderBanners();
  // The corner shows the night's NAME as the host typed it ("Tuesday
  // Quiz"), not the two-word URL slug. The slug is only useful where
  // somebody has to type it, which is the join screen.
  var gn = document.querySelectorAll('.gamename');
  for (var i = 0; i < gn.length; i++) {
    gn[i].textContent = state.title || state.game;
  }
}
