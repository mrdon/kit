import { useRef, useState } from 'react';
import { frogSVG } from './art/frog.js';
import { fxPuff } from './art/fx.js';
import { HOP } from './consts.js';
import type { Frame } from './api';

// FLICK!: the word, your frog, the clock. Nothing else.
//
// Strength is how fast the thumb is moving up when it lets go, in screen
// heights per second over the last ~80ms (so phone size doesn't matter),
// scaled so FULL is 1. A relaxed flick is ~2-3 H/s, a hard one 5+. A flick
// within the band of the frame's flickSpot is perfect. The spot is secret:
// nothing on screen hints at it, and only a perfect flick that won the bonus
// gets a word. The phone judges it, the server trusts it. A tap or a lazy
// flick still hops.
const FULL = 8;
const WINDOW = 80;
// How long the frog takes to fly off after a flick, before the screen closes.
export const FLICK_AWAY_MS = 900;

type Trail = [number, number][]; // [time ms, y px] while a thumb is down

function upSpeed(tr: Trail) {
  if (tr.length < 2) return 0;
  const [t1, y1] = tr[tr.length - 1];
  let i = tr.length - 2;
  while (i > 0 && t1 - tr[i][0] < WINDOW) i--;
  const [t0, y0] = tr[i];
  const dt = Math.max(8, t1 - t0);
  return Math.max(0, ((y0 - y1) / dt) * 1000 / window.innerHeight);
}

export const myTurn = (s: Frame | null, id: string) =>
  !!s && !!id && s.phase === HOP.phase.racing && s.sub === HOP.sub.flick && s.current === id;

interface Props {
  frame: Frame;
  color: string;
  hat: string;
  // msLeft is the shared stream's local countdown (see live/liveStream).
  msLeft: number | null;
  onFlick: (power: number, perfect: boolean) => void;
}

export function Flick({ frame, color, hat, msLeft, onFlick }: Props) {
  const [away, setAway] = useState(false);
  const trail = useRef<Trail | null>(null);
  const done = useRef(false);

  const fire = (power: number, swiped: boolean) => {
    if (done.current) return;
    done.current = true;
    const band = frame.flickBand || HOP.flickBand;
    const perfect = swiped && Math.abs(power - (frame.flickSpot ?? 0.62)) <= band / 2;
    setAway(true);
    fxPuff(window.innerWidth / 2, window.innerHeight * 0.7, 40, 9);
    onFlick(Math.round(power * 100) / 100, perfect);
  };

  return (
    <div
      id="flick"
      onPointerDown={(e) => {
        trail.current = [[e.timeStamp, e.clientY]];
        try { e.currentTarget.setPointerCapture(e.pointerId); } catch { /* not capturable */ }
      }}
      onPointerMove={(e) => {
        const tr = trail.current;
        if (!tr || done.current) return;
        const evs = e.nativeEvent.getCoalescedEvents?.() ?? [];
        for (const c of evs.length ? evs : [e.nativeEvent]) tr.push([c.timeStamp, c.clientY]);
        if (tr.length > 64) tr.splice(0, tr.length - 64);
      }}
      onPointerUp={(e) => {
        const tr = trail.current;
        if (!tr) return;
        // a lift right after the last move isn't a stop: only count it if the
        // thumb moved again or rested a while (resting first = a lazy flick)
        const [lt, ly] = tr[tr.length - 1];
        if (e.clientY !== ly || e.timeStamp - lt > 50) tr.push([e.timeStamp, e.clientY]);
        const dy = e.clientY - tr[0][1];
        trail.current = null;
        if (dy < -30) fire(Math.min(1, upSpeed(tr) / FULL), true);
        else if (Math.abs(dy) < 10) fire(0, false);
      }}
      onPointerCancel={() => { trail.current = null; }}
    >
      <h1>
        {[...'FLICK!'].map((c, i) => <span key={i} style={{ animationDelay: `${-i * 0.15}s` }}>{c}</span>)}
      </h1>
      <div className={`face ${away ? 'away' : ''}`} dangerouslySetInnerHTML={{ __html: frogSVG(color, hat, { mood: 'wow' }) }} />
      <div className="bar"><div style={{ width: `${Math.max(0, Math.min(100, (msLeft ?? 0) / 80))}%` }} /></div>
    </div>
  );
}
