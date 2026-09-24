import { useEffect, useRef } from 'react';
import { PHASE, type PlayerFrame } from './api';

// useBuildReload picks up a deploy on a phone that is already open.
//
// Every frame carries the server's build token (wireCommon.Build). The first
// frame a phone sees is the build it booted with; any later frame carrying a
// different one means the server has been replaced underneath it, and the
// bundle in this browser is the old one. That is exactly the situation a fix
// shipped during a quiz has to survive — a bug found at question four is no
// use fixed on a server that twenty stale phones are not talking to.
//
// WHEN it reloads is the whole design. A reload throws away whatever is in
// the answer box, so it never happens while a table might be typing: the
// three input phases wait, and the reload lands at the next phase change,
// which is at most one question away and costs nothing because the screen is
// being replaced at that moment anyway. Everywhere else — the board, a break,
// scoring, the mentions, the podium, the lobby — there is nothing to lose and
// it goes immediately.
const INPUT_PHASES: readonly string[] = [PHASE.QUESTION, PHASE.WAGER, PHASE.BETTING];

export function useBuildReload(frame: PlayerFrame | null) {
  const booted = useRef<string | null>(null);
  const lastPhase = useRef<string | null>(null);
  // Latched on purpose: once a newer build has been seen, a later frame that
  // somehow carries the old token again (a straggler from a container being
  // drained) must not cancel the reload.
  const stale = useRef(false);

  useEffect(() => {
    if (!frame?.build) return;
    if (booted.current === null) {
      booted.current = frame.build;
      lastPhase.current = frame.phase;
      return;
    }
    if (frame.build !== booted.current) stale.current = true;

    const phaseChanged = frame.phase !== lastPhase.current;
    lastPhase.current = frame.phase;
    if (!stale.current) return;
    if (!INPUT_PHASES.includes(frame.phase) || phaseChanged) {
      window.location.reload();
    }
  }, [frame]);
}
