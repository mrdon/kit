import { useEffect, useRef, useState } from 'react';
import { join, me, money, PHASE, reclaim, type PlayerFrame } from './api';
import { useStream, useWakeLock } from './useStream';
import { Answer, Wager, WagerWatching, Waiting } from './screens';
import { Betting } from './betting';
import { Podium, Result, Standings } from './results';
import { sittingOutScreen, WatchingCards } from './waiting';

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
  const { frame, connected, msLeft, apply, reopen } = useStream();
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
        <span className="who">{you?.name ?? identity?.name ?? frame.title}</span>
        <span>
          {!connected ? <span className="offline">reconnecting… </span> : null}
          {you ? <span className="score">{money(you.score)}</span> : null}
        </span>
      </header>

      {spectating && checked ? (
        <Lobby
          frame={frame}
          onJoined={(v) => {
            setIdentity(v);
            // The stream was opened without a cookie, so it is a spectator
            // stream. Reopen it now that we have an identity.
            reopen();
          }}
        />
      ) : null}
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
  // A table that joined during the question in flight is in the room but not
  // in THIS round, so the two phases where it would otherwise ACT are
  // diverted before the switch. Everything else it gets as normal.
  const out = sittingOutScreen(frame, msLeft);
  if (out) return out;
  switch (frame.phase) {
    case PHASE.SETUP:
    case PHASE.LOBBY:
      return (
        <div className="body">
          <h1>You&rsquo;re in.</h1>
          <p className="sub">Waiting for the host to start.</p>
          <TeamList frame={frame} />
          {/* Most people at a table have never played this. The wait before
              the first question is exactly when they have time to read it. */}
          <Rules frame={frame} />
        </div>
      );
    case PHASE.BOARD:
      // Between questions is the one moment a table has nothing to do, so it
      // is the right time to tell them where they stand. "Next question
      // coming up" told them nothing they could not see on the wall.
      return <BetweenQuestions frame={frame} />;
    case PHASE.INTERMISSION:
      // NOT the same screen as the board, though it looked close enough to
      // reuse. Between questions the pick line is the one thing to act on;
      // during a ten-minute break it is an instruction to shout a category
      // at a host who is at the bar, about a board that does not exist yet.
      return <Break frame={frame} />;
    case PHASE.WAGER: {
      // The blind bet. No prompt has been sent, so there is nothing else this
      // screen could show even if it wanted to.
      //
      // A table that walked in during the final is not in this round at all —
      // the server refuses its wager — so it watches rather than being handed
      // a slider that 409s.
      const seat = frame.teams.find((t) => t.id === you.teamId);
      if (seat && !seat.eligible) return <WagerWatching frame={frame} msLeft={msLeft} />;
      return <Wager frame={frame} msLeft={msLeft} onDone={apply} />;
    }
    case PHASE.QUESTION:
      // ONE tree whether or not the answer is in. Wrapping the answered case
      // in its own panel remounted <Answer> the moment the flag flipped, which
      // stacked a second clock above its own and wiped the number the table
      // had just typed. The answered count lives inside Answer now.
      return <Answer frame={frame} msLeft={msLeft} onDone={apply} />;
    case PHASE.REVEAL:
      return <WatchingCards frame={frame} msLeft={msLeft} note="until betting opens" />;
    case PHASE.BETTING:
      return <Betting frame={frame} msLeft={msLeft} onDone={apply} />;
    case PHASE.SCORING:
      return <Result frame={frame} />;
    case PHASE.PODIUM:
      return <Podium frame={frame} />;
    default:
      return <Waiting title="Hold on" />;
  }
}

// Break: the room is at the bar and this table wants two things — to know
// the night paused on purpose rather than stalled, and to know where they
// stand going into the next board.
//
// It also carries the ONE announcement the phone never made: the money goes
// up. The TV says it, the host says it, and until this screen existed the
// phone just started handing out bigger chips.
function Break({ frame }: { frame: PlayerFrame }) {
  const chips = frame.tokens.map((t) => money(t)).join(' and ');
  // The last break of the night has the final behind it, not a board, so
  // there are no new cell or chip values to promise.
  const boardSpent = frame.board.length > 0 && frame.board.every((c) => c.played);
  const finalNext = boardSpent && frame.finalWager;
  return (
    <div className="body">
      <h1>Break</h1>
      <p className="sub" style={{ textAlign: 'center' }}>
        Back in a few minutes. Get a drink.
      </p>
      {finalNext ? (
        <p className="sub" style={{ textAlign: 'center' }}>
          <strong>The final is next.</strong> You will see the category and set your wager
          before you see the question.
        </p>
      ) : frame.roundCount > 1 ? (
        <p className="sub" style={{ textAlign: 'center' }}>
          <strong>Round {frame.roundNumber} of {frame.roundCount}</strong> is next.
          Every question is worth more, and your chips go up to {chips}.
        </p>
      ) : null}
      <Standings frame={frame} />
      <RulesReminder frame={frame} />
    </div>
  );
}

// BetweenQuestions: where this table stands, with their own row called out.
function BetweenQuestions({ frame }: { frame: PlayerFrame }) {
  const sorted = [...frame.teams].sort((a, b) => b.score - a.score);
  const me = frame.you;
  // Shared score means shared rank — two tables on $400 are both 2nd, and
  // telling one of them they are 3rd would be wrong in a way they can check
  // against the wall. So the rank is one more than the number of tables
  // strictly ahead, never the position in the sorted list.
  const rank = me ? sorted.filter((t) => t.score > me.score).length + 1 : 0;
  const tied = me ? sorted.filter((t) => t.score === me.score).length > 1 : false;
  const leader = sorted[0];
  const behind = me && leader ? leader.score - me.score : 0;

  // Whose pick it is, above the rank, because it is the only thing on this
  // screen anybody has to ACT on: a table holding the pick has to shout a
  // category at the host, and a table that is not gets a name to grumble at.
  // A spent board has nothing left to pick, so the pick line would send a
  // table shouting a category at a grid that is struck through. The host
  // console has guarded this all along; the phone did not.
  const boardSpent = frame.board.length > 0 && frame.board.every((c) => c.played);
  const picker = boardSpent ? null : frame.picker;
  const mine = !!picker && !!me && picker.teamId === me.teamId;

  return (
    <div className="body">
      {picker ? (
        mine
          ? <h1 className="your-pick">It&rsquo;s your pick. Tell the host which category.</h1>
          : <p className="sub" style={{ textAlign: 'center' }}>{picker.name} picks the next category</p>
      ) : null}
      {me ? (
        <>
          <p className="sub" style={{ textAlign: 'center' }}>You&rsquo;re</p>
          <div className="rank-hero">
            {tied ? '=' : ''}{ordinal(rank)}
            <span className="of"> of {sorted.length}</span>
          </div>
          <p className="sub" style={{ textAlign: 'center' }}>
            {leader && leader.score === 0
              // Everybody on nothing is not a leader, it is question one.
              // Crowning six tables at once makes the word mean nothing by
              // the time somebody has actually earned it.
              ? 'Nothing on the board yet.'
              : rank === 1
                ? `Leading.${picker ? '' : ' Next question shortly.'}`
                : `${money(behind)} behind ${leader.name}.`}
          </p>
        </>
      ) : (
        picker ? null : <h1>Next question shortly</h1>
      )}
      <Standings frame={frame} />
      <RulesReminder frame={frame} />
    </div>
  );
}

// ordinal renders 1 as "1st". Small thing, but "You're 3" reads as a score.
function ordinal(n: number): string {
  const rem100 = n % 100;
  if (rem100 >= 11 && rem100 <= 13) return `${n}th`;
  switch (n % 10) {
    case 1: return `${n}st`;
    case 2: return `${n}nd`;
    case 3: return `${n}rd`;
    default: return `${n}th`;
  }
}

// Rules, straight from the server so this and the TV always agree.
function Rules({ frame }: { frame: PlayerFrame }) {
  if (!frame.rules?.length) return null;
  return (
    <div className="rules">
      <h2>How it works</h2>
      <ol>
        {frame.rules.map((r, i) => <li key={i}>{r}</li>)}
      </ol>
    </div>
  );
}

// The same rules, folded away, for the screens where a table already knows
// roughly what is going on.
//
// They used to appear on the lobby and the join form and NOWHERE ELSE, so a
// table that joined at question seven -- which the join corner deliberately
// invites all night -- got one look while typing a name and never again.
// Between questions is the one screen with nothing else on it.
function RulesReminder({ frame }: { frame: PlayerFrame }) {
  if (!frame.rules?.length) return null;
  return (
    <details className="rules-reminder">
      <summary>How it works</summary>
      <ol>
        {frame.rules.map((r, i) => <li key={i}>{r}</li>)}
      </ol>
    </details>
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
//
// Lobby itself is only the router between the three states a phone with no
// identity can be in — the game is over, the room is full, or there is a form
// to fill in — because the form and the reclaim screen each carry their own
// state and neither wants to know about the other.
function Lobby({ frame, onJoined }: { frame: PlayerFrame; onJoined: (v: { teamId: string; name: string }) => void }) {
  const [mode, setMode] = useState<'join' | 'reclaim'>('join');

  if (mode === 'reclaim') {
    return <ReclaimForm frame={frame} onJoined={onJoined} onBack={() => setMode('join')} />;
  }

  const full = frame.teams.length >= 20;
  const finished = frame.phase === PHASE.PODIUM;
  return (
    <div className="body">
      {/* The night's name, big, so somebody who just scanned a QR can confirm
          they landed on the right thing. NEVER the URL slug: "puffing-buffalo"
          confirms nothing to a person holding a phone in a bar. */}
      <h1 className="gamename">{frame.title}</h1>

      {finished ? (
        <>
          <p className="sub">This game has finished.</p>
          <Standings frame={frame} />
        </>
      ) : full ? (
        <>
          <div className="banner">This game is full. You can watch along.</div>
          <TeamList frame={frame} />
        </>
      ) : (
        <JoinForm frame={frame} onJoined={onJoined} onReclaim={() => setMode('reclaim')} />
      )}
    </div>
  );
}

// The form, and the two things a latecomer has to be told before they use it.
//
// A bar fills up all night, so most tables join AFTER the first question, not
// before it — the note is the normal path, not an edge case. Without it a
// table types a name, lands on a waiting screen and concludes the app is
// broken; with it, the wait is something they were told about.
function JoinForm({ frame, onJoined, onReclaim }: {
  frame: PlayerFrame;
  onJoined: (v: { teamId: string; name: string }) => void;
  onReclaim: () => void;
}) {
  const [name, setName] = useState('');
  const [err, setErr] = useState('');
  const running = useRef(false);

  const inProgress = frame.phase !== PHASE.LOBBY && frame.phase !== PHASE.SETUP;
  // The final is where the door shuts. The round in flight being final covers
  // every phase the final passes through; the refusal text covers the race
  // where the host started it between this frame landing and the tap.
  const closing = !!frame.round?.isFinal || err.includes('this game is closing');

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

  if (closing) {
    // No form at all, because there is nothing left to join: a table arriving
    // now could not answer the final, could not wager into it, and the next
    // thing that happens is the podium. The standings go here instead, so
    // somebody standing in the room can at least watch the end.
    return (
      <>
        <div className="banner">The final question has started. This game is closed to new tables.</div>
        <Standings frame={frame} />
      </>
    );
  }

  return (
    <>
      {inProgress ? (
        <p className="sub">The game has started. You are in from the next question.</p>
      ) : null}
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
      <button className="btn ghost" onClick={onReclaim}>Already playing? Get back in</button>
      <Rules frame={frame} />
    </>
  );
}

// Getting back in with a code the host reads out. Its own component because
// it shares nothing with the join form but the callback.
function ReclaimForm({ frame, onJoined, onBack }: {
  frame: PlayerFrame;
  onJoined: (v: { teamId: string; name: string }) => void;
  onBack: () => void;
}) {
  const [teamId, setTeamId] = useState('');
  const [code, setCode] = useState('');
  const [err, setErr] = useState('');
  const running = useRef(false);

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
      <button className="btn ghost" onClick={onBack}>Back</button>
      <p className="err">{err}</p>
    </div>
  );
}
