import { useCallback, useEffect, useRef, useState } from 'react';
import { API_BASE } from '../../api';
import type { HostFrame } from './common';

// The host console's live connection. Same shape as the player's hook and for
// the same reasons: EventSource for free browser reconnect, an absolute
// deadline plus a per-frame skew ticked locally at 100ms, a watchdog on
// silence rather than on error events, and a poll fallback so a proxy eating
// SSE costs latency rather than a frozen console mid-question.
export interface HostStream {
  frame: HostFrame | null;
  connected: boolean;
  msLeft: number | null;
  apply: (f: HostFrame) => void;
}

export function useHostStream(gameId: string | undefined): HostStream {
  const [frame, setFrame] = useState<HostFrame | null>(null);
  const [connected, setConnected] = useState(false);
  const [msLeft, setMsLeft] = useState<number | null>(null);

  const versionRef = useRef(-1);
  const skewRef = useRef(0);
  const deadlineRef = useRef<number | null>(null);
  const lastFrameAt = useRef(Date.now());
  const esRef = useRef<EventSource | null>(null);
  const connectingSince = useRef(0);
  const bootBuild = useRef<string | null>(null);

  const apply = useCallback((next: HostFrame) => {
    if (next.version <= versionRef.current) return;
    versionRef.current = next.version;
    // A deploy while a game is running leaves this laptop driving the night
    // from the bundle it loaded an hour ago. That is how a host ended up with
    // no "Show the winner" button: the server had the awards phase, the
    // console did not, and primaryAction fell through to nothing. Unlike a
    // phone there is nothing half-typed to protect here.
    if (bootBuild.current === null) bootBuild.current = next.build ?? '';
    else if (next.build && next.build !== bootBuild.current) window.location.reload();
    skewRef.current = next.serverNow - Date.now();
    deadlineRef.current = next.deadlineMs || null;
    // Deliberately does NOT stamp lastFrameAt: that clock means "the SOCKET
    // is alive", and this runs for poll frames too. Stamping it here meant a
    // working poll hid a dead stream, which was then never retried.
    setFrame(next);
  }, []);

  const poll = useCallback(async () => {
    if (!gameId) return;
    const since = versionRef.current >= 0 ? `?since=${versionRef.current}` : '';
    try {
      const res = await fetch(`${API_BASE}/trivia/games/${gameId}/state${since}`, {
        credentials: 'same-origin',
      });
      if (res.status === 204 || !res.ok) return;
      apply((await res.json()) as HostFrame);
    } catch {
      /* the watchdog will retry */
    }
  }, [gameId, apply]);

  const connect = useCallback(() => {
    if (!gameId) return;
    const current = esRef.current;
    // Never tear down a socket that is still dialling -- closing a CONNECTING
    // EventSource every watchdog tick is what stopped a slow handshake from
    // ever finishing -- but not forever, or a socket latched in CONNECTING
    // could never be replaced.
    if (current && current.readyState === EventSource.CONNECTING
      && Date.now() - connectingSince.current < 40_000) return;
    current?.close();
    // Give the new socket a full silence window to prove itself.
    lastFrameAt.current = Date.now();
    connectingSince.current = Date.now();
    const es = new EventSource(`${API_BASE}/trivia/games/${gameId}/stream`, {
      withCredentials: true,
    });
    es.addEventListener('state', (ev) => {
      setConnected(true);
      try {
        apply(JSON.parse((ev as MessageEvent).data) as HostFrame);
      } catch {
        /* ignore a malformed frame */
      }
    });
    es.addEventListener('open', () => {
      setConnected(true);
      lastFrameAt.current = Date.now();
    });
    // The server's liveness beat — see web_stream.go. A quiet game (a break,
    // an emptied board waiting on the host) publishes nothing, and without
    // this the silence watchdog reads that as a dead socket and reconnects
    // every twenty seconds while the host is mid-sentence.
    es.addEventListener('ping', () => {
      setConnected(true);
      lastFrameAt.current = Date.now();
    });
    es.addEventListener('error', () => setConnected(false));
    esRef.current = es;
  }, [gameId, apply]);

  useEffect(() => {
    if (!gameId) return;
    versionRef.current = -1;
    connect();
    void poll();
    const watchdog = window.setInterval(() => {
      if (Date.now() - lastFrameAt.current > 20_000) {
        setConnected(false);
        connect();
        void poll();
      }
    }, 5_000);
    const poller = window.setInterval(() => void poll(), 5_000);
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
  }, [gameId, connect, poll]);

  useEffect(() => {
    const tick = window.setInterval(() => {
      const d = deadlineRef.current;
      setMsLeft(d === null ? null : Math.max(0, d - (Date.now() + skewRef.current)));
    }, 100);
    return () => window.clearInterval(tick);
  }, []);

  return { frame, connected, msLeft, apply };
}
