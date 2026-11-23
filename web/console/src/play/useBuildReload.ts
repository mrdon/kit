import { useFrameBuildReload } from '../live/useFrameBuildReload';
import { PHASE, type PlayerFrame } from './api';

// useBuildReload picks up a deploy on a phone that is already open (see
// live/useFrameBuildReload).
//
// A reload throws away whatever is in the answer box, so it never happens
// while a table might be typing: the three input phases wait, and the reload
// lands at the next phase change, which is at most one question away.
// Everywhere else — the board, a break, scoring, the mentions, the podium,
// the lobby — there is nothing to lose and it goes immediately.
const INPUT_PHASES: readonly string[] = [PHASE.QUESTION, PHASE.WAGER, PHASE.BETTING];

export function useBuildReload(frame: PlayerFrame | null) {
  useFrameBuildReload(frame, {
    hold: (f) => INPUT_PHASES.includes(f.phase),
    stage: (f) => f.phase,
  });
}
