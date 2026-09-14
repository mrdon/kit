import { useEffect, useMemo, useRef, useState } from 'react';
import { motion } from 'framer-motion';
import { Clock } from './screens';
import { money, placeChip, type PlayerFrame, type WireSlot } from './api';

// Betting. Tap-to-place is primary and drag is the affordance: in a dark bar
// with greasy hands, drag fights a scrolling list.
//
// The tap flow is most of this screen, so it is worth stating outright. A tap
// on an answer row does the obvious thing WHENEVER THERE IS an obvious thing:
// one chip left in hand — and the final, which only ever has one — places it
// then and there. With both chips still in hand the row asks which, inline
// and anchored to the row you touched, because a dialog in the middle of a
// phone has already lost the answer it is about. Arming a chip first (tap the
// chip, then the row) still works and skips the question, and tapping a chip
// that is already down takes it back.
export function Betting({
  frame, msLeft, onDone,
}: {
  frame: PlayerFrame;
  msLeft: number | null;
  onDone: (f: PlayerFrame) => void;
}) {
  const [armed, setArmed] = useState<number | null>(null);
  // The row whose "which chip?" question is open, or null. At most one, and
  // it belongs to a row rather than to the screen.
  const [asking, setAsking] = useState<string | null>(null);

  const isFinal = !!frame.round?.isFinal;
  const chips = isFinal ? [frame.you?.stake ?? 0] : frame.tokens;
  const placedBy = useMemo(() => {
    const m = new Map<number, string>();
    (frame.you?.chips ?? []).forEach((c) => m.set(c.tokenIndex, c.slotId));
    return m;
  }, [frame.you]);
  const inHand = chips.map((_, i) => i).filter((i) => !placedBy.has(i));

  const { place, err } = usePlacer(onDone);
  const drag = useChipDrag({
    place,
    placedBy,
    onStart: (chip) => { setArmed(chip); setAsking(null); },
    onEnd: () => setArmed(null),
  });

  useEffect(() => {
    setArmed(null);
    setAsking(null);
  }, [frame.round?.id]);

  // One tap on a row, three possible meanings — resolved here so the row
  // itself stays dumb.
  const tapSlot = (slot: WireSlot) => {
    if (drag.justDragged()) return; // the click that trails a drop
    if (armed !== null) {
      setArmed(null);
      void place(armed, slot.id);
      return;
    }
    if (inHand.length === 0) return;
    if (inHand.length === 1) {
      void place(inHand[0], slot.id);
      return;
    }
    setAsking(asking === slot.id ? null : slot.id);
  };

  const answer = (slot: WireSlot, picked: number[]) => {
    setAsking(null);
    // Sequentially, because usePlacer serialises: two PUTs in flight at once
    // come back in either order and the later frame wins.
    picked.forEach((chip) => void place(chip, slot.id));
  };

  return (
    <div className="body">
      <Clock msLeft={msLeft} note="to place your chips" />
      <h2 className="q-compact">{frame.round?.text}</h2>

      <ChipTray
        chips={chips}
        placedBy={placedBy}
        armed={armed}
        dragging={drag.dragging}
        dragProps={drag.dragProps}
        onArm={(i) => { setAsking(null); setArmed(armed === i ? null : i); }}
        status={statusLine({ frame, chips, placedBy, isFinal })}
      />

      <div className="slots">
        {frame.slots.map((s) => (
          <SlotRow
            key={s.id}
            slot={s}
            mine={(frame.you?.chips ?? []).filter((c) => c.slotId === s.id)}
            armed={armed !== null}
            live={armed !== null || inHand.length > 0}
            over={drag.over === s.id}
            dragging={drag.dragging}
            dragProps={drag.dragProps}
            setRow={drag.setRow(s.id)}
            onTap={() => tapSlot(s)}
            onLift={(chip) => { if (!drag.justDragged()) void place(chip, null); }}
            asking={asking === s.id ? { chips, inHand, onPick: (picked) => answer(s, picked) } : null}
          />
        ))}
      </div>
      <p className="err">{err}</p>
    </div>
  );
}

// usePlacer owns every call to the bets endpoint.
//
// It SERIALISES rather than drops. The old synchronous `running` ref returned
// early while a request was in flight, which is fine for a double-tap and
// wrong for everything else: a chip dropped a beat after another one simply
// vanished, and "Both" could never place two. A PUT of a desired placement is
// idempotent, so queueing is safe, and queueing also keeps the frames in
// order — two requests in flight come back in either order and the later
// answer wins.
function usePlacer(onDone: (f: PlayerFrame) => void) {
  const [err, setErr] = useState('');
  const chain = useRef<Promise<void>>(Promise.resolve());

  const place = (chip: number, slotId: string | null): Promise<void> => {
    const next = chain.current.then(async () => {
      try {
        onDone(await placeChip(chip, slotId));
        setErr('');
      } catch (e) {
        setErr(e instanceof Error ? e.message : 'could not place that');
      }
    });
    chain.current = next;
    return next;
  };

  return { place, err };
}

type DragProps = ReturnType<ReturnType<typeof useChipDrag>['dragProps']>;

// useChipDrag is the pointer half of the same two gestures.
function useChipDrag({
  place, placedBy, onStart, onEnd,
}: {
  place: (chip: number, slotId: string | null) => void;
  placedBy: Map<number, string>;
  onStart: (chip: number) => void;
  onEnd: () => void;
}) {
  const [dragging, setDragging] = useState<number | null>(null);
  const [over, setOver] = useState<string | null>(null);
  const rowRefs = useRef(new Map<string, HTMLElement>());
  const endedAt = useRef(0);

  const setRow = (id: string) => (el: HTMLDivElement | null) => {
    if (el) rowRefs.current.set(id, el);
    else rowRefs.current.delete(id);
  };

  // slotUnder finds the answer row beneath a pointer. Our own rects rather
  // than elementFromPoint, because the chip under the finger would be the top
  // element every time.
  //
  // THE SUBTRACTION IS THE WHOLE DRAG BUG. framer-motion reports pageX/pageY
  // and getBoundingClientRect is viewport space; the answers scroll, so the
  // two disagree by exactly the scroll offset. Past the first screenful every
  // drop landed on a row further down the list, or on nothing at all — which
  // to the person holding the phone is simply "drag doesn't work".
  const slotUnder = (point: { x: number; y: number }): string | null => {
    const x = point.x - window.scrollX;
    const y = point.y - window.scrollY;
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
      onStart(chip);
    },
    onDrag: (_: unknown, info: { point: { x: number; y: number } }) => {
      setOver(slotUnder(info.point));
    },
    onDragEnd: (_: unknown, info: { point: { x: number; y: number } }) => {
      const id = slotUnder(info.point);
      setDragging(null);
      setOver(null);
      onEnd();
      // A click still follows pointerup after a drag, and the row underneath
      // would read it as a tap: placing the chip a second time, or popping
      // the "which chip?" question open on top of the one that just landed.
      // Stamp the drop; the tap handlers ignore anything this close behind.
      endedAt.current = Date.now();
      if (!id) {
        // Dropped on nothing. Dragging a placed chip off its row is how you
        // take it back — the same gesture, no separate control.
        if (placedBy.has(chip)) place(chip, null);
        return;
      }
      if (placedBy.get(chip) === id) return; // already there
      place(chip, id);
    },
  });

  return {
    dragging,
    over,
    setRow,
    dragProps,
    justDragged: () => Date.now() - endedAt.current < 250,
  };
}

// The tray: the chips still in hand at full size, the ones that are down
// reduced to a ticked outline, and the state of play spelled out underneath.
// People could not tell how many chips they had or whether they were
// finished; both now have their own space rather than being inferred.
function ChipTray({
  chips, placedBy, armed, dragging, dragProps, onArm, status,
}: {
  chips: number[];
  placedBy: Map<number, string>;
  armed: number | null;
  dragging: number | null;
  dragProps: (chip: number) => DragProps;
  onArm: (chip: number) => void;
  status: string;
}) {
  const done = placedBy.size === chips.length;
  return (
    <div className="betting-head">
      <div className="tray">
        {chips.map((amount, i) =>
          placedBy.has(i) ? (
            <span key={i} className={`chip c${i} ghost`} aria-hidden="true">✓</span>
          ) : (
            <motion.button
              key={i}
              className={`chip c${i} ${armed === i ? 'armed' : ''} ${dragging === i ? 'dragging' : ''}`}
              onClick={() => onArm(i)}
              whileTap={{ scale: 0.92 }}
              whileDrag={{ scale: 1.15, zIndex: 30 }}
              aria-label={`${money(amount)} chip, still to place`}
              {...dragProps(i)}
            >
              {money(amount)}
            </motion.button>
          ))}
      </div>
      <p className={done ? 'betting-status done' : 'betting-status'}>{status}</p>
    </div>
  );
}

// One answer card: its value, who wrote it, your chips on it, and — when the
// row has been asked a question it cannot answer on its own — the chooser.
function SlotRow({
  slot, mine, armed, live, over, dragging, dragProps, setRow, onTap, onLift, asking,
}: {
  slot: WireSlot;
  mine: { tokenIndex: number; amount: number }[];
  // armed: a chip is picked up, so every row is a target and says so. live:
  // a tap would do SOMETHING (there are chips left), which is a lower bar —
  // lighting every row red for the whole phase would just be noise.
  armed: boolean;
  live: boolean;
  over: boolean;
  dragging: number | null;
  dragProps: (chip: number) => DragProps;
  setRow: (el: HTMLDivElement | null) => void;
  onTap: () => void;
  onLift: (chip: number) => void;
  asking: { chips: number[]; inHand: number[]; onPick: (chips: number[]) => void } | null;
}) {
  const cls = [
    'slot',
    slot.value === null ? 'pseudo' : '',
    armed ? 'armed' : '',
    over ? 'over' : '',
    asking ? 'asking' : '',
  ].filter(Boolean).join(' ');

  return (
    <motion.div
      key={slot.id}
      ref={setRow}
      className={cls}
      onClick={onTap}
      whileTap={live ? { scale: 0.98 } : undefined}
    >
      <div>
        <div className="val">{slot.label}</div>
        {slot.teams.length ? <div className="names">{slot.teams.join(' · ')}</div> : null}
      </div>
      <div className="mine">
        {mine.map((c) => (
          // A placed chip stays draggable, so moving it to another answer is
          // the same gesture that put it there — and while the clock is still
          // running, moving is the whole game.
          <motion.button
            key={c.tokenIndex}
            className={`chip c${c.tokenIndex} ${dragging === c.tokenIndex ? 'dragging' : ''}`}
            onClick={(e) => {
              e.stopPropagation();
              onLift(c.tokenIndex);
            }}
            whileDrag={{ scale: 1.15, zIndex: 30 }}
            aria-label={`${money(c.amount)} chip — drag to move it, tap to take it back`}
            {...dragProps(c.tokenIndex)}
          >
            {money(c.amount)}
          </motion.button>
        ))}
      </div>
      {asking ? (
        <ChipChooser label={slot.label} chips={asking.chips} inHand={asking.inHand} onPick={asking.onPick} />
      ) : null}
    </motion.div>
  );
}

// The chooser. Anchored to the row it is about and phrased as the question a
// player would ask out loud, so the answer never has to be remembered across
// a screen: "put which chip on 1969?"
function ChipChooser({
  label, chips, inHand, onPick,
}: {
  label: string;
  chips: number[];
  inHand: number[];
  onPick: (chips: number[]) => void;
}) {
  return (
    <div className="chooser" onClick={(e) => e.stopPropagation()}>
      <span className="ask">Put which chip on {label}?</span>
      <div className="picks">
        {inHand.map((i) => (
          <button key={i} className={`pick c${i}`} onClick={() => onPick([i])}>
            {money(chips[i])}
          </button>
        ))}
        <button className="pick both" onClick={() => onPick(inHand)}>Both</button>
      </div>
    </div>
  );
}

// Plain words, not a progress bar. On a phone at a noisy table the only thing
// that reliably lands is a sentence saying what to do next, or that there is
// nothing left to do.
function statusLine({
  frame, chips, placedBy, isFinal,
}: {
  frame: PlayerFrame;
  chips: number[];
  placedBy: Map<number, string>;
  isFinal: boolean;
}): string {
  const total = chips.length;
  const down = placedBy.size;
  const inHand = chips.map((_, i) => i).filter((i) => !placedBy.has(i));

  if (isFinal) {
    return down === total
      ? 'Wager placed. Waiting for the other tables.'
      : 'Put your wager on whichever answer you think wins.';
  }
  if (down === total) {
    const waiting = frame.teams.filter((t) => t.eligible && t.chipsPlaced < total).length;
    const all = total === 2 ? 'Both chips down.' : `All ${total} chips down.`;
    return waiting > 0
      ? `${all} Waiting for ${waiting} more ${waiting === 1 ? 'table' : 'tables'}.`
      : all;
  }
  if (down === 0) {
    return `You have ${total} chips. Tap an answer — both on one is allowed.`;
  }
  return `${down} of ${total} placed — your ${money(chips[inHand[0]] ?? 0)} chip still to go.`;
}
