import { useEffect, useRef, useState } from 'react';
import { money, parseAnswer, submitAnswer, type PlayerFrame } from './api';

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
