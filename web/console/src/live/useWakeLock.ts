import { useEffect } from 'react';

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
