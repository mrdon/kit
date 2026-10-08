import { useEffect, useRef } from 'react';

// useBuildReload picks up a deploy in a console tab that is already open.
//
// The console is a single-page app: once loaded, nothing fetches HTML
// again, so a tab left open through a deploy keeps running the bundle it
// booted with until somebody reloads it. A device page is left open for a
// whole shift, and the question "why does the list still say Run it" is
// what that looks like from the bar. The trivia phone fixes the same thing
// off its frames (play/useBuildReload); the console has no stream, so it
// reads the build token /api/me carries, which the shell re-fetches on the
// same cadence the device pages poll on.
//
// It never reloads under somebody's typing: with a text field focused the
// reload waits for the next check, by which time the field has usually
// been submitted or abandoned.
export function useBuildReload(build: string | undefined) {
  const booted = useRef<string | null>(null);
  // Latched: a straggling response from the old container must not cancel
  // a reload a newer one already called for.
  const stale = useRef(false);

  useEffect(() => {
    if (!build) return;
    if (booted.current === null) {
      booted.current = build;
      return;
    }
    if (build !== booted.current) stale.current = true;
    if (!stale.current) return;
    const el = document.activeElement;
    const typing = el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement;
    if (!typing) window.location.reload();
  }, [build]);
}
