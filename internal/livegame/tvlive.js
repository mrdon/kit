/* KitLive: the live connection for every game's TV page.

   Shared by every live game's wall screen (trivia, Hoppers), so a reconnect
   fix reaches all of them at once. Vanilla, because a TV page is inlined into
   a server-rendered document with no build step, on purpose, so the wall
   paints from a cheap stick on flaky wifi.

   THIS IS A DELIBERATE MIRROR of web/console/src/live/liveStream.ts, which is
   the one the phones and the host console share. It cannot import that
   module, so the two are kept in step by hand and are meant to be changed
   together. Every rule below has its reasoning written out over there.

   Usage:
     var live = KitLive.open({
       stream: url, poll: url,            // GET /stream (SSE) and GET /state
       versionUrl: url, version: token,   // the page's reload stamp
       onFrame: function (frame, skew) {},  // a newer frame than any before it,
                                          // with serverNow - Date.now()
       onHealth: function (down) {},      // the connection dot
       canReload: function () { return true; }  // optional
     });
     live.poll()    // fetch the state now
*/
var KitLive = (function () {
  'use strict';

  var SILENCE_MS = 20000;
  var WATCH_EVERY_MS = 2000;
  var CONNECTING_MAX_MS = 40000;
  var RETRY_MIN_MS = 1000;
  var RETRY_MAX_MS = 15000;
  var POLL_OK_MS = 5000;
  var POLL_DOWN_MS = 2000;
  var STALL_EVERY_MS = 500;
  var STALL_GRACE_MS = 1500;
  var STALL_POLL_EVERY_MS = 1000;
  var STALL_RECONNECT_AFTER_MS = 3000;
  var VERSION_POLL_MS = 15000;

  function open(opts) {
    var onFrame = opts.onFrame || function () {};
    var onHealth = opts.onHealth || function () {};
    var canReload = opts.canReload || function () { return true; };
    var last = null;
    var lastVersion = -1;
    var skew = 0;
    var es = null;
    /* Any traffic at all on the socket: a frame, a ping, or an open. Silence
       on THIS is what the watchdog reads, and a connect ATTEMPT stamps it
       too, so a socket that is still coming up is never mistaken for one
       that has died. */
    var lastFrameAt = Date.now();
    var retryMs = RETRY_MIN_MS;
    var retryTimer = null;
    var connectingSince = 0;
    var stallSince = 0;
    var lastStallPoll = 0;
    var stale = false;

    function apply(next) {
      if (next.version <= lastVersion) { return; }   // a stale frame never repaints backwards
      lastVersion = next.version;
      skew = next.serverNow - Date.now();            // latest sample folds one-way delay in as a
                                                     // conservative bias, so we run slightly ahead
      last = next;
      onFrame(next, skew);
    }

    function connect() {
      /* NEVER tear down a socket that is still trying to come up: a handshake
         needing six seconds on bad wifi must get to finish. But not FOREVER:
         a socket can latch in CONNECTING and never resolve, and past twice
         the silence window it has had every chance. */
      if (es && es.readyState === 0 /* CONNECTING */ && Date.now() - connectingSince < CONNECTING_MAX_MS) { return; }
      if (es) { es.close(); }
      /* Give the new socket a full silence window to prove itself, or the
         watchdog reads the OLD timestamp and kills it on the next tick. */
      lastFrameAt = Date.now();
      connectingSince = Date.now();
      es = new EventSource(opts.stream);
      var alive = function () { lastFrameAt = Date.now(); retryMs = RETRY_MIN_MS; onHealth(false); };
      es.addEventListener('state', function (ev) {
        alive();
        try { apply(JSON.parse(ev.data)); } catch (e) { /* a malformed frame is not worth a blank wall */ }
      });
      es.addEventListener('open', alive);
      /* The liveness beat (livegame.EventPing). A break can sit unchanged for
         minutes, and without this the watchdog reads that quiet as a dead
         socket and rebuilds the stream under the room. */
      es.addEventListener('ping', alive);
      /* livegame.EventReset: the stream's identity is gone. A TV has none,
         so this only keeps the mirror honest: start over, floor cleared. */
      es.addEventListener('reset', function () { lastVersion = -1; if (es) { es.close(); es = null; } connect(); poll(); });
      es.addEventListener('error', function () {
        onHealth(true);
        /* EventSource retries on its own while it can. CLOSED means the
           browser has given up for good and nothing will ever arrive. */
        if (es && es.readyState === 2 /* CLOSED */) { scheduleRetry(); }
      });
    }

    /* Reconnect with backoff rather than on a fixed beat, so a stall cannot
       become a teardown loop. */
    function scheduleRetry() {
      if (retryTimer !== null) { return; }
      var wait = retryMs;
      retryMs = Math.min(retryMs * 2, RETRY_MAX_MS);
      retryTimer = setTimeout(function () {
        retryTimer = null;
        connect();
        poll();
      }, wait);
    }

    /* Any recovery the browser hands us is worth taking at once, and it
       resets the backoff: an access point coming back is new information,
       not another failure. */
    function revive() {
      retryMs = RETRY_MIN_MS;
      if (retryTimer !== null) { clearTimeout(retryTimer); retryTimer = null; }
      connect();
      poll();
    }

    /* Poll fallback. A captive portal or a proxy that eats SSE should cost a
       few seconds of latency, not a frozen screen. Deliberately does NOT
       stamp lastFrameAt: that clock means "the SOCKET is alive", and a
       working poll refreshing it meant a dead stream was never detected. */
    function poll() {
      var url = opts.poll + (lastVersion >= 0 ? '?since=' + lastVersion : '');
      fetch(url, { credentials: 'same-origin' }).then(function (r) {
        if (r.status === 204) { return null; }
        return r.json();
      }).then(function (data) {
        if (data) { apply(data); }
      }).catch(function () { /* offline; the watchdog will try again */ });
    }

    /* Slow while the stream is healthy, fast while it is not: while the
       socket is down the poll IS the wall. */
    function schedulePoll() {
      var down = Date.now() - lastFrameAt > SILENCE_MS;
      setTimeout(function () {
        poll();
        schedulePoll();
      }, down ? POLL_DOWN_MS : POLL_OK_MS);
    }

    /* A suspended EventSource frequently LOOKS open and is dead, so silence
       is the signal rather than an error event. */
    function watchSilence() {
      if (Date.now() - lastFrameAt > SILENCE_MS) { onHealth(true); scheduleRetry(); }
    }

    /* THE COUNTDOWN AS A LIVENESS CHECK. When a frame carries a deadline the
       SERVER ends it, so a clock sitting on zero with no newer version is
       proof the wall is not hearing this game. Silence alone cannot catch
       this: a socket delivering pings but no state looks healthy. */
    function watchStall() {
      if (!last || !last.deadlineMs) { stallSince = 0; return; }
      var past = Date.now() + skew - last.deadlineMs;
      if (past < STALL_GRACE_MS) { stallSince = 0; return; }
      if (stallSince === 0) { stallSince = Date.now(); }
      var now = Date.now();
      if (now - lastStallPoll >= STALL_POLL_EVERY_MS) { lastStallPoll = now; onHealth(true); poll(); }
      if (now - stallSince > STALL_RECONNECT_AFTER_MS) { scheduleRetry(); }
    }

    /* The page's own reload stamp (livegame.DisplayVersion). Latched: once a
       newer page has been seen, a straggling old answer cannot cancel it. */
    function checkVersion() {
      fetch(opts.versionUrl, { credentials: 'same-origin' })
        .then(function (r) { return r.text(); })
        .then(function (v) {
          if (v && v.trim() && v.trim() !== opts.version) { stale = true; }
          if (stale && canReload()) { window.location.reload(); }
        })
        .catch(function () { /* offline; try again next tick */ });
    }

    window.addEventListener('online', revive);
    window.addEventListener('pageshow', revive);
    document.addEventListener('visibilitychange', function () {
      if (!document.hidden) { connect(); poll(); }
    });
    setInterval(watchSilence, WATCH_EVERY_MS);
    setInterval(watchStall, STALL_EVERY_MS);
    if (opts.versionUrl) { setInterval(checkVersion, VERSION_POLL_MS); }
    schedulePoll();
    connect();
    poll();

    return { poll: poll };
  }

  return { open: open };
})();
