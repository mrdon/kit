import { useCallback, useEffect, useRef, useState } from 'react';

// THE live connection for every React surface of the trivia app: the host
// console and the player's phone both use this, and neither keeps its own
// copy of any of it.
//
// They used to. Three copies of this logic existed -- phone, console, and the
// TV's vanilla one -- and they drifted, in the way three copies always do:
// the same two reconnect bugs were fixed in the phone's and left in the
// console's, and the console then froze in a way the phone no longer could.
// The TV's copy cannot import this (it is inlined into a server-rendered page
// with no build step, on purpose) so it stays separate, but it is a
// deliberate mirror of this file and the two are meant to be changed
// together.
//
// EventSource rather than fetch streaming: these endpoints are GET, so it
// gives browser reconnect for free and -- decisively -- sends cookies
// automatically while being unable to set headers, which is exactly why the
// player's identity is a cookie.

// What the transport needs off a frame. Both surfaces' frames satisfy this;
// everything else about them is the caller's business.
export interface LiveFrame {
  version: number;
  serverNow: number;
  deadlineMs: number;
}

export interface LiveStream<T> {
  frame: T | null;
  connected: boolean;
  // msLeft is the countdown, ticked LOCALLY at 100ms from an absolute
  // deadline and a per-frame skew. Countdown ticks are never sent over the
  // wire; that would put the clock on bar wifi.
  msLeft: number | null;
  apply: (f: T) => void;
  // reopen drops the stream and starts a new one, and invalidates anything
  // already in flight. MUST be called when the IDENTITY changes (a join, a
  // reclaim): EventSource sends whatever cookies exist AT CONNECT TIME, so a
  // stream opened before the identity cookie existed keeps arriving as a
  // spectator forever.
  reopen: () => void;
}

// The server beats every 8s (pingEvery in web_stream.go), so nothing at all
// on the socket for two and a half beats means dead rather than quiet.
const SILENCE_MS = 20_000;
// How often the watchdog LOOKS. Looking often is free; it is SILENCE_MS that
// decides whether a socket is dead, and conflating the two is what produced
// the original teardown loop.
const WATCH_EVERY_MS = 2_000;
// The poll is a FALLBACK. At 5s it was a second, redundant transport: twenty
// phones asking for a full snapshot against a pool that defaults to four
// connections, to re-fetch what the stream had already delivered.
const POLL_OK_MS = 30_000;
const POLL_DOWN_MS = 2_000;
// Reconnect backoff. The floor matters more than the ceiling: the original
// bug reopened every 5s unconditionally, so a handshake that needed six
// seconds on bar wifi never got to finish.
const RETRY_MIN_MS = 1_000;
const RETRY_MAX_MS = 15_000;

// THE COUNTDOWN AS A LIVENESS CHECK.
//
// This is the strongest signal the app has, and it is a far better one than
// silence. When a phase carries a deadline the SERVER ends it -- the sweeper
// runs process-wide every 500ms and every poll heals an expired phase on its
// way past -- so a countdown sitting on zero with no newer version is not a
// quiet moment that might be fine. It is proof we are not hearing the server,
// and it is exactly what a frozen phone looks like from the table: a clock at
// 0:00 and nothing happening.
//
// Silence alone could not catch this. A socket that is delivering pings but
// no state looks perfectly healthy to a silence watchdog, and so does one
// whose frames a proxy is eating. The clock knows better.
//
// Polling is also the CURE and not just the alarm: /state runs SweepDue
// first, so a client noticing the stall is a client that ends the phase the
// server should have ended. Which means this heals a wedged sweeper too.
const STALL_GRACE_MS = 1_500;
const STALL_POLL_EVERY_MS = 1_000;
const STALL_RECONNECT_AFTER_MS = 3_000;

export interface LiveStreamOptions {
  // Both absolute or both app-relative; null suspends the connection, which
  // is what the console does before a game is chosen.
  streamUrl: string | null;
  stateUrl: string | null;
}

export function useLiveStream<T extends LiveFrame>({ streamUrl, stateUrl }: LiveStreamOptions): LiveStream<T> {
  const [frame, setFrame] = useState<T | null>(null);
  const [connected, setConnected] = useState(false);
  const [msLeft, setMsLeft] = useState<number | null>(null);

  const versionRef = useRef(-1);
  const skewRef = useRef(0);
  const deadlineRef = useRef<number | null>(null);
  // Any traffic at all on the socket: a frame, a ping, or an open. Silence on
  // THIS is what the watchdog reads, and a connect attempt stamps it so a
  // socket still coming up is never mistaken for one that has died.
  const lastEventAt = useRef(Date.now());
  const esRef = useRef<EventSource | null>(null);
  const reopenRef = useRef<() => void>(() => undefined);
  // Bumped by reopen(). A response issued under an older identity must not be
  // applied after a newer one exists.
  const epochRef = useRef(0);

  const apply = useCallback((next: T) => {
    // A stale frame never repaints the screen backwards.
    if (next.version <= versionRef.current) return;
    versionRef.current = next.version;
    // Taking the latest sample folds one-way delay in as a conservative bias,
    // so the client runs slightly AHEAD of the server -- the right direction
    // to be wrong in when somebody is deciding whether they have time to
    // retype.
    skewRef.current = next.serverNow - Date.now();
    deadlineRef.current = next.deadlineMs || null;
    // Deliberately does NOT stamp lastEventAt. That clock means "the SOCKET
    // is alive", and this runs for poll frames too -- so a working poll used
    // to refresh it, which meant a dead stream was never detected and never
    // retried, and the client ran on polling for the rest of the night.
    setFrame(next);
  }, []);

  useEffect(() => {
    if (!streamUrl || !stateUrl) return undefined;
    let stopped = false;
    let retryMs = RETRY_MIN_MS;
    let retryTimer: number | null = null;
    let pollTimer: number | null = null;
    let healthy = false;
    let connectingSince = 0;
    let stallSince = 0;
    let lastStallPoll = 0;

    const poll = async () => {
      const since = versionRef.current >= 0 ? `?since=${versionRef.current}` : '';
      // A response from before the identity changed carries the OLD identity's
      // projection at the new version. Applied, it takes the version floor
      // with it and the reopened stream's first frame is then dropped as
      // stale -- which stranded a table on the join screen, and told it the
      // name was taken when it tried again.
      const epoch = epochRef.current;
      try {
        const res = await fetch(stateUrl + since, { credentials: 'same-origin' });
        if (res.status === 204 || !res.ok) return;
        const next = (await res.json()) as T;
        if (epoch !== epochRef.current) return;
        apply(next);
      } catch {
        /* offline; the watchdog and the next tick will retry */
      }
    };

    // The poll reschedules itself rather than sitting on one fixed interval,
    // so it can speed up when the stream is down -- while it is down, the
    // poll IS the game.
    const schedulePoll = () => {
      if (stopped) return;
      if (pollTimer !== null) window.clearTimeout(pollTimer);
      pollTimer = window.setTimeout(() => {
        void poll();
        schedulePoll();
      }, healthy ? POLL_OK_MS : POLL_DOWN_MS);
    };

    const setHealthy = (ok: boolean) => {
      if (ok === healthy) return;
      healthy = ok;
      setConnected(ok);
      schedulePoll();
    };

    const connect = () => {
      if (stopped) return;
      const current = esRef.current;
      // Never tear down a socket that is still trying to come up -- closing a
      // CONNECTING EventSource is what turned a slow handshake into a
      // permanent one. But not forever: a socket restored from a suspended
      // iOS tab can latch in CONNECTING and never resolve, and an
      // unconditional pass meant nothing could ever replace it.
      if (current && current.readyState === EventSource.CONNECTING
        && Date.now() - connectingSince < SILENCE_MS * 2) return;
      current?.close();
      // Give the new socket a full silence window to prove itself, or the
      // watchdog reads the OLD timestamp and kills it on the next tick.
      lastEventAt.current = Date.now();
      connectingSince = Date.now();

      const es = new EventSource(streamUrl, { withCredentials: true });
      const alive = () => {
        lastEventAt.current = Date.now();
        retryMs = RETRY_MIN_MS;
        setHealthy(true);
      };
      es.addEventListener('open', alive);
      // The server's liveness beat. It carries nothing and is not a frame --
      // it exists so a quiet game is distinguishable from a dead socket.
      es.addEventListener('ping', alive);
      es.addEventListener('state', (ev) => {
        alive();
        try {
          apply(JSON.parse((ev as MessageEvent).data) as T);
        } catch {
          /* a malformed frame is not worth blanking the screen */
        }
      });
      es.addEventListener('error', () => {
        setHealthy(false);
        // EventSource retries on its own while it can. CLOSED means the
        // browser has given up for good and nothing will ever arrive on it.
        if (es.readyState === EventSource.CLOSED) scheduleRetry();
      });
      esRef.current = es;
    };

    const scheduleRetry = () => {
      if (stopped || retryTimer !== null) return;
      const wait = retryMs;
      retryMs = Math.min(retryMs * 2, RETRY_MAX_MS);
      retryTimer = window.setTimeout(() => {
        retryTimer = null;
        connect();
        void poll();
      }, wait);
    };

    // Any recovery the browser hands us is worth taking at once, and it
    // resets the backoff: an access point coming back is new information, not
    // another failure. But a poll is always worth it and a RECONNECT is not
    // -- these fire on every unlock and tab switch, and rebuilding a socket
    // that is open and recently alive throws away a working connection to
    // re-enter the handshake window this file exists to stay out of.
    const revive = () => {
      if (stopped || document.hidden) return;
      retryMs = RETRY_MIN_MS;
      if (retryTimer !== null) {
        window.clearTimeout(retryTimer);
        retryTimer = null;
      }
      const es = esRef.current;
      const fresh = Date.now() - lastEventAt.current < SILENCE_MS;
      if (!es || es.readyState !== EventSource.OPEN || !fresh) connect();
      void poll();
    };

    connect();
    void poll();
    schedulePoll();

    // Silence is a signal, not an error event: a suspended EventSource
    // frequently LOOKS open and is dead. Retries go through the backoff so a
    // stall cannot become a teardown loop.
    const watchdog = window.setInterval(() => {
      if (Date.now() - lastEventAt.current > SILENCE_MS) {
        setHealthy(false);
        scheduleRetry();
      }
    }, WATCH_EVERY_MS);

    // The countdown check. See the constants above: a deadline that has
    // passed with no newer version is the one thing that says, positively,
    // that we are not hearing this game -- where silence only ever says we
    // might not be.
    const stallWatch = window.setInterval(() => {
      const deadline = deadlineRef.current;
      if (!deadline) {
        stallSince = 0;
        return;
      }
      const past = Date.now() + skewRef.current - deadline;
      if (past < STALL_GRACE_MS) {
        stallSince = 0;
        return;
      }
      if (stallSince === 0) stallSince = Date.now();
      const now = Date.now();
      if (now - lastStallPoll >= STALL_POLL_EVERY_MS) {
        lastStallPoll = now;
        setHealthy(false);
        void poll();
      }
      // Still nothing after a few seconds of asking: the socket is the
      // problem, not the server.
      if (now - stallSince > STALL_RECONNECT_AFTER_MS) scheduleRetry();
    }, 500);

    const onVisible = () => revive();
    document.addEventListener('visibilitychange', onVisible);
    // pageshow catches a bfcache restore, which mobile Safari does not
    // reliably report through visibilitychange; online catches the access
    // point coming back, which is most of what goes wrong in a bar.
    window.addEventListener('pageshow', revive);
    window.addEventListener('online', revive);
    window.addEventListener('focus', revive);

    reopenRef.current = () => {
      epochRef.current += 1;
      versionRef.current = -1;
      esRef.current?.close();
      esRef.current = null;
      revive();
    };

    return () => {
      stopped = true;
      window.clearInterval(watchdog);
      window.clearInterval(stallWatch);
      if (retryTimer !== null) window.clearTimeout(retryTimer);
      if (pollTimer !== null) window.clearTimeout(pollTimer);
      document.removeEventListener('visibilitychange', onVisible);
      window.removeEventListener('pageshow', revive);
      window.removeEventListener('online', revive);
      window.removeEventListener('focus', revive);
      esRef.current?.close();
    };
  }, [streamUrl, stateUrl, apply]);

  useEffect(() => {
    const tick = window.setInterval(() => {
      const d = deadlineRef.current;
      setMsLeft(d === null ? null : Math.max(0, d - (Date.now() + skewRef.current)));
    }, 100);
    return () => window.clearInterval(tick);
  }, []);

  const reopen = useCallback(() => reopenRef.current(), []);

  return { frame, connected, msLeft, apply, reopen };
}
