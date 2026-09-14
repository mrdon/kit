import { useEffect, useMemo, useRef, useState } from 'react';
import { motion } from 'framer-motion';
import { money, parseAnswer, placeChip, submitAnswer, type PlayerFrame, type WireSlot } from './api';

// Countdown reads from the locally-ticked millisecond value, never from a
// server tick.
export function Clock({ msLeft, note }: { msLeft: number | null; note?: string }) {
  if (msLeft === null) return note ? <p className="sub" style={{ textAlign: 'center' }}>{note}</p> : null;
  const secs = Math.ceil(msLeft / 1000);
  const cls = secs <= 5 ? 'clock hot' : secs <= 15 ? 'clock warn' : 'clock';
  return (
    <div className={cls}>
      <span className="n">{secs}</span>
      <span className="of">{note ?? 'seconds left'}</span>
    </div>
  );
}

export function Waiting({ title, sub }: { title: string; sub?: string }) {
  return (
    <div className="body">
      <h1>{title}</h1>
      {sub ? <p className="sub">{sub}</p> : null}
    </div>
  );
}

// Answer is the screen a table spends most of the night on, so the details
// that decide whether it actually works all live here.
export function Answer({
  frame, msLeft, onDone,
}: {
  frame: PlayerFrame;
  msLeft: number | null;
  onDone: (f: PlayerFrame) => void;
}) {
  const [raw, setRaw] = useState('');
  const [stake, setStake] = useState(0);
  const [confirming, setConfirming] = useState(false);
  // changing is the one thing the locked screen remembers: this table asked to
  // reopen the form. Everything else about being locked in comes off the
  // frame, so a phone that reloads mid-final is still locked in.
  const [changing, setChanging] = useState(false);
  const [err, setErr] = useState('');
  // The synchronous ref guard, because setState is async and two fast taps
  // both see busy === false.
  const running = useRef(false);
  const [, force] = useState(0);

  const isFinal = !!frame.round?.isFinal && frame.finalWager;
  const bank = frame.you?.score ?? 0;
  const parsed = parseAnswer(raw);
  const submitted = frame.you?.answered ?? false;
  // The server's copy of the wager. $0 is a real answer here — the leader's
  // defensive play — so this is a null check, never a truthiness one.
  const locked = isFinal && submitted ? (frame.you?.stake ?? null) : null;

  useEffect(() => {
    setRaw('');
    setConfirming(false);
    setChanging(false);
    setStake(0);
  }, [frame.round?.id]);

  const send = async () => {
    if (running.current || parsed === null) return;
    running.current = true;
    force((n) => n + 1);
    try {
      onDone(await submitAnswer(raw, isFinal ? stake : null));
      setErr('');
      setConfirming(false);
      setChanging(false);
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'could not send that');
    } finally {
      running.current = false;
      force((n) => n + 1);
    }
  };

  if (isFinal && confirming) {
    return (
      <ConfirmWager
        bank={bank} stake={stake} answer={parsed} err={err} busy={running.current}
        onLock={() => void send()} onBack={() => setConfirming(false)}
      />
    );
  }

  // The phase is STILL `question` after a table locks in — the host has not
  // closed it and the rest of the room is still typing — so this component
  // stays mounted and has to announce the change itself. Leaving the confirm
  // screen up is what made "Lock it in" read as a button that did nothing.
  if (locked !== null && !changing) {
    return (
      <LockedIn
        msLeft={msLeft} stake={locked} answer={parsed}
        onChange={() => { setStake(locked); setChanging(true); }}
      />
    );
  }

  return (
    <div className="body">
      <Clock msLeft={msLeft} />
      <h2>{frame.round?.text}</h2>
      <input
        className="field big"
        /* type="text" with inputmode="decimal", deliberately. `number` brings
           spinners, silently drops non-numeric paste and handles locales
           badly; `numeric` gives no decimal point on iOS, and answers are not
           all integers. */
        type="text"
        inputMode="decimal"
        enterKeyHint="send"
        autoComplete="off"
        placeholder="your number"
        value={raw}
        onChange={(e) => setRaw(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !isFinal) void send();
        }}
      />
      {/* Echo the parsed value back BEFORE submit. Without it you get silent
          zeros and an argument at the bar. */}
      <div className={parsed === null && raw !== '' ? 'echo bad' : 'echo'}>
        {raw === '' ? '' : parsed === null
          ? <>we can&rsquo;t read <strong>{raw}</strong> as a number</>
          : <>we read that as <strong>{parsed.toLocaleString('en-US')}</strong></>}
      </div>

      {isFinal ? (
        <StakeControl bank={bank} stake={stake} onChange={setStake} />
      ) : null}

      <button
        className="btn"
        disabled={parsed === null || running.current}
        onClick={() => (isFinal ? setConfirming(true) : void send())}
      >
        {isFinal ? 'Review wager' : submitted ? 'Change my answer' : 'Send it'}
      </button>
      {/* Saying so removes fat-finger anxiety on a 60-second clock. */}
      <p className="sub" style={{ textAlign: 'center' }}>
        {submitted ? 'In! You can change it until time\u2019s up.' : 'You can change it until time\u2019s up.'}
      </p>
      <p className="err">{err}</p>
    </div>
  );
}

// The confirm beat. A final wager is the whole night's money, so it gets its
// own screen rather than a second tap on the same button.
function ConfirmWager({
  bank, stake, answer, err, busy, onLock, onBack,
}: {
  bank: number;
  stake: number;
  answer: number | null;
  err: string;
  busy: boolean;
  onLock: () => void;
  onBack: () => void;
}) {
  return (
    <div className="body">
      <h2>Lock it in?</h2>
      <div className="stake-amount">{money(stake)}</div>
      <p className="sub">
        Your answer: <strong>{answer !== null ? answer.toLocaleString('en-US') : '—'}</strong>
      </p>
      <div className="outcomes">
        <span className="win">win → {money(bank + stake)}</span>
        <span className="lose">lose → {money(bank - stake)}</span>
      </div>
      {/* The button says what it is doing. On bar wifi the round trip is long
          enough that a button which just sits there gets tapped again. */}
      <button className="btn gold" disabled={busy} onClick={onLock}>
        {busy ? 'Locking it in…' : 'Lock it in'}
      </button>
      <button className="btn ghost" disabled={busy} onClick={onBack}>Back</button>
      <p className="err">{err}</p>
    </div>
  );
}

// What a table sees for the rest of the final's clock. The amount is the hero
// because it is what they will argue about at the table, and "you can still
// change it" is on screen because the alternative is a table that believes it
// is stuck with a number it typed in a hurry.
function LockedIn({
  msLeft, stake, answer, onChange,
}: {
  msLeft: number | null;
  stake: number;
  answer: number | null;
  onChange: () => void;
}) {
  return (
    <div className="body">
      <Clock msLeft={msLeft} />
      <h1 style={{ textAlign: 'center' }}>Locked in.</h1>
      <div className="stake-amount">{money(stake)}</div>
      {/* The answer is local state, so a phone that reloaded mid-final has the
          wager back from the server but not the number it typed. Better to
          show the wager alone than to show a dash where their answer was. */}
      {answer !== null ? (
        <p className="sub" style={{ textAlign: 'center' }}>
          Your answer: <strong>{answer.toLocaleString('en-US')}</strong>
        </p>
      ) : null}
      <p className="sub" style={{ textAlign: 'center' }}>
        Waiting for the other tables &mdash; you can change it until time&rsquo;s up.
      </p>
      <button className="btn ghost" onClick={onChange}>Change it</button>
    </div>
  );
}

// The stake control. Presets alongside a slider because a slider alone is
// imprecise with a thumb, and $0 is a first-class choice — the leader's
// defensive play — so it reads as a button rather than as giving up.
function StakeControl({ bank, stake, onChange }: { bank: number; stake: number; onChange: (n: number) => void }) {
  const clamp = (n: number) => Math.max(0, Math.min(bank, Math.round(n)));
  return (
    <div className="stake">
      <div className="stake-amount">{money(stake)}</div>
      <input
        className="slider"
        type="range"
        min={0}
        max={Math.max(bank, 1)}
        step={Math.max(1, Math.round(bank / 100) || 1)}
        value={stake}
        onChange={(e) => onChange(clamp(Number(e.target.value)))}
        aria-label="wager"
      />
      <div className="presets">
        <button className="btn ghost" onClick={() => onChange(0)}>$0</button>
        <button className="btn ghost" onClick={() => onChange(clamp(bank / 2))}>Half</button>
        <button className="btn ghost" onClick={() => onChange(bank)}>All in</button>
      </div>
      <div className="outcomes">
        <span className="win">win → {money(bank + stake)}</span>
        <span className="lose">lose → {money(bank - stake)}</span>
      </div>
    </div>
  );
}

// Betting. Tap-to-place is primary and drag is the affordance: in a dark bar
// with greasy hands, drag fights a scrolling list.
export function Betting({
  frame, msLeft, onDone,
}: {
  frame: PlayerFrame;
  msLeft: number | null;
  onDone: (f: PlayerFrame) => void;
}) {
  const [armed, setArmed] = useState<number | null>(null);
  const [dragging, setDragging] = useState<number | null>(null);
  const [over, setOver] = useState<string | null>(null);
  const [err, setErr] = useState('');
  const running = useRef(false);
  // Live rects for the answer rows, so a drop can be hit-tested without
  // measuring the DOM on every pointer move.
  const rowRefs = useRef(new Map<string, HTMLElement>());

  const isFinal = !!frame.round?.isFinal;
  const chips = isFinal ? [frame.you?.stake ?? 0] : frame.tokens;
  const placedBy = useMemo(() => {
    const m = new Map<number, string>();
    (frame.you?.chips ?? []).forEach((c) => m.set(c.tokenIndex, c.slotId));
    return m;
  }, [frame.you]);

  const place = async (chip: number, slotId: string | null) => {
    if (running.current) return;
    running.current = true;
    try {
      onDone(await placeChip(chip, slotId));
      setErr('');
      setArmed(null);
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'could not place that');
    } finally {
      running.current = false;
    }
  };

  const tapSlot = (slot: WireSlot) => {
    if (armed === null) return;
    if (blockedFor(armed, slot.id, placedBy)) return;
    void place(armed, slot.id);
  };

  // slotUnder finds the answer row beneath a pointer. Hit-testing our own
  // cached rects rather than elementFromPoint, because the chip being dragged
  // sits under the finger and would be the top element every time.
  const slotUnder = (x: number, y: number): string | null => {
    for (const [id, el] of rowRefs.current) {
      const r = el.getBoundingClientRect();
      if (x >= r.left && x <= r.right && y >= r.top && y <= r.bottom) return id;
    }
    return null;
  };

  const dragProps = (chip: number) => ({
    drag: true as const,
    // Snap home on release. The chip's real position is decided by the
    // server round-trip, so animating it to where it was dropped would be a
    // lie the next frame has to undo.
    dragSnapToOrigin: true as const,
    dragMomentum: false as const,
    // Follow the finger exactly: elastic drag makes a small target harder to
    // land on a row in a moving bar.
    dragElastic: 0,
    onDragStart: () => {
      setDragging(chip);
      setArmed(chip);
    },
    onDrag: (_: unknown, info: { point: { x: number; y: number } }) => {
      const id = slotUnder(info.point.x, info.point.y);
      setOver(id && !blockedFor(chip, id, placedBy) ? id : null);
    },
    onDragEnd: (_: unknown, info: { point: { x: number; y: number } }) => {
      const id = slotUnder(info.point.x, info.point.y);
      setDragging(null);
      setOver(null);
      if (!id) {
        // Dropped on nothing. Dragging a placed chip off its row is how you
        // take it back — the same gesture, no separate control.
        if (placedBy.has(chip)) void place(chip, null);
        return;
      }
      if (blockedFor(chip, id, placedBy)) return; // snaps home
      if (placedBy.get(chip) === id) return; // already there
      void place(chip, id);
    },
  });

  const active = dragging ?? armed;

  const total = chips.length;
  const down = placedBy.size;
  const done = down === total;
  // Plain words, not a progress bar. On a phone at a noisy table the only
  // thing that reliably lands is a sentence saying what to do next, or that
  // there is nothing left to do.
  const statusLine = (() => {
    if (isFinal) {
      return done
        ? 'Wager placed. Waiting for the other tables.'
        : 'Put your wager on whichever answer you think wins.';
    }
    if (done) {
      const waiting = frame.teams.filter((t) => t.eligible && t.chipsPlaced < total).length;
      return waiting > 0
        ? `Both chips down. Waiting for ${waiting} more ${waiting === 1 ? 'table' : 'tables'}.`
        : 'Both chips down.';
    }
    if (down === 0) {
      return `You have ${total} chips. Put them on ${total} different answers.`;
    }
    return `${down} of ${total} placed — your ${money(chips[total - down - 1] ?? 0)} chip still to go, on a different answer.`;
  })();

  return (
    <div className="body">
      <Clock msLeft={msLeft} note="to place your chips" />
      <h2 className="q-compact">{frame.round?.text}</h2>

      {/* People did not realise they had two chips, or whether they were
          done. So: the chips are counted out loud, the ones still in hand
          stay in the tray at full size, and the state of play gets its own
          line in plain words rather than being inferred from the tray. */}
      <div className="betting-head">
        <div className="tray">
          {chips.map((amount, i) => {
            const placed = placedBy.has(i);
            if (placed) {
              return (
                <span key={i} className={`chip c${i} ghost`} aria-hidden="true">
                  ✓
                </span>
              );
            }
            return (
              <motion.button
                key={i}
                className={`chip c${i} ${armed === i ? 'armed' : ''} ${dragging === i ? 'dragging' : ''}`}
                onClick={() => setArmed(armed === i ? null : i)}
                whileTap={{ scale: 0.92 }}
                whileDrag={{ scale: 1.15, zIndex: 30 }}
                aria-label={`${money(amount)} chip, still to place`}
                {...dragProps(i)}
              >
                {money(amount)}
              </motion.button>
            );
          })}
        </div>
        <p className={done ? 'betting-status done' : 'betting-status'}>{statusLine}</p>
      </div>

      <div className="slots">
        {frame.slots.map((s) => {
          const blocked = active !== null && blockedFor(active, s.id, placedBy);
          const mine = (frame.you?.chips ?? []).filter((c) => c.slotId === s.id);
          const target = over === s.id;
          return (
            <motion.div
              key={s.id}
              ref={(el: HTMLDivElement | null) => {
                if (el) rowRefs.current.set(s.id, el);
                else rowRefs.current.delete(s.id);
              }}
              className={[
                'slot',
                s.value === null ? 'pseudo' : '',
                active !== null && !blocked ? 'armed' : '',
                blocked ? 'blocked' : '',
                target ? 'over' : '',
              ].filter(Boolean).join(' ')}
              onClick={() => tapSlot(s)}
              whileTap={armed !== null && !blocked ? { scale: 0.98 } : undefined}
            >
              <div>
                <div className="val">{s.label}</div>
                {s.teams.length ? <div className="names">{s.teams.join(' · ')}</div> : null}
                {/* A rule you discover by being rejected is a bad rule; a rule
                    the interface makes obvious is not felt as a rule at all. */}
                {blocked ? <div className="why">your other chip is here</div> : null}
              </div>
              <div className="mine">
                {mine.map((c) => (
                  // A placed chip stays draggable, so moving it to another
                  // answer is the same gesture that put it there — and while
                  // the clock is still running, moving is the whole game.
                  <motion.button
                    key={c.tokenIndex}
                    className={`chip c${c.tokenIndex} ${dragging === c.tokenIndex ? 'dragging' : ''}`}
                    onClick={(e) => {
                      e.stopPropagation();
                      void place(c.tokenIndex, null);
                    }}
                    whileDrag={{ scale: 1.15, zIndex: 30 }}
                    aria-label={`${money(c.amount)} chip — drag to move it, tap to take it back`}
                    {...dragProps(c.tokenIndex)}
                  >
                    {money(c.amount)}
                  </motion.button>
                ))}
              </div>
            </motion.div>
          );
        })}
      </div>
      <p className="err">{err}</p>
    </div>
  );
}

// The two-different-answers rule, mirrored in the UI so it is obvious rather
// than discovered by rejection. The server enforces it with a unique index
// regardless.
function blockedFor(chip: number, slotId: string, placed: Map<number, string>): boolean {
  for (const [idx, sid] of placed) {
    if (idx !== chip && sid === slotId) return true;
  }
  return false;
}
