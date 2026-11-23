import { useEffect, useRef } from 'react';
import { fxConfetti, fxPop } from './art/fx.js';
import { HOP } from './consts.js';
import type { Frame } from './api';
import { buzz } from './buzz';
import { myHeat } from './screens';

// useMoments is the phone's reactions to the game moving: a buzz when your
// turn or your heat comes up, confetti for points, and the word that pops up
// when your frog moves ("+2", "Carried 1", "Nice flick +1"). Each one fires
// once per thing that happened, keyed so a re-sent frame never repeats it.
//
// It returns the time the FLICK! screen will be gone, so a hop's word waits
// for the frog to have flown off before it appears.
export function useMoments(frame: Frame | null, id: string) {
  const flickHideAt = useRef(0);
  const lastHop = useRef(0);
  const lastDeck = useRef('');
  const lastTurn = useRef('');
  const celebrated = useRef('');
  const won = useRef(false);

  useEffect(() => {
    if (!frame || !id) return;
    const turnKey = `${frame.round}-${frame.heat}-${frame.turn}`;
    if (frame.phase === HOP.phase.racing && frame.sub === HOP.sub.flick) {
      if (frame.current === id && lastTurn.current !== turnKey) {
        lastTurn.current = turnKey;
        buzz([200, 80, 200]);
      } else if (frame.onDeck === id && lastDeck.current !== turnKey) {
        lastDeck.current = turnKey;
        buzz(80);
      }
    }
    const h = myHeat(frame, id);
    if (h && h.status === HOP.heat.next && lastDeck.current !== `heat-${frame.round}-${h.num}`) {
      lastDeck.current = `heat-${frame.round}-${h.num}`;
      buzz([120, 60, 120]);
    }
    const res = (h?.results ?? []).find((r) => r.player === id);
    if (res) {
      const key = `${frame.round}-${res.place}-${res.points}`;
      if (celebrated.current !== key && res.points) {
        buzz([60, 40, 60]);
        fxConfetti(res.place === 1 ? 90 : 35);
      }
      celebrated.current = key;
    }
    if (frame.phase === HOP.phase.final && frame.champion === id && !won.current) {
      won.current = true;
      fxConfetti(160);
      buzz([100, 60, 100, 60, 200]);
    }
    hopWord(frame, id, lastHop, flickHideAt.current);
  }, [frame, id]);

  return flickHideAt;
}

// When my frog moves (hopped or carried), say so, after the FLICK! screen is
// gone. The first hop a phone sees is history, not news: it says nothing.
function hopWord(frame: Frame, id: string, last: { current: number }, hideAt: number) {
  const h = frame.lastHop;
  if (!h || h.seq <= last.current) return;
  const first = !last.current;
  last.current = h.seq;
  if (first || !h.moved.includes(id)) return;
  const mine = h.frog === id;
  const txt = mine ? (h.back ? `Back ${h.roll}` : `+${h.roll}`) : h.back ? `Dragged back ${h.roll}` : `Carried ${h.roll}`;
  window.setTimeout(() => {
    if (mine && h.perfect) {
      // the engine sets perfect only when the +1 was won
      const f = document.getElementById('meFace');
      if (f) {
        f.classList.remove('sparkle');
        void f.offsetWidth;
        f.classList.add('sparkle');
      }
      buzz([30, 40, 90]);
      fxPop(window.innerWidth / 2, window.innerHeight * 0.3, 'Nice flick +1', 34, '#f5c451');
    } else {
      fxPop(window.innerWidth / 2, window.innerHeight * 0.3, txt, 40, h.back ? '#ffb4a3' : '#4ec9a5');
    }
  }, Math.max(0, hideAt - Date.now()));
}
