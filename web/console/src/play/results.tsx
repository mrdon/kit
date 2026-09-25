// The two screens that tell a table how it did: the round's scoring beat and
// the podium at the end of the night.
//
// They live apart from App.tsx because they are the celebration surface — the
// one part of the phone that is allowed to be loud — and because App.tsx is
// the router, which should stay legible as a router.

import { useEffect, useRef, useState } from 'react';
import { money, type PlayerFrame } from './api';
import { confetti } from './confetti';
import { RateNight } from './feedback';

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
      {top && !wrote ? <div className="badge-gold">Top earner this round</div> : null}
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

// The honorable mentions, above the standings on every phone.
//
// The whole list, not just this table's own. A table that won one wants to
// see it named next to the others, and a table that won nothing still wants
// to know who took "wildest guesses" -- showing each phone only its own would
// turn a shared bit of the night into twenty private ones.
//
// The card for your own table is marked, because on a phone at a dark table
// you are scanning for your name and nothing else.
function Awards({ frame }: { frame: PlayerFrame }) {
  if (!frame.awards?.length) return null;
  const mine = frame.you?.teamId;
  return (
    <div className="awards">
      <h2 className="awards-head">Honorable mentions</h2>
      {frame.awards.map((a) => (
        <div key={a.key} className={`award${a.teamId === mine ? ' mine' : ''}`}>
          <div className="award-title">{a.title}</div>
          <div className="award-team">{a.teamName}</div>
          <div className="award-detail">{a.detail}</div>
        </div>
      ))}
    </div>
  );
}

// The mentions, while the host reads them out and before the winner is up.
//
// No standings on this screen, deliberately. They are public all night and
// every table has seen them, but putting them here would let a phone answer
// the question the wall is three sentences away from answering, and the
// whole point of the extra press is that the room finds out together.
export function Mentions({ frame }: { frame: PlayerFrame }) {
  const mine = frame.you?.teamId;
  const won = frame.awards?.find((a) => a.teamId === mine);
  return (
    <div className="body">
      <h1>Honorable mentions</h1>
      {won ? (
        <p className="celebrate-line">You got one: {won.title}</p>
      ) : (
        <p className="sub" style={{ textAlign: 'center' }}>The winner is next.</p>
      )}
      <Awards frame={frame} />
      {/* Asked HERE rather than only on the podium. The host is reading the
          mentions out and every table has nothing to do but listen, which is
          the best moment of the night to be handed five stars -- and on the
          podium the room is already standing up. RateNight remembers per game,
          so a table that rates here sees "thanks" there. */}
      <RateNight game={frame.game} />
    </div>
  );
}

// The podium. The winning table's phone is the one screen in the room that
// should be impossible to mistake for anybody else's, so it gets a shower
// that keeps falling for six seconds -- long enough that the table next to
// them looks over, which is the whole point of putting it on the phone rather
// than only on the wall.
export function Podium({ frame }: { frame: PlayerFrame }) {
  const sorted = [...frame.teams].sort((a, b) => b.score - a.score);
  const winner = sorted[0];
  const place = podiumPlace(frame, sorted);

  // Keyed on the phase rather than a round: there is only ever one podium, and
  // the frame for it arrives again on every reconnect.
  useOncePerKey(
    place > 0 ? `podium:${place}` : '',
    place > 0,
    () => (place === 1
      ? confetti({ durationMs: 6000, count: 140 })
      : confetti({ durationMs: 1800, count: 45 })),
  );

  if (place === 1) {
    return (
      <div className="body">
        <p className="celebrate-line big">You won</p>
        <p className="sub" style={{ textAlign: 'center' }}>{money(winner.score)}. That&rsquo;s the game.</p>
        <Awards frame={frame} />
        <Standings frame={frame} />
        <RateNight game={frame.game} />
      </div>
    );
  }
  if (place === 2 || place === 3) {
    return (
      <div className="body">
        <p className="celebrate-line big">{place === 2 ? '2nd place' : '3rd place'}</p>
        <p className="sub" style={{ textAlign: 'center' }}>{winner.name} wins the night.</p>
        <Awards frame={frame} />
        <Standings frame={frame} />
        <RateNight game={frame.game} />
      </div>
    );
  }
  return (
    <div className="body">
      <h1>{winner ? `${winner.name} wins` : 'That’s the game'}</h1>
      <Awards frame={frame} />
      <Standings frame={frame} />
      <RateNight game={frame.game} />
    </div>
  );
}

// podiumPlace is this table's finishing position, or 0 if it is off the
// podium (or watching).
//
// Shared score means shared place, the same rule BetweenQuestions uses: two
// tables tied on $1,400 both won, and telling one of them it came second
// would be wrong in a way it can check against the wall.
function podiumPlace(frame: PlayerFrame, sorted: PlayerFrame['teams']): number {
  const teamId = frame.you?.teamId;
  if (!teamId) return 0;
  const mine = sorted.find((t) => t.id === teamId);
  if (!mine) return 0;
  const place = sorted.findIndex((t) => t.score === mine.score) + 1;
  return place >= 1 && place <= 3 ? place : 0;
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
