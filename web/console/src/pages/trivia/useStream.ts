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

  const apply = useCallback((next: HostFrame) => {
    if (next.version <= versionRef.current) return;
    versionRef.current = next.version;
    skewRef.current = next.serverNow - Date.now();
    deadlineRef.current = next.deadlineMs || null;
    lastFrameAt.current = Date.now();
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
    esRef.current?.close();
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
