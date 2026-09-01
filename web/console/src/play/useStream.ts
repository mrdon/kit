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

const WATCHDOG_MS = 20_000;
const POLL_MS = 5_000;

export function useStream(): Stream {
  const [frame, setFrame] = useState<PlayerFrame | null>(null);
  const [connected, setConnected] = useState(false);
  const [msLeft, setMsLeft] = useState<number | null>(null);

  const versionRef = useRef(-1);
  const skewRef = useRef(0);
  const deadlineRef = useRef<number | null>(null);
  const lastFrameAt = useRef(Date.now());
  const esRef = useRef<EventSource | null>(null);

  const apply = useCallback((next: PlayerFrame) => {
    // A stale frame never repaints the screen backwards.
    if (next.version <= versionRef.current) return;
    versionRef.current = next.version;
    // Taking the latest sample folds one-way delay in as a conservative bias,
    // so the phone runs slightly AHEAD of the server — the right direction to
    // be wrong in when somebody is deciding whether they have time to retype.
    skewRef.current = next.serverNow - Date.now();
    deadlineRef.current = next.deadlineMs || null;
    lastFrameAt.current = Date.now();
    setFrame(next);
  }, []);

  const connect = useCallback(() => {
    esRef.current?.close();
    const es = new EventSource(base + '/stream', { withCredentials: true });
    es.addEventListener('state', (ev) => {
      setConnected(true);
      try {
        apply(JSON.parse((ev as MessageEvent).data) as PlayerFrame);
      } catch {
        /* a malformed frame is not worth blanking the phone */
      }
    });
    es.addEventListener('open', () => {
      setConnected(true);
      lastFrameAt.current = Date.now();
    });
    es.addEventListener('error', () => setConnected(false));
    esRef.current = es;
  }, [apply]);

  const poll = useCallback(async () => {
    const since = versionRef.current >= 0 ? `?since=${versionRef.current}` : '';
    try {
      const res = await fetch(base + '/state' + since, { credentials: 'same-origin' });
      if (res.status === 204 || !res.ok) return;
      apply((await res.json()) as PlayerFrame);
    } catch {
      /* offline; the watchdog will retry */
    }
  }, [apply]);

  useEffect(() => {
    connect();
    void poll();

    // Silence is the signal, not an error event: a suspended iOS EventSource
    // frequently LOOKS open and is dead.
    const watchdog = window.setInterval(() => {
      if (Date.now() - lastFrameAt.current > WATCHDOG_MS) {
        setConnected(false);
        connect();
        void poll();
      }
    }, 5_000);
    // The poll fallback keeps the phone playable at a few seconds of latency
    // when a captive portal or proxy is eating SSE.
    const poller = window.setInterval(() => void poll(), POLL_MS);

    const onVisible = () => {
      if (!document.hidden) {
        connect();
        void poll();
      }
    };
    document.addEventListener('visibilitychange', onVisible);

    return () => {
      window.clearInterval(watchdog);
      window.clearInterval(poller);
      document.removeEventListener('visibilitychange', onVisible);
      esRef.current?.close();
    };
  }, [connect, poll]);

  useEffect(() => {
    const tick = window.setInterval(() => {
      const d = deadlineRef.current;
      setMsLeft(d === null ? null : Math.max(0, d - (Date.now() + skewRef.current)));
    }, 100);
    return () => window.clearInterval(tick);
  }, []);

  // reopen resets the version floor as well as the connection: the identity
  // has changed, so the next frame is new information even at a version this
  // client has already seen.
  const reopen = useCallback(() => {
    versionRef.current = -1;
    connect();
    void poll();
  }, [connect, poll]);

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
