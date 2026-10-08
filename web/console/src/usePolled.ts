import { useCallback, useEffect, useRef } from 'react';

// usePolled keeps a page's data current the way the wall displays keep
// theirs: by asking again on a timer, and the moment the page is looked at
// again. A device page is left open for a shift, so what it showed at
// 4pm is not what is true at 7pm: happy hour was ended from Slack, a
// screen was repointed from the console, a description was typed on the
// other iPad. The displays poll a tiny .version endpoint and reload; these
// payloads are small enough that re-fetching the data itself is simpler
// and no more traffic.
//
// Pages that hold edits in progress merge rather than replace, so a poll
// never wipes what somebody is halfway through typing.
//
// Pauses while the tab is hidden (an iPad on the home screen, a laptop
// lid closed) and fires once as soon as it is visible again, which is the
// case that matters: the device woke up and the first thing it shows must
// be true now, not when it slept.
export function usePolled(load: () => void | Promise<unknown>, everyMs = 15000): () => void {
  const loadRef = useRef(load);
  loadRef.current = load;
  const run = useCallback(() => {
    void loadRef.current();
  }, []);

  useEffect(() => {
    run();
    let timer: number | null = null;
    const start = () => {
      if (timer === null) timer = window.setInterval(run, everyMs);
    };
    const stop = () => {
      if (timer !== null) {
        window.clearInterval(timer);
        timer = null;
      }
    };
    const onVisible = () => {
      if (document.visibilityState === 'visible') {
        run();
        start();
      } else {
        stop();
      }
    };
    if (document.visibilityState === 'visible') start();
    document.addEventListener('visibilitychange', onVisible);
    window.addEventListener('focus', run);
    return () => {
      stop();
      document.removeEventListener('visibilitychange', onVisible);
      window.removeEventListener('focus', run);
    };
  }, [run, everyMs]);

  return run;
}
