import { useMemo, useRef, useState } from 'react';
import { motion } from 'framer-motion';
import { Clock } from './screens';
import { money, placeChip, type PlayerFrame, type WireSlot } from './api';

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
