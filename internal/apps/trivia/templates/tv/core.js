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
/* Any traffic at all on the socket: a frame, a ping, or an open. Silence on
   THIS is what the watchdog reads, and a connect ATTEMPT stamps it too, so a
   socket that is still coming up is never mistaken for one that has died. */
var lastFrameAt = Date.now();
var retryMs = 1000;        /* reconnect backoff, reset on a healthy socket */
var retryTimer = null;
var connectingSince = 0;   /* when the current socket started dialling */
var stallSince = 0;        /* when the countdown first sat past its deadline */
var lastStallPoll = 0;

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

/* THIS IS A DELIBERATE MIRROR of web/console/src/liveStream.ts, which is the
   one the phone and the host console share. It cannot import that module --
   this file is inlined into a server-rendered page with no build step, on
   purpose, so the wall paints from a cheap stick on flaky wifi -- so the two
   are kept in step by hand and are meant to be changed together. Every rule
   below has its reasoning written out over there. */
function connect() {
  /* NEVER tear down a socket that is still trying to come up. This is the
     whole bug: the watchdog used to call connect() every 5s for as long as
     the stream was silent, and connect() closed whatever was there first, so
     a handshake needing six seconds on bad wifi was killed at five, every
     five seconds, forever. The recovery path was what prevented recovery. */
  /* ...but not FOREVER. A socket can latch in CONNECTING and never resolve,
     and an unconditional pass meant nothing could replace it: the wall would
     run on the fallback poll for the rest of the night with the dot red.
     Past twice the silence window it has had every chance. */
  if (es && es.readyState === 0 /* CONNECTING */ && Date.now() - connectingSince < 40000) { return; }
  if (es) { es.close(); }
  /* Give the new socket a full silence window to prove itself, or the
     watchdog reads the OLD timestamp and kills it on the next tick. */
  lastFrameAt = Date.now();
  connectingSince = Date.now();
  es = new EventSource(STREAM);
  var alive = function () { lastFrameAt = Date.now(); retryMs = 1000; setDot(false); };
  es.addEventListener('state', function (ev) {
    alive();
    try { apply(JSON.parse(ev.data)); } catch (e) { /* a malformed frame is not worth a blank wall */ }
  });
  es.addEventListener('open', alive);
  /* The liveness beat — see web_stream.go. A break or an emptied board can
     sit unchanged for minutes, and without this the watchdog below reads that
     quiet as a dead socket and rebuilds the stream under the room. */
  es.addEventListener('ping', alive);
  es.addEventListener('error', function () {
    setDot(true);
    /* EventSource retries on its own while it can. CLOSED means the browser
       has given up for good and nothing will ever arrive on it. */
    if (es && es.readyState === 2 /* CLOSED */) { scheduleRetry(); }
  });
}

/* Reconnect with backoff rather than on a fixed beat, so a stall cannot
   become a teardown loop. */
function scheduleRetry() {
  if (retryTimer !== null) { return; }
  var wait = retryMs;
  retryMs = Math.min(retryMs * 2, 15000);
  retryTimer = setTimeout(function () {
    retryTimer = null;
    connect();
    poll();
  }, wait);
}

/* Any recovery the browser hands us is worth taking at once, and it resets
   the backoff: an access point coming back is new information, not another
   failure. */
function revive() {
  retryMs = 1000;
  if (retryTimer !== null) { clearTimeout(retryTimer); retryTimer = null; }
  connect();
  poll();
}
window.addEventListener('online', revive);
window.addEventListener('pageshow', revive);

/* A suspended EventSource frequently LOOKS open and is dead, so silence is
   the signal rather than an error event: no frame and no ping for 20s means
   reopen. The server beats every 8s, so this only fires on a real stall.
   Looking often is free; it is the 20s that decides, and conflating the two
   is what produced the teardown loop. */
setInterval(function () {
  var quiet = Date.now() - lastFrameAt;
  if (quiet > 20000) { setDot(true); scheduleRetry(); }
}, 2000);

/* THE COUNTDOWN AS A LIVENESS CHECK.

   When a phase carries a deadline the SERVER ends it, so a clock sitting on
   zero with no newer version is not a quiet moment that might be fine -- it
   is proof the wall is not hearing this game, and it is exactly what a frozen
   screen looks like from the room.

   Silence alone cannot catch this: a socket delivering pings but no state
   looks perfectly healthy to the watchdog above. The clock knows better.

   And the poll is the CURE as well as the alarm -- /state sweeps an expired
   phase on its way past, so a screen noticing the stall is a screen that ends
   the phase the server should have ended. */
setInterval(function () {
  if (!state || !state.deadlineMs) { stallSince = 0; return; }
  var past = Date.now() + skew - state.deadlineMs;
  if (past < 1500) { stallSince = 0; return; }
  if (stallSince === 0) { stallSince = Date.now(); }
  var now = Date.now();
  if (now - lastStallPoll >= 1000) { lastStallPoll = now; setDot(true); poll(); }
  if (now - stallSince > 3000) { scheduleRetry(); }
}, 500);

/* Poll fallback. A captive portal or a proxy that eats SSE should cost a
   few seconds of latency, not a frozen screen. */
function poll() {
  var url = POLL + (lastVersion >= 0 ? '?since=' + lastVersion : '');
  fetch(url, { credentials: 'same-origin' }).then(function (r) {
    if (r.status === 204) { return null; }
    return r.json();
  }).then(function (data) {
    /* Deliberately does NOT stamp lastFrameAt. That clock means "the SOCKET
       is alive", and a working poll used to refresh it -- which meant a dead
       stream was never detected, never retried, and the wall ran on five
       second polling for the rest of the night with the dot showing green. */
    if (data) { apply(data); }
  }).catch(function () { /* offline; the watchdog will try again */ });
}
/* Slow while the stream is healthy, fast while it is not, rescheduling
   itself rather than sitting on one fixed beat. A screen with a dead socket
   should be two seconds behind, never frozen -- while the socket is down the
   poll IS the wall. */
function schedulePoll() {
  var down = Date.now() - lastFrameAt > 20000;
  setTimeout(function () {
    poll();
    schedulePoll();
  }, down ? 2000 : 5000);
}
schedulePoll();

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
  AWARDS: 'awards',
  PODIUM: 'podium'
};

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
    case PHASE.SETUP:
    case PHASE.LOBBY:   renderJoin(); break;
    case PHASE.BOARD:   renderBoard(prev); break;
    case PHASE.INTERMISSION: renderBreak(); break;
    case PHASE.WAGER:   renderWager(phaseChanged); break;
    case PHASE.QUESTION: renderQuestion(phaseChanged); break;
    case PHASE.REVEAL:  renderCards(PHASE.REVEAL); break;
    case PHASE.BETTING: renderCards(PHASE.BETTING); break;
    case PHASE.SCORING: renderScoring(phaseChanged); break;
    case PHASE.AWARDS:  renderAwards(phaseChanged); break;
    case PHASE.PODIUM:  renderPodium(phaseChanged); break;
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
