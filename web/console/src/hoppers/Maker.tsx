import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { COLORS, HATS, frogSVG, hatSVG } from './art/frog.js';
import { fxPuff, randomFrogName } from './art/fx.js';
import type { Frame, FrogDraft } from './api';

// The frog maker: name, colour and hat, on a beer-mat stage. It is the join
// screen for a new phone and the edit screen for a joined one.

const HAT_NAMES: Record<string, string> = {
  none: 'No hat', crown: 'Crown', cowboy: 'Cowboy hat', beanie: 'Beanie', party: 'Party hat', viking: 'Viking helmet',
  propeller: 'Propeller cap', tophat: 'Top hat', flower: 'Daisy', pint: 'Pint', chef: 'Chef hat', headband: 'Sweatband',
  bucket: 'Bucket hat', antlers: 'Antlers', silver: 'Parylene', curls: 'Appraiser', cap: 'COTY',
  blonde: 'Cat herder', wild: 'Butler', black: 'Raven', ponytail: 'Welder', pm: 'PM guy',
  helmet: 'Major Tom', hacker: 'Hacker', finance: 'Finance', construction: 'Construction',
  baron: 'Property baron', growler: 'Mr 2:58', garage: 'Man cave',
};

const DRAFT_KEY = 'hoppers.frog';
const pick = <T,>(a: readonly T[]) => a[Math.floor(Math.random() * a.length)];

// The draft is remembered on the phone so a second night starts from the same
// frog. Only the frog: the identity lives in an HttpOnly cookie.
export function loadDraft(): FrogDraft | null {
  try {
    const v = JSON.parse(window.localStorage.getItem(DRAFT_KEY) ?? 'null') as FrogDraft | null;
    return v && v.name ? { name: v.name, color: v.color, hat: v.hat } : null;
  } catch {
    return null;
  }
}
export function saveDraft(f: FrogDraft) {
  try {
    window.localStorage.setItem(DRAFT_KEY, JSON.stringify({ name: f.name, color: f.color, hat: f.hat }));
  } catch {
    /* private mode: nothing to remember it in */
  }
}

function takenColours(frame: Frame | null, me: string) {
  return new Set((frame?.players ?? []).filter((p) => !p.bot && p.id !== me).map((p) => p.color));
}
function freeColour(frame: Frame | null, me: string) {
  const taken = takenColours(frame, me);
  const free = COLORS.filter((c) => !taken.has(c));
  return pick(free.length ? free : COLORS);
}
export function newDraft(frame: Frame | null): FrogDraft {
  return loadDraft() ?? { name: randomFrogName(), color: freeColour(frame, ''), hat: pick(HATS.slice(1)) };
}

type Reaction = 'pop' | 'hop' | '';

interface Props {
  frame: Frame | null;
  me: string; // the joined player's id, '' for a new phone
  initial: FrogDraft;
  busy: boolean;
  onDone: (f: FrogDraft) => void;
  onReclaim: (playerId: string, code: string) => void;
}

export function Maker({ frame, me, initial, busy, onDone, onReclaim }: Props) {
  const [draft, setDraft] = useState<FrogDraft>(initial);
  const [react, setReact] = useState<{ kind: Reaction; n: number }>({ kind: 'pop', n: 0 });
  const [roll, setRoll] = useState(0);
  const preview = useRef<HTMLDivElement>(null);
  const hats = useRef<HTMLDivElement>(null);
  const taken = takenColours(frame, me);

  const update = (f: Partial<FrogDraft>, kind: Reaction) => {
    setDraft((d) => ({ ...d, ...f }));
    setReact((r) => ({ kind, n: r.n + 1 }));
  };
  const puff = (dy: number, size: number, n: number) => {
    const r = preview.current?.getBoundingClientRect();
    if (r) fxPuff(r.left + r.width / 2, r.top + r.height * 0.25 + dy, size, n);
  };

  // Fit each hat thumbnail to its own art, so small hats read as clearly as
  // big ones. Needs the rendered SVG, hence a layout effect.
  useLayoutEffect(() => {
    hats.current?.querySelectorAll('svg').forEach((sv) => {
      try {
        const b = sv.getBBox(), pad = 3, m = Math.max(b.width, b.height) + pad * 2;
        sv.setAttribute('viewBox', `${b.x + b.width / 2 - m / 2} ${b.y + b.height / 2 - m / 2} ${m} ${m}`);
      } catch {
        /* not laid out yet */
      }
    });
  }, [draft.color]);

  useEffect(() => {
    const el = preview.current;
    if (!el || !react.kind) return;
    el.classList.remove('pop', 'hop');
    void el.offsetWidth;
    el.classList.add(react.kind);
  }, [react]);

  const done = () => onDone({ ...draft, name: draft.name.trim() || randomFrogName() });

  return (
    <div id="maker">
      <div className="mk-head">
        <h2>{me ? 'Edit your frog' : 'Make your frog'}</h2>
      </div>
      <div className="stage" onClick={() => update({}, 'hop')}>
        <div
          className="preview"
          ref={preview}
          onAnimationEnd={(e) => e.target === preview.current && e.animationName === 'mkjump' && preview.current?.classList.remove('hop')}
          dangerouslySetInnerHTML={{ __html: frogSVG(draft.color, draft.hat) }}
        />
      </div>
      <div className="namebox">
        <input
          type="text" maxLength={24} placeholder="Name your frog" autoComplete="off" enterKeyHint="done" aria-label="Frog name"
          value={draft.name}
          onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
          onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()}
        />
        <button
          key={`dice-${roll}`} className={`dice ${roll ? 'roll' : ''}`} title="Random name" aria-label="Random name"
          onClick={() => { setRoll((n) => n + 1); update({ name: randomFrogName() }, 'hop'); }}
        >
          🎲
        </button>
      </div>
      <div className="lbl">Colour</div>
      <div className="swatches">
        {COLORS.map((c) => (
          <button
            key={c}
            className={`swatch ${c === draft.color ? 'on' : ''} ${taken.has(c) ? 'taken' : ''}`}
            style={{ background: c }} aria-label={`colour ${c}`} disabled={taken.has(c)} title={taken.has(c) ? 'taken' : undefined}
            onClick={() => update({ color: c }, c === draft.color ? 'hop' : 'pop')}
          />
        ))}
      </div>
      <div className="lbl">
        Hat <em>· {HAT_NAMES[draft.hat] ?? draft.hat}</em>
      </div>
      <div className="hats" ref={hats}>
        {HATS.map((h) => (
          <button
            key={h} className={`hatbtn ${h === draft.hat ? 'on' : ''}`} title={HAT_NAMES[h] ?? h} aria-label={HAT_NAMES[h] ?? h}
            onClick={() => { update({ hat: h }, 'hop'); if (h !== 'none') puff(0, 22, 6); }}
          >
            {h === 'none' ? <span className="no">none</span>
              : <svg viewBox="12 -4 78 46" xmlns="http://www.w3.org/2000/svg" dangerouslySetInnerHTML={{ __html: hatSVG(h, draft.color) }} />}
          </button>
        ))}
      </div>
      <div className="mk-go">
        <button
          className="btn ghost" id="randomAll"
          onClick={() => {
            update({ name: randomFrogName(), color: freeColour(frame, me), hat: pick(HATS.slice(1)) }, 'hop');
            puff(60, 30, 8);
          }}
        >
          <span>🎲</span>Random
        </button>
        <button className="btn" id="done" disabled={busy} onClick={done}>
          {me ? 'Save' : 'Join'}
        </button>
      </div>
      {!me && <Reclaim frame={frame} onReclaim={onReclaim} />}
    </div>
  );
}

// A frog whose phone died: the host reads out a one-time code for it. There
// is no "pick your frog" on its own -- with twenty names on the wall that is
// an impersonation hole -- so the code is the trust boundary, and the host
// who issues it can see who is asking.
function Reclaim({ frame, onReclaim }: { frame: Frame | null; onReclaim: (id: string, code: string) => void }) {
  const [open, setOpen] = useState(false);
  const [who, setWho] = useState('');
  const [code, setCode] = useState('');
  const frogs = (frame?.players ?? []).filter((p) => !p.bot);
  if (!open) {
    return (
      <div className="reclaim">
        <button className="link" onClick={() => setOpen(true)}>Already have a frog? Ask the host for a code.</button>
      </div>
    );
  }
  return (
    <div className="reclaim">
      <div id="reclaimForm">
        <select value={who} onChange={(e) => setWho(e.target.value)} aria-label="Your frog">
          <option value="">Your frog…</option>
          {frogs.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
        </select>
        <input
          type="text" inputMode="numeric" maxLength={4} placeholder="Code" autoComplete="off" aria-label="Code from the host"
          value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))}
        />
        <button className="btn" disabled={!who || code.length !== 4} onClick={() => onReclaim(who, code)}>Get it</button>
      </div>
    </div>
  );
}
