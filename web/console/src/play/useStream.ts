import { useEffect } from 'react';
import { useLiveStream, type LiveStream } from '../liveStream';
import { base, type PlayerFrame } from './api';

// The phone's connection is the shared one in ../liveStream: everything about
// reconnecting, polling, the silence watchdog and the stuck-countdown check
// lives there and is shared with the host console. What is left here is what
// is genuinely the PHONE's -- its URLs, and when it is safe to reload it.

export type Stream = LiveStream<PlayerFrame>;

export function useStream(): Stream {
  return useLiveStream<PlayerFrame>({
    streamUrl: base + '/stream',
    stateUrl: base + '/state',
  });
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
