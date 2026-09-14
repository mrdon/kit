// The two screens that tell a table how it did: the round's scoring beat and
// the podium at the end of the night.
//
// They live apart from App.tsx because they are the celebration surface — the
// one part of the phone that is allowed to be loud — and because App.tsx is
// the router, which should stay legible as a router.

import { useEffect, useRef, useState } from 'react';
import { money, type PlayerFrame } from './api';
import { confetti } from './confetti';

// The delta is the hero, counted up, because "what did that round do to us"
// is the only question anybody has at this moment. The celebration is layered
// OVER that, never in place of it: a table that just took $600 off the room
// wants the number first and the fuss second.
export function Result({ frame }: { frame: PlayerFrame }) {
  const target = frame.you?.delta ?? 0;
  const roundId = frame.round?.id ?? '';
  const shown = useCountUp(target, roundId);
  const cls = target > 0 ? 'delta up' : target < 0 ? 'delta down' : 'delta flat';
  const wrote = !!frame.you?.wroteWinner;
  const top = topEarner(frame);

  // Writing the winning answer is the round's big moment, so it gets the
  // confetti. Being top earner without writing it is the quieter one, so it
  // gets a gold wash and a badge -- two showers a round would stop meaning
  // anything by the third question.
  useOncePerKey(roundId, wrote, () => confetti({ durationMs: 2400, count: 90 }));

  return (
    <div className={`body ${top && !wrote ? 'gold-flash' : ''}`}>
      <p className="sub" style={{ textAlign: 'center' }}>The answer was</p>
      <h1 style={{ textAlign: 'center' }}>{frame.scoring?.correctText || frame.scoring?.correctValue}</h1>
      <div className={cls}>{target > 0 ? '+' : ''}{money(shown)}</div>
      {wrote ? <p className="celebrate-line">You nailed it</p> : null}
      {top ? <div className="badge-gold">Top earner this round</div> : null}
      {wrote ? (
        <p className="sub" style={{ textAlign: 'center' }}>You wrote the winning answer.</p>
      ) : null}
      <Standings frame={frame} />
    </div>
  );
}

// topEarner: did this table take more off the round than anybody else?
//
// Ties count, deliberately. Two tables that both swung +$400 both earned the
// most, and picking one of them by map order would be arbitrary in a way the
// room can check against the wall. A flat or negative round is never a "top
// earner", however far ahead of everyone else it is -- the badge is about the
// round, not the standings.
function topEarner(frame: PlayerFrame): boolean {
  const deltas = frame.scoring?.deltas;
  const teamId = frame.you?.teamId;
  if (!deltas || !teamId) return false;
  const mine = deltas[teamId];
  if (typeof mine !== 'number' || mine <= 0) return false;
  for (const id of Object.keys(deltas)) {
    if (deltas[id] > mine) return false;
  }
  return true;
}

// useOncePerKey fires an effect at most once for a given key, and cleans up
// whatever it returns.
//
// The guard is the whole point. Every reconnect re-delivers the current
// frame, and a phone that locks and wakes gets the scoring frame again -- a
// second confetti burst over a screen the table has been reading for twenty
// seconds reads as a glitch rather than as a celebration. The ref survives
// re-renders; the key is the round (or the phase) so the NEXT round still
// fires.
function useOncePerKey(key: string, armed: boolean, fire: () => (() => void) | void) {
  const firedFor = useRef<string | null>(null);
  useEffect(() => {
    if (!armed || !key || firedFor.current === key) return;
    firedFor.current = key;
    return fire();
    // `fire` is a fresh closure every render and is deliberately not a
    // dependency: it would re-run this on every frame that lands.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, armed]);
}

export function Podium({ frame }: { frame: PlayerFrame }) {
  const sorted = [...frame.teams].sort((a, b) => b.score - a.score);
  const winner = sorted[0];
  return (
    <div className="body">
      <h1>{winner ? `${winner.name} wins` : 'That’s the game'}</h1>
      <Standings frame={frame} />
    </div>
  );
}

export function Standings({ frame }: { frame: PlayerFrame }) {
  const sorted = [...frame.teams].sort((a, b) => b.score - a.score);
  return (
    <div className="board-list">
      {sorted.map((t, i) => (
        <div key={t.id} className={`brow ${t.id === frame.you?.teamId ? 'you' : ''}`}>
          <span className="rank">{i + 1}</span>
          <span>{t.name}</span>
          <span className="sc">{money(t.score)}</span>
        </div>
      ))}
    </div>
  );
}

// useCountUp animates a number toward its target with rAF. Resets whenever
// the round changes so a new delta counts from zero rather than from the last
// round's number.
function useCountUp(target: number, key: string): number {
  const [value, setValue] = useState(0);
  useEffect(() => {
    setValue(0);
    let raf = 0;
    const start = performance.now();
    const step = (now: number) => {
      const t = Math.min(1, (now - start) / 900);
      setValue(Math.round(target * (1 - Math.pow(1 - t, 3))));
      if (t < 1) raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [target, key]);
  return value;
}
