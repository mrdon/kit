import { useEffect, useRef, useState } from 'react';
import { money, parseAnswer, setWager, submitAnswer, type PlayerFrame } from './api';

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
//
// It is the SAME screen in the final. The wager was committed a phase earlier,
// against nothing but the category, so by the time the question appears there
// is no money decision left to make — only a number to type. All the final
// gets here is a label saying which question this is, and that is the point:
// the bet is behind them.
// "4 of 6 tables have answered." — the line the old wrapper used to carry.
function answeredCount(frame: PlayerFrame): string {
  const r = frame.round;
  return r ? `${r.answered} of ${r.eligible} tables in. ` : '';
}

export function Answer({
  frame, msLeft, onDone,
}: {
  frame: PlayerFrame;
  msLeft: number | null;
  onDone: (f: PlayerFrame) => void;
}) {
  const [raw, setRaw] = useState('');
  const [err, setErr] = useState('');
  // The synchronous ref guard, because setState is async and two fast taps
  // both see busy === false.
  const running = useRef(false);
  const [, force] = useState(0);

  const isFinal = !!frame.round?.isFinal;
  const parsed = parseAnswer(raw);
  const submitted = frame.you?.answered ?? false;
  // A mirror of the server's own everyone-in test, never the authority: the
  // phase ends because the shortened deadline passes, not because this is
  // true. It only decides which sentence to print.
  const roomIsIn = submitted && !!frame.round
    && frame.round.answered >= frame.round.eligible;

  useEffect(() => { setRaw(''); }, [frame.round?.id]);

  const send = async () => {
    if (running.current || parsed === null) return;
    running.current = true;
    force((n) => n + 1);
    try {
      onDone(await submitAnswer(raw));
      setErr('');
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'could not send that');
    } finally {
      running.current = false;
      force((n) => n + 1);
    }
  };

  return (
    <div className="body">
      <Clock msLeft={msLeft} />
      {isFinal ? <p className="final-tag">FINAL QUESTION</p> : null}
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
        onKeyDown={(e) => { if (e.key === 'Enter') void send(); }}
      />
      {/* Echo the parsed value back BEFORE submit. Without it you get silent
          zeros and an argument at the bar. */}
      <div className={parsed === null && raw !== '' ? 'echo bad' : 'echo'}>
        {raw === '' ? '' : parsed === null
          ? <>we can&rsquo;t read <strong>{raw}</strong> as a number</>
          : <>we read that as <strong>{parsed.toLocaleString('en-US')}</strong></>}
      </div>

      <button className="btn" disabled={parsed === null || running.current} onClick={() => void send()}>
        {submitted ? 'Change my answer' : 'Send it'}
      </button>
      {/* Saying so removes fat-finger anxiety on a 60-second clock. Once the
          room is in, the clock is no longer the 60 it was — the server has
          pulled it in to the grace — so the sentence stops promising "time's
          up" and starts promising the few seconds that are actually left. */}
      <p className="sub" style={{ textAlign: 'center' }}>
        {roomIsIn
          ? 'Everyone’s in. You have a few seconds to change it.'
          : submitted ? `In! ${answeredCount(frame)}You can change it until time’s up.` : 'You can change it until time’s up.'}
      </p>
      <p className="err">{err}</p>
    </div>
  );
}

// Wager is the blind bet, and the screen that makes the final a wager rather
// than a calculation: a category, a clock, and an amount — with the question
// nowhere on the wire, let alone on this screen.
//
// Locked state is derived from the SERVER's copy (`you.stake`), never from
// having tapped the button, so a phone that reloads mid-wager comes back
// locked in rather than showing an empty slider over money it already
// committed. $0 is a real wager — the leader's defensive play — so this is a
// null check and never a truthiness one.
export function Wager({
  frame, msLeft, onDone,
}: {
  frame: PlayerFrame;
  msLeft: number | null;
  onDone: (f: PlayerFrame) => void;
}) {
  const locked = frame.you?.stake ?? null;
  const [amount, setAmount] = useState(locked ?? 0);
  const [changing, setChanging] = useState(false);
  const [err, setErr] = useState('');
  const running = useRef(false);
  const [, force] = useState(0);

  const bank = frame.you?.score ?? 0;

  useEffect(() => { setChanging(false); }, [frame.round?.id]);

  const lock = async () => {
    if (running.current) return;
    running.current = true;
    force((n) => n + 1);
    try {
      onDone(await setWager(amount));
      setErr('');
      setChanging(false);
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'could not lock that in');
    } finally {
      running.current = false;
      force((n) => n + 1);
    }
  };

  if (locked !== null && !changing) {
    return (
      <LockedIn
        msLeft={msLeft} stake={locked}
        roomIsIn={frame.teams.filter((t) => t.eligible && !t.stakeLocked).length === 0}
        onChange={() => { setAmount(locked); setChanging(true); }}
      />
    );
  }

  return (
    <div className="body">
      <Clock msLeft={msLeft} />
      <p className="final-tag">FINAL QUESTION</p>
      {/* The category, and it is ALL they get. Betting against a word is the
          whole mechanic — how hard the question turns out to be is not
          something this decision is allowed to know. */}
      {frame.round?.category ? <h1 className="category">{frame.round.category}</h1> : null}
      <p className="sub" style={{ textAlign: 'center' }}>
        Set your wager now. You will see the question next.
      </p>

      <StakeControl bank={bank} stake={amount} onChange={setAmount} />

      <button className="btn gold" disabled={running.current} onClick={() => void lock()}>
        {running.current ? 'Locking it in…' : 'Lock it in'}
      </button>
      <p className="err">{err}</p>
    </div>
  );
}

// What everybody who is NOT betting sees while the room bets: a spectator, a
// table that walked in during the final, a phone with no cookie. Read-only,
// and still the category rather than the question — there is no privileged
// view here, the prompt does not exist on the wire yet for anyone.
export function WagerWatching({ frame, msLeft }: { frame: PlayerFrame; msLeft: number | null }) {
  return (
    <div className="body">
      <Clock msLeft={msLeft} />
      <p className="final-tag">FINAL QUESTION</p>
      {frame.round?.category ? <h1 className="category">{frame.round.category}</h1> : null}
      <p className="sub" style={{ textAlign: 'center' }}>
        Tables are setting their wagers.
      </p>
    </div>
  );
}

// What a table sees for the rest of the wager clock. The amount is the hero
// because it is what they will argue about at the table, and "you can still
// change it" is on screen because the alternative is a table that believes it
// is stuck with a number it dragged in a hurry.
function LockedIn({
  msLeft, stake, roomIsIn, onChange,
}: {
  msLeft: number | null;
  stake: number;
  roomIsIn: boolean;
  onChange: () => void;
}) {
  return (
    <div className="body">
      <Clock msLeft={msLeft} />
      <h1 style={{ textAlign: 'center' }}>Locked in.</h1>
      <div className="stake-amount">{money(stake)}</div>
      {/* "Waiting for the other tables" is a lie once there are none, and it
          is the moment the clock has just dropped to the grace. */}
      <p className="sub" style={{ textAlign: 'center' }}>
        {roomIsIn
          ? <>Everyone&rsquo;s in &mdash; you have a few seconds to change it.</>
          : <>Waiting for the other tables &mdash; you can change it until time&rsquo;s up.</>}
      </p>
      <button className="btn ghost" onClick={onChange}>Change it</button>
    </div>
  );
}

// The stake control. Presets alongside a slider because a slider alone is
// imprecise with a thumb, and $0 is a first-class choice — the leader's
// defensive play — so it reads as a button rather than as giving up.
function preset(on: boolean): string {
  return on ? 'btn ghost on' : 'btn ghost';
}

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
      {/* Which preset is lit comes from the VALUE, not from which button was
          tapped, so a form reopened with the old wager still shows it. */}
      <div className="presets">
        <button className={preset(stake === 0)} onClick={() => onChange(0)}>$0</button>
        <button className={preset(stake === clamp(bank / 2) && bank > 0)} onClick={() => onChange(clamp(bank / 2))}>Half</button>
        <button className={preset(stake === bank && bank > 0)} onClick={() => onChange(bank)}>All in</button>
      </div>
      <div className="outcomes">
        <span className="win">win → {money(bank + stake)}</span>
        <span className="lose">lose → {money(bank - stake)}</span>
      </div>
    </div>
  );
}
