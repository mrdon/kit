import { useCallback, useEffect, useRef, useState } from 'react';
import { base, type PlayerFrame } from './api';

// useStream owns the live connection and the clock.
//
// EventSource rather than the shared readSSE helper: these endpoints are GET,
// so EventSource gives browser exponential reconnect for free and — the
// deciding fact — sends cookies automatically while being unable to set
// headers, which is exactly why the team identity is a cookie in the first
// place.
export interface Stream {
  frame: PlayerFrame | null;
  connected: boolean;
  // msLeft is the countdown, ticked LOCALLY at 100ms from an absolute
  // deadline and a per-frame skew. Countdown ticks are never sent over the
  // wire; that would put the clock on bar wifi.
  msLeft: number | null;
  apply: (f: PlayerFrame) => void;
  // reopen drops the stream and starts a new one. MUST be called after a join
  // or a reclaim: EventSource sends whatever cookies exist AT CONNECT TIME,
  // so a stream opened before the identity cookie existed keeps arriving as a
  // spectator forever — and because its frames carry ever-newer versions,
  // they suppress the poll frames that would have carried the private block.
  // The phone would sit on the join screen for the whole game.
  reopen: () => void;
}

// The server beats every 8s (pingEvery in web_stream.go), so nothing at all
// on the socket for two and a half beats means dead rather than quiet.
const SILENCE_MS = 20_000;
// How often the watchdog LOOKS. Looking often is free; it is SILENCE_MS that
// decides whether a socket is dead, and these two were conflated before.
const WATCH_EVERY_MS = 2_000;
// The poll is slow while the stream is healthy and fast while it is not,
// because while it is not, the poll IS the game.
// 30s while healthy. At 5s, twenty phones plus the wall and the console were
// asking for a full snapshot four or five times a second — eight to ten
// queries each, against a pool that defaults to four connections — purely to
// re-fetch state the stream had already delivered. The stream is the
// transport; this is the net under it.
const POLL_OK_MS = 30_000;
const POLL_DOWN_MS = 2_000;
// Reconnect backoff.
//
// This is the fix for the freeze, and the floor matters more than the
// ceiling. The old watchdog called connect() every 5s for as long as the
// stream was silent, and connect() closed whatever was there first — so a
// handshake that needed six seconds on bar wifi was torn down at five, every
// five seconds, forever. The recovery path was what prevented recovery, and
// the only way out was the reload the room was doing by hand.
const RETRY_MIN_MS = 1_000;
const RETRY_MAX_MS = 15_000;

export function useStream(): Stream {
  const [frame, setFrame] = useState<PlayerFrame | null>(null);
  const [connected, setConnected] = useState(false);
  const [msLeft, setMsLeft] = useState<number | null>(null);

  const versionRef = useRef(-1);
  const skewRef = useRef(0);
  const deadlineRef = useRef<number | null>(null);
  // Any traffic at all: a frame, a ping, or an open. Silence on THIS is what
  // the watchdog reads, and a connect attempt stamps it so a socket that is
  // still coming up is never mistaken for one that has died.
  const lastEventAt = useRef(Date.now());
  const esRef = useRef<EventSource | null>(null);
  const reopenRef = useRef<() => void>(() => undefined);
  // Bumped by reopen(). A request issued under an older identity must not be
  // applied after a newer one exists — see poll().
  const epochRef = useRef(0);

  const apply = useCallback((next: PlayerFrame) => {
    // A stale frame never repaints the screen backwards.
    if (next.version <= versionRef.current) return;
    versionRef.current = next.version;
    // Taking the latest sample folds one-way delay in as a conservative bias,
    // so the phone runs slightly AHEAD of the server — the right direction to
    // be wrong in when somebody is deciding whether they have time to retype.
    skewRef.current = next.serverNow - Date.now();
    deadlineRef.current = next.deadlineMs || null;
    // Deliberately does NOT stamp lastEventAt. That clock means "the SOCKET
    // is alive", and apply runs for poll frames too — so a working poll used
    // to refresh it, which meant a dead stream was never detected, never
    // retried, and the phone ran on polling for the rest of the night.
    setFrame(next);
  }, []);

  useEffect(() => {
    let stopped = false;
    let retryMs = RETRY_MIN_MS;
    let retryTimer: number | null = null;
    let pollTimer: number | null = null;
    let healthy = false;
    let connectingSince = 0;

    const poll = async () => {
      const since = versionRef.current >= 0 ? `?since=${versionRef.current}` : '';
      // A poll issued as a SPECTATOR and answered after this table joined
      // carries the spectator projection (no `you`) at the version the join
      // itself produced. Applied, it took the version floor with it, so the
      // rejoined stream's first frame — same version, but WITH `you` — was
      // dropped as stale and the table sat on the join screen until something
      // else moved the game on. Tapping join again then said the name was
      // taken. So: a response from before the identity changed is discarded.
      const epoch = epochRef.current;
      try {
        const res = await fetch(base + '/state' + since, { credentials: 'same-origin' });
        if (res.status === 204 || !res.ok) return;
        const next = (await res.json()) as PlayerFrame;
        if (epoch !== epochRef.current) return;
        apply(next);
      } catch {
        /* offline; the watchdog and the next tick will retry */
      }
    };

    // The poll runs on its own cadence and speeds up when the stream is down,
    // rescheduling itself rather than sitting on one fixed interval — a phone
    // with a dead socket should be a couple of seconds behind, not frozen.
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
      // Never tear down a socket that is still trying to come up — closing a
      // CONNECTING EventSource is what turned a slow handshake into a
      // permanent one. But not FOREVER: an iOS socket restored from a
      // suspended tab can latch in CONNECTING and never resolve, and an
      // unconditional pass meant nothing could ever replace it. The phone
      // then ran on the fallback poll for the rest of the night, showing
      // "reconnecting" the whole time. Past twice the silence window it has
      // had every chance.
      if (current && current.readyState === EventSource.CONNECTING
        && Date.now() - connectingSince < SILENCE_MS * 2) return;
      current?.close();
      connectingSince = Date.now();
      // Give the new socket a full silence window to prove itself. Without
      // this the watchdog sees the OLD stale timestamp and kills it on the
      // next tick.
      lastEventAt.current = Date.now();

      const es = new EventSource(base + '/stream', { withCredentials: true });
      const alive = () => {
        lastEventAt.current = Date.now();
        retryMs = RETRY_MIN_MS;
        setHealthy(true);
      };
      es.addEventListener('open', alive);
      // The server's liveness beat. It carries nothing and is not a frame —
      // it exists so that a quiet game is distinguishable from a dead socket.
      es.addEventListener('ping', alive);
      es.addEventListener('state', (ev) => {
        alive();
        try {
          apply(JSON.parse((ev as MessageEvent).data) as PlayerFrame);
        } catch {
          /* a malformed frame is not worth blanking the phone */
        }
      });
      es.addEventListener('error', () => {
        setHealthy(false);
        // EventSource retries on its own while it can. CLOSED means the
        // browser has given up for good, and nothing will ever arrive on it.
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

    // Any recovery the browser hands us is worth taking immediately, and it
    // resets the backoff: coming back from a locked screen or a dropped
    // access point is new information, not another failure.
    const revive = () => {
      if (stopped || document.hidden) return;
      retryMs = RETRY_MIN_MS;
      if (retryTimer !== null) {
        window.clearTimeout(retryTimer);
        retryTimer = null;
      }
      // A poll is always worth it; a reconnect is not. These fire on every
      // unlock, tab switch and keyboard dismissal, and rebuilding a socket
      // that is OPEN and has heard from the server recently throws away a
      // working connection and re-enters the multi-second handshake window
      // this whole file exists to stay out of.
      const es = esRef.current;
      const fresh = Date.now() - lastEventAt.current < SILENCE_MS;
      if (!es || es.readyState !== EventSource.OPEN || !fresh) connect();
      void poll();
    };
    const onVisible = () => revive();

    connect();
    void poll();
    schedulePoll();

    // Silence is the signal, not an error event: a suspended iOS EventSource
    // frequently LOOKS open and is dead. Retries go through the backoff so a
    // stall cannot become a teardown loop.
    const watchdog = window.setInterval(() => {
      if (Date.now() - lastEventAt.current > SILENCE_MS) {
        setHealthy(false);
        scheduleRetry();
      }
    }, WATCH_EVERY_MS);

    document.addEventListener('visibilitychange', onVisible);
    // pageshow catches a bfcache restore, which on mobile Safari does not
    // reliably fire visibilitychange; online catches the access point coming
    // back, which is most of what goes wrong in a bar.
    window.addEventListener('pageshow', revive);
    window.addEventListener('online', revive);
    window.addEventListener('focus', revive);

    reopenRef.current = () => {
      // The identity has changed, so the next frame is new information even
      // at a version this client has already seen, and anything already in
      // flight under the old identity is now worthless.
      epochRef.current += 1;
      versionRef.current = -1;
      esRef.current?.close();
      esRef.current = null;
      revive();
    };

    return () => {
      stopped = true;
      window.clearInterval(watchdog);
      if (retryTimer !== null) window.clearTimeout(retryTimer);
      if (pollTimer !== null) window.clearTimeout(pollTimer);
      document.removeEventListener('visibilitychange', onVisible);
      window.removeEventListener('pageshow', revive);
      window.removeEventListener('online', revive);
      window.removeEventListener('focus', revive);
      esRef.current?.close();
    };
  }, [apply]);

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

// useWakeLock keeps the screen on for the length of a game. Without it twenty
// people unlock their phones every ninety seconds, which is the difference
// between a smooth game and a fiddly one. Guarded and re-requested on
// visibilitychange, because the lock is dropped whenever the tab is hidden.
export function useWakeLock() {
  useEffect(() => {
    let lock: WakeLockSentinel | null = null;
    let cancelled = false;

    const request = async () => {
      try {
        if (!('wakeLock' in navigator) || document.hidden) return;
        lock = await navigator.wakeLock.request('screen');
      } catch {
        /* denied, unsupported, or battery saver — not worth surfacing */
      }
    };
    void request();

    const onVisible = () => {
      if (!document.hidden && !cancelled) void request();
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      cancelled = true;
      document.removeEventListener('visibilitychange', onVisible);
      void lock?.release().catch(() => undefined);
    };
  }, []);
}
