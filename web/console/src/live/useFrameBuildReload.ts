import { useEffect, useRef } from 'react';

// useFrameBuildReload picks up a deploy on a live-game page that is already
// open, off the build token every frame carries.
//
// The first frame a page sees is the build it booted with; any later frame
// carrying a different one means the server has been replaced underneath it,
// and the bundle in this browser is the old one. That is exactly the
// situation a fix shipped mid-game has to survive — a bug found halfway
// through the night is no use fixed on a server that twenty stale phones are
// not talking to.
//
// WHEN it reloads is the caller's call, through `hold`. A reload throws away
// whatever is half-typed, so a phone holds it while someone might be entering
// something, and the reload lands at the next change of `stage` instead —
// which costs nothing because the screen is being replaced at that moment
// anyway. With no `hold` it reloads at once, which is right for a host, who
// has nothing half-typed to protect and is the one person who cannot afford
// to be a version behind.
export interface BuildFrame {
  build?: string;
}

export interface BuildReloadOptions<T> {
  // hold reports that reloading now could throw away someone's input.
  hold?: (frame: T) => boolean;
  // stage names the screen the frame is on; a reload held by `hold` goes
  // ahead as soon as it changes.
  stage?: (frame: T) => string;
}

export function useFrameBuildReload<T extends BuildFrame>(
  frame: T | null,
  { hold, stage }: BuildReloadOptions<T> = {},
) {
  const booted = useRef<string | null>(null);
  const lastStage = useRef<string | null>(null);
  // Latched on purpose: once a newer build has been seen, a later frame that
  // somehow carries the old token again (a straggler from a container being
  // drained) must not cancel the reload.
  const stale = useRef(false);

  useEffect(() => {
    if (!frame?.build) return;
    const now = stage ? stage(frame) : '';
    if (booted.current === null) {
      booted.current = frame.build;
      lastStage.current = now;
      return;
    }
    if (frame.build !== booted.current) stale.current = true;

    const stageChanged = now !== lastStage.current;
    lastStage.current = now;
    if (!stale.current) return;
    if (!hold?.(frame) || stageChanged) {
      window.location.reload();
    }
    // hold and stage are read fresh on every frame; only a new frame re-runs.
  }, [frame]);
}
