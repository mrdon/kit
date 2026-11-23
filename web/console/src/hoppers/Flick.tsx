import { useEffect, useRef, useState } from 'react';
import { frogSVG } from './art/frog.js';
import { fxPop, fxPuff } from './art/fx.js';
import { HOP } from './consts.js';
import type { Frame } from './api';

// FLICK!: the word, your frog, the power meter, the clock.
//
// Strength is how fast the thumb is moving up when it lets go, in screen
// heights per second over the last ~80ms (so phone size doesn't matter),
// scaled so FULL is 1. A relaxed flick is ~2-3 H/s, a hard one 5+. A flick
// within the band of the frame's flickSpot is perfect. The meter shows it:
// it fills with the thumb's speed while dragging, then holds where the flick
// landed against the green band, with last turn's flick as a faint tick. The
// phone judges it, the server trusts it. A tap or a lazy flick still hops.
// Perfect is about the flick; the +1 only comes on a forward hop.
const FULL = 8;
const WINDOW = 80;
// How long the meter stays up after a flick, so you can read it, before the
// screen closes.
export const FLICK_HOLD_MS = 1400;

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
const strength = (tr: Trail) => Math.min(1, upSpeed(tr) / FULL);

export const myTurn = (s: Frame | null, id: string) =>
  !!s && !!id && s.phase === HOP.phase.racing && s.sub === HOP.sub.flick && s.current === id;

interface Props {
  frame: Frame;
  color: string;
  hat: string;
  // msLeft is the shared stream's local countdown (see live/liveStream).
  msLeft: number | null;
  // lastPower is this phone's previous flick (-1 for none), shown as a tick.
  lastPower: number;
  onFlick: (power: number, perfect: boolean, swiped: boolean) => void;
}

interface Landed {
  power: number;
  swiped: boolean;
  hit: boolean;
}

export function Flick({ frame, color, hat, msLeft, lastPower, onFlick }: Props) {
  const [landed, setLanded] = useState<Landed | null>(null);
  const trail = useRef<Trail | null>(null);
  const fill = useRef<HTMLDivElement>(null);
  const spot = frame.flickSpot ?? 0.62;
  const band = frame.flickBand || HOP.flickBand;

  // The live meter while the thumb is down: its speed right now, so a
  // resting thumb drains it.
  useEffect(() => {
    let raf = 0;
    const tick = () => {
      raf = requestAnimationFrame(tick);
      const tr = trail.current;
      if (!tr || !fill.current) return;
      const [, y] = tr[tr.length - 1];
      fill.current.style.width = `${strength([...tr, [performance.now(), y]]) * 100}%`;
    };
    tick();
    return () => cancelAnimationFrame(raf);
  }, []);

  const fire = (power: number, swiped: boolean) => {
    if (landed) return;
    const hit = swiped && Math.abs(power - spot) <= band / 2;
    setLanded({ power, swiped, hit });
    fxPuff(window.innerWidth / 2, window.innerHeight * 0.5, 40, 9);
    if (hit) fxPop(window.innerWidth / 2, window.innerHeight * 0.22, '✦ ✦ ✦', 54, '#f5c451');
    onFlick(Math.round(power * 100) / 100, hit, swiped);
  };

  const label = !landed ? 'Flick up. Aim for the green.'
    : !landed.swiped ? 'Just a tap'
      : landed.hit ? 'Perfect!'
        : landed.power < spot ? 'Too soft' : 'Too hard';

  return (
    <div
      id="flick"
      className={landed?.hit ? 'hit' : ''}
      onPointerDown={(e) => {
        if (landed) return;
        trail.current = [[e.timeStamp, e.clientY]];
        try { e.currentTarget.setPointerCapture(e.pointerId); } catch { /* not capturable */ }
      }}
      onPointerMove={(e) => {
        const tr = trail.current;
        if (!tr) return;
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
        if (dy < -30) fire(strength(tr), true);
        else if (Math.abs(dy) < 10) fire(0, false);
        else if (fill.current) fill.current.style.width = '0';
      }}
      onPointerCancel={() => { trail.current = null; }}
    >
      <h1>
        {[...'FLICK!'].map((c, i) => <span key={i} style={{ animationDelay: `${-i * 0.15}s` }}>{c}</span>)}
      </h1>
      <div className={`face ${landed ? 'away' : ''}`} dangerouslySetInnerHTML={{ __html: frogSVG(color, hat, { mood: 'wow' }) }} />
      <div className={`meter${landed ? ' done' : ''}${landed?.hit ? ' hit' : ''}`}>
        <div className="zone" style={{ left: `${(spot - band / 2) * 100}%`, width: `${band * 100}%` }} />
        <div className="fill" ref={fill} style={landed ? { width: `${landed.power * 100}%` } : undefined} />
        {lastPower >= 0 && <div className="ghost" style={{ left: `${lastPower * 100}%` }} />}
        {landed && <div className="needle" style={{ left: `${landed.power * 100}%` }} />}
      </div>
      <div className={`mlabel${landed?.hit ? ' hit' : ''}`}>{label}</div>
      <div className="bar"><div style={{ width: `${Math.max(0, Math.min(100, (msLeft ?? 0) / 80))}%` }} /></div>
    </div>
  );
}
