import { Clock } from './screens';
import { PHASE, type PlayerFrame } from './api';

// The screens a table sees while it is IN THE ROOM but not in the round.
//
// You can walk in at 9pm and join on any question but the final. The catch is
// the question already in flight: joining mid-question would put a table in a
// denominator it was excluded from, so the server marks it eligible from the
// next one and these screens are what it looks at until then.
//
// They all lead with the SAME sentence, and that sentence carries a NUMBER.
// "Sitting this one out" on its own reads like a penalty for turning up late;
// "You're in from question 7" reads like a seat with a start time, which is
// what it actually is.

// sittingOutLine is the sentence, and the one place it is written.
//
// The final gets its own wording because it is not a wait — there is no
// question after it. A table that somehow has an ineligible seat while the
// final runs is watching the end of the night, not queuing for a turn.
export function sittingOutLine(frame: PlayerFrame): string {
  if (frame.round?.isFinal) {
    return 'The final question has already started. You are watching this one out.';
  }
  const n = frame.you?.inFromQuestion ?? 0;
  return n
    ? `You’re in from question ${n}. Watch this one out.`
    : 'You’re in from the next question. Watch this one out.';
}

// sittingOutScreen is the whole of the late-joiner routing, in one place.
//
// It returns null for a table that is in the round — which is almost always —
// and for the phases where an ineligible table wants the ordinary screen
// anyway: between questions it has a rank to look at (on $0, which is the
// honest number), and at scoring it gets the result card like everybody else.
// The two it diverts are the two where the normal screen offers a control the
// server would refuse: the answer box and the chip tray.
export function sittingOutScreen(frame: PlayerFrame, msLeft: number | null) {
  if (frame.you?.eligible !== false) return null;
  if (frame.phase === PHASE.QUESTION) return <SittingOut frame={frame} msLeft={msLeft} />;
  if (frame.phase === PHASE.BETTING) {
    return <WatchingCards frame={frame} msLeft={msLeft} note="until the cards are scored" sittingOut />;
  }
  return null;
}

// SittingOut is the question phase for a table that is not in it.
//
// The clock and the "x of y answered" line are the same two live numbers the
// wall is showing, deliberately: a phone that goes inert for sixty seconds
// reads as broken, and these two tick. The prompt is underneath because it is
// already on the wall and on every other phone — there is nothing to withhold,
// and a table that can play along in its head is a table still in the night.
export function SittingOut({ frame, msLeft }: { frame: PlayerFrame; msLeft: number | null }) {
  return (
    <div className="body">
      <Clock msLeft={msLeft} />
      <h1>{sittingOutLine(frame)}</h1>
      <p className="sub">
        {frame.round ? `${frame.round.answered} of ${frame.round.eligible} tables have answered.` : ''}
      </p>
      {frame.round ? <h2 className="watching">{frame.round.text}</h2> : null}
    </div>
  );
}

// WatchingCards is the reveal, and the betting for a table that cannot bet.
//
// Read-only on purpose, and the same component for both phases: the cards are
// the interesting thing on the screen either way, and the difference between
// "betting opens shortly" and "you cannot bet on this one" is one line of
// text, not a different screen. A latecomer must NEVER see the chip tray —
// the server refuses the chip anyway, so a tray here would be a button that
// only ever produces an error.
export function WatchingCards({
  frame, msLeft, note, sittingOut,
}: {
  frame: PlayerFrame;
  msLeft: number | null;
  note?: string;
  sittingOut?: boolean;
}) {
  return (
    <div className="body">
      <Clock msLeft={msLeft} note={note} />
      {sittingOut ? <p className="banner">{sittingOutLine(frame)}</p> : null}
      <h2>Here&rsquo;s what the room said</h2>
      <div className="slots">
        {frame.slots.map((s) => (
          <div key={s.id} className={`slot ${s.value === null ? 'pseudo' : ''}`}>
            <div>
              <div className="val">{s.label}</div>
              {s.teams.length ? <div className="names">{s.teams.join(' · ')}</div> : null}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
