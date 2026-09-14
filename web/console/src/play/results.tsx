// The two screens that tell a table how it did: the round's scoring beat and
// the podium at the end of the night.
//
// They live apart from App.tsx because they are the celebration surface — the
// one part of the phone that is allowed to be loud — and because App.tsx is
// the router, which should stay legible as a router.

import { useEffect, useState } from 'react';
import { money, type PlayerFrame } from './api';

// The delta is the hero, counted up, because "what did that round do to us"
// is the only question anybody has at this moment.
export function Result({ frame }: { frame: PlayerFrame }) {
  const target = frame.you?.delta ?? 0;
  const shown = useCountUp(target, frame.round?.id ?? '');
  const cls = target > 0 ? 'delta up' : target < 0 ? 'delta down' : 'delta flat';
  return (
    <div className="body">
      <p className="sub" style={{ textAlign: 'center' }}>The answer was</p>
      <h1 style={{ textAlign: 'center' }}>{frame.scoring?.correctText || frame.scoring?.correctValue}</h1>
      <div className={cls}>{target > 0 ? '+' : ''}{money(shown)}</div>
      {frame.you?.wroteWinner ? (
        <p className="sub" style={{ textAlign: 'center' }}>You wrote the winning answer.</p>
      ) : null}
      <Standings frame={frame} />
    </div>
  );
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
