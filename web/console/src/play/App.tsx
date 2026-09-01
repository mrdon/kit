import { useEffect, useRef, useState } from 'react';
import { join, me, money, reclaim, type PlayerFrame } from './api';
import { useStream, useWakeLock } from './useStream';
import { Answer, Betting, Clock, Waiting } from './screens';

// LOCAL_KEY mirrors {gameId, teamId, teamName} — never the token — purely so
// the UI can render "rejoining as Bar Flies…" before the first round trip.
// The token itself lives only in an HttpOnly cookie.
const LOCAL_KEY = 'kit.trivia.identity';

interface LocalIdentity {
  game: string;
  teamId: string;
  name: string;
}

function readLocal(game: string): LocalIdentity | null {
  try {
    const raw = window.localStorage.getItem(LOCAL_KEY);
    if (!raw) return null;
    const v = JSON.parse(raw) as LocalIdentity;
    return v.game === game ? v : null;
  } catch {
    return null;
  }
}

function writeLocal(v: LocalIdentity) {
  try {
    window.localStorage.setItem(LOCAL_KEY, JSON.stringify(v));
  } catch {
    /* private mode; the cookie still works */
  }
}

export default function App() {
  const { frame, connected, msLeft, apply } = useStream();
  const [identity, setIdentity] = useState<{ teamId: string; name: string } | null>(null);
  const [checked, setChecked] = useState(false);
  useWakeLock();

  const gameName = frame?.game ?? '';

  useEffect(() => {
    // Optimistic render from localStorage, then the authoritative answer.
    const cached = gameName ? readLocal(gameName) : null;
    if (cached) setIdentity({ teamId: cached.teamId, name: cached.name });
    void me().then((v) => {
      setIdentity(v);
      setChecked(true);
      if (v && gameName) writeLocal({ game: gameName, teamId: v.teamId, name: v.name });
    });
  }, [gameName]);

  if (!frame) {
    return (
      <div className="app">
        <div className="body">
          <h1>Connecting…</h1>
          <p className="sub">Hold tight.</p>
        </div>
      </div>
    );
  }

  const you = frame.you;
  // A phone with no cookie is a SPECTATOR, not an error. Somebody who opened
  // the URL to watch — or who lost their cookie — sees the whole game
  // read-only.
  const spectating = !you;

  return (
    <div className="app">
      <header className="top">
        <span className="who">{you?.name ?? identity?.name ?? frame.game}</span>
        <span>
          {!connected ? <span className="offline">reconnecting… </span> : null}
          {you ? <span className="score">{money(you.score)}</span> : null}
        </span>
      </header>

      {spectating && checked ? <Lobby frame={frame} onJoined={setIdentity} /> : null}
      {!spectating ? <Playing frame={frame} msLeft={msLeft} apply={apply} /> : null}
      {/* While /me is in flight we render the localStorage name, so a phone
          coming back from a lock screen says "rejoining as Bar Flies…"
          instead of flashing the join form at somebody who is already in. */}
      {spectating && !checked ? (
        <Waiting title={identity ? `Rejoining as ${identity.name}…` : 'One moment…'} />
      ) : null}
    </div>
  );
}

// Playing routes the eight screens off the phase.
function Playing({
  frame, msLeft, apply,
}: {
  frame: PlayerFrame;
  msLeft: number | null;
  apply: (f: PlayerFrame) => void;
}) {
  const you = frame.you!;
  switch (frame.phase) {
    case 'setup':
    case 'lobby':
      return (
        <div className="body">
          <h1>You&rsquo;re in.</h1>
          <p className="sub">Waiting for the host to start.</p>
          <TeamList frame={frame} />
        </div>
      );
    case 'board':
      return <Waiting title="Next question coming up" sub="Watch the big screen." />;
    case 'question':
      if (!you.answered || frame.round?.isFinal) {
        return <Answer frame={frame} msLeft={msLeft} onDone={apply} />;
      }
      return (
        <div className="body">
          <Clock msLeft={msLeft} />
          <h1>Answer&rsquo;s in.</h1>
          <p className="sub">
            {frame.round ? `${frame.round.answered} of ${frame.round.eligible} tables have answered.` : ''}
          </p>
          <Answer frame={frame} msLeft={msLeft} onDone={apply} />
        </div>
      );
    case 'reveal':
      return (
        <div className="body">
          <Clock msLeft={msLeft} note="until betting opens" />
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
    case 'betting':
      return <Betting frame={frame} msLeft={msLeft} onDone={apply} />;
    case 'scoring':
      return <Result frame={frame} />;
    case 'podium':
      return <Podium frame={frame} />;
    default:
      return <Waiting title="Hold on" />;
  }
}

// The delta is the hero, counted up, because "what did that round do to us"
// is the only question anybody has at this moment.
function Result({ frame }: { frame: PlayerFrame }) {
  const target = frame.you?.delta ?? 0;
  const shown = useCountUp(target, frame.round?.id ?? '');
  const cls = target > 0 ? 'delta up' : target < 0 ? 'delta down' : 'delta flat';
  return (
    <div className="body">
      <p className="sub" style={{ textAlign: 'center' }}>The answer was</p>
      <h1 style={{ textAlign: 'center' }}>{frame.scoring?.correctText || frame.scoring?.correctValue}</h1>
      <div className={cls}>{target > 0 ? '+' : ''}{money(shown)}</div>
      {frame.you?.wroteWinner ? (
        <p className="sub" style={{ textAlign: 'center' }}>You wrote the winning answer.</p>
      ) : null}
      <Standings frame={frame} />
    </div>
  );
}

function Podium({ frame }: { frame: PlayerFrame }) {
  const sorted = [...frame.teams].sort((a, b) => b.score - a.score);
  const winner = sorted[0];
  return (
    <div className="body">
      <h1>{winner ? `${winner.name} wins` : 'That&rsquo;s the game'}</h1>
      <Standings frame={frame} />
    </div>
  );
}

function Standings({ frame }: { frame: PlayerFrame }) {
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

function TeamList({ frame }: { frame: PlayerFrame }) {
  return (
    <div className="teamlist">
      {frame.teams.map((t) => (
        <span key={t.id} className={`tpill ${t.id === frame.you?.teamId ? 'in' : ''}`}>{t.name}</span>
      ))}
    </div>
  );
}

// Join, plus the host-issued reclaim code. There is deliberately no "pick
// your team from this list": with twenty names on a TV screen that would be
// an impersonation hole, so a table that lost its phone asks the host, who
// can see who is asking.
function Lobby({ frame, onJoined }: { frame: PlayerFrame; onJoined: (v: { teamId: string; name: string }) => void }) {
  const [name, setName] = useState('');
  const [err, setErr] = useState('');
  const [mode, setMode] = useState<'join' | 'reclaim'>('join');
  const [teamId, setTeamId] = useState('');
  const [code, setCode] = useState('');
  const running = useRef(false);

  const full = frame.teams.length >= 20;
  const finished = frame.phase === 'podium';

  const doJoin = async () => {
    if (running.current) return;
    running.current = true;
    try {
      const v = await join(name);
      writeLocal({ game: frame.game, teamId: v.teamId, name: v.name });
      onJoined(v);
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'could not join');
    } finally {
      running.current = false;
    }
  };

  const doReclaim = async () => {
    if (running.current) return;
    running.current = true;
    try {
      await reclaim(teamId, code);
      const v = await me();
      if (v) {
        writeLocal({ game: frame.game, teamId: v.teamId, name: v.name });
        onJoined(v);
      }
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'that code is not valid');
    } finally {
      running.current = false;
    }
  };

  if (mode === 'reclaim') {
    return (
      <div className="body">
        <h2>Get back in</h2>
        <p className="sub">Ask the host for your table&rsquo;s code.</p>
        <select className="field" value={teamId} onChange={(e) => setTeamId(e.target.value)}>
          <option value="">Which table?</option>
          {frame.teams.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
        </select>
        <input
          className="field big" type="text" inputMode="numeric" maxLength={4}
          placeholder="0000" value={code} onChange={(e) => setCode(e.target.value)}
        />
        <button className="btn" disabled={!teamId || code.length !== 4} onClick={() => void doReclaim()}>
          Rejoin
        </button>
        <button className="btn ghost" onClick={() => setMode('join')}>Back</button>
        <p className="err">{err}</p>
      </div>
    );
  }

  return (
    <div className="body">
      {/* The game name huge, so somebody who just scanned a QR can confirm
          they landed on the right thing. */}
      <p className="gamename">{frame.game}</p>
      <h1>{frame.title || 'Trivia'}</h1>

      {finished ? (
        <>
          <p className="sub">This game has finished.</p>
          <Standings frame={frame} />
        </>
      ) : full ? (
        <>
          <div className="banner">This game is full — you&rsquo;re watching along.</div>
          <TeamList frame={frame} />
        </>
      ) : (
        <>
          <input
            className="field" type="text" enterKeyHint="go" maxLength={40}
            placeholder="your table&rsquo;s name" value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') void doJoin(); }}
          />
          <button className="btn" disabled={!name.trim()} onClick={() => void doJoin()}>Join</button>
          <p className="err">{err}</p>
          {/* The live team list from the spectator stream, so a latecomer can
              see the party is real before committing a name. */}
          <TeamList frame={frame} />
          <button className="btn ghost" onClick={() => setMode('reclaim')}>
            Already playing? Get back in
          </button>
        </>
      )}
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
