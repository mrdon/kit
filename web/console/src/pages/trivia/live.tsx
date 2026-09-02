import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { api, type TriviaGame } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { useHostStream } from './useStream';
import { PHASE_LABEL, money, primaryAction, type HostFrame } from './common';

// The live driver: the page a host runs the night from.
//
// Board on the left, phase panel on the right, one big primary button labelled
// by what happens next. The host is a CONTROLLER, not an authority — closing
// this tab does not stop the clock — so nothing here holds state the game
// depends on. Every click carries the phase it was made from, and a 409 just
// means the stream already moved on.
export default function TriviaLive() {
  useSetChatContext('the Trivia live page');
  const { id = '' } = useParams();
  const nav = useNavigate();
  const [game, setGame] = useState<TriviaGame | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [ending, setEnding] = useState(false);
  const { frame, connected, msLeft, apply } = useHostStream(id);

  useEffect(() => {
    api.triviaGame(id).then((r) => setGame(r.game)).catch((e) => setErr(e.message));
  }, [id]);

  const act = async (body: Record<string, unknown>) => {
    if (busy || !frame) return;
    setBusy(true);
    setErr(null);
    try {
      apply(await api.triviaAction(id, { from_phase: frame.phase, ...body }));
      // Ending the game is the last thing a host does here, so leaving them
      // parked on a finished game's driver page is a dead end — the podium is
      // on the TV, not on their laptop.
      if (body.action === 'finish') nav('/trivia');
    } catch (e) {
      const msg = (e as Error).message;
      // A conflict is not an error the host has to do anything about: the
      // game moved on and the stream has already told us. Say so briefly
      // rather than showing a red bar over a working game.
      setErr(msg.includes('409') || msg.includes('already moved') ? 'That already happened.' : msg);
    } finally {
      setBusy(false);
    }
  };

  if (!frame || !game) {
    return <p className="page-sub">{err ?? 'Connecting…'}</p>;
  }

  const boardEmpty = frame.progress.cellsTotal > 0 && frame.progress.cellsPlayed === frame.progress.cellsTotal;
  const primary = primaryAction(frame.phase, boardEmpty, frame.finalWager, frame.progress.finalPlayed);
  const secs = msLeft === null ? null : Math.ceil(msLeft / 1000);

  return (
    <>
      <div className="crumbs"><Link to="/trivia">Trivia</Link> / {frame.title}</div>
      <div className="page-head page-head-row">
        <div>
          <h1>{frame.title}</h1>
          <p className="page-sub">
            {PHASE_LABEL[frame.phase]} · {frame.progress.cellsPlayed}/{frame.progress.cellsTotal} played
            {!connected ? ' · reconnecting…' : ''}
          </p>
          {/* Both addresses, while the host is actually running the night —
              the one for the screen and the one people are scanning. */}
          <p className="page-sub">
            Screen: <code>{game.screen_url}</code>{' '}
            (<a href={game.tv_url} target="_blank" rel="noreferrer">just this game</a>)
          </p>
          <p className="page-sub">
            Players: <code>{game.short_url}</code>
          </p>
        </div>
        <div className="page-head-actions">
          {/* Two taps: ending a game is not reversible and the button sits
              next to the ones a host presses every round. */}
          {ending ? (
            <>
              <button className="btn btn-danger" disabled={busy}
                onClick={() => void act({ action: 'finish' })}>
                Really end it
              </button>
              <button className="btn btn-danger" disabled={busy}
                onClick={() => setEnding(false)}>Cancel</button>
            </>
          ) : (
            <button className="btn btn-danger" disabled={busy} onClick={() => setEnding(true)}>
              End game
            </button>
          )}
        </div>
      </div>

      {err ? <p className="banner banner-error">{err}</p> : null}

      <div className="trivia-live">
        <section>
          <BoardGrid frame={frame} busy={busy} onPick={(cellId) => void act({ action: 'pick_cell', cell_id: cellId })} />
        </section>

        <aside className="trivia-panel">
          {/* The host sees the correct answer in every phase. They are reading
              it out and adjudicating nothing, so hiding it would be theatre
              with a cost. */}
          {frame.round ? (
            <>
              <p className="card-desc">
                {frame.round.isFinal ? 'FINAL · ' : ''}Question {frame.round.ordinal} · {money(frame.round.points)}
              </p>
              <p className="trivia-question">{frame.round.text}</p>
              {frame.answer ? (
                <p className="trivia-answer">Answer: <strong>{frame.answer.text || frame.answer.value}</strong></p>
              ) : null}
            </>
          ) : (
            <p className="card-desc">
              {frame.phase === 'board' ? 'Pick a cell to ask it.' : 'No question in play.'}
            </p>
          )}

          {secs !== null ? <div className="trivia-clock">{secs}</div> : null}
          {frame.round && frame.phase === 'question' ? (
            <p className="card-desc">{frame.round.answered} of {frame.round.eligible} in</p>
          ) : null}

          <div className="page-head-actions">
            {primary ? (
              <button className="btn" disabled={busy} onClick={() => void act({ action: primary.action })}>
                {primary.label}
              </button>
            ) : null}
            {secs !== null ? (
              <button className="btn btn-danger" disabled={busy}
                onClick={() => void act({ action: 'extend', seconds: 15 })}>
                +15s
              </button>
            ) : null}
          </div>

          <TeamChips frame={frame} gameId={id} />
        </aside>
      </div>

      {frame.slots.length ? <Cards frame={frame} /> : null}
      <Leaderboard frame={frame} />
    </>
  );
}

function BoardGrid({ frame, busy, onPick }: { frame: HostFrame; busy: boolean; onPick: (id: string) => void }) {
  const cols = Math.max(1, ...frame.board.map((c) => c.col + 1));
  const rows = Math.max(1, ...frame.board.map((c) => c.row + 1));
  const byPos = new Map(frame.board.map((c) => [`${c.col}:${c.row}`, c]));
  const headers: string[] = [];
  frame.board.forEach((c) => { headers[c.col] = c.topic; });
  const pickable = frame.phase === 'board';

  if (!frame.board.length) {
    return <p className="page-sub">No board yet — build one on the setup page.</p>;
  }
  return (
    <div className="trivia-board" style={{ gridTemplateColumns: `repeat(${cols}, 1fr)` }}>
      {headers.map((h, i) => <div key={`h${i}`} className="trivia-cat">{h}</div>)}
      {Array.from({ length: rows }, (_, r) =>
        Array.from({ length: cols }, (_, c) => {
          const cell = byPos.get(`${c}:${r}`);
          if (!cell) return <div key={`${c}:${r}`} className="trivia-cell">—</div>;
          return (
            <button
              key={cell.id}
              className={cell.played ? 'trivia-cell played' : 'trivia-cell live'}
              disabled={busy || cell.played || !pickable}
              onClick={() => onPick(cell.id)}
            >
              {money(cell.points)}
            </button>
          );
        }),
      )}
    </div>
  );
}

// One chip per table, lighting as answers and bets land — so the host can see
// WHICH table is holding everyone up rather than just a count.
function TeamChips({ frame, gameId }: { frame: HostFrame; gameId: string }) {
  const [code, setCode] = useState<{ team: string; code: string } | null>(null);

  const reissue = async (teamId: string, name: string) => {
    try {
      const r = await api.triviaReclaim(gameId, teamId);
      setCode({ team: name, code: r.code });
    } catch {
      /* the host can just try again */
    }
  };

  return (
    <>
      <h3 className="card-title">Tables</h3>
      <div className="teamlist">
        {frame.teams.map((t) => {
          const cls = t.stakeLocked ? 'pill pill-ok' : t.answered ? 'pill pill-ok' : 'pill pill-off';
          return (
            <button key={t.id} className={cls} title="Reissue this table's code"
              onClick={() => void reissue(t.id, t.name)}>
              {t.name}
              {frame.phase === 'betting' ? ` ${t.chipsPlaced}/${frame.tokens.length}` : ''}
              {t.stakeLocked ? ' 🔒' : ''}
            </button>
          );
        })}
      </div>
      {code ? (
        <p className="banner banner-ok">
          Read <strong>{code.code}</strong> to {code.team}. Their old phone is signed out.
        </p>
      ) : (
        <p className="card-desc">Tap a table to reissue its code if their phone died.</p>
      )}
    </>
  );
}

function Cards({ frame }: { frame: HostFrame }) {
  return (
    <section className="panel">
      <h2>Answers</h2>
      <ul className="card-list">
        {frame.slots.map((s) => (
          <li key={s.id} className={frame.scoring?.winningSlot === s.id ? 'card trivia-win' : 'card'}>
            <div className="card-main">
              <span className="card-title">{s.label}</span>
              <span className="card-desc">{s.teams.join(' · ') || 'nobody wrote this'}</span>
            </div>
            <div className="card-side">
              {s.chips.map((c, i) => (
                <span key={i} className="pill">{c.team} {money(c.amount)}</span>
              ))}
              {s.pot ? <span className="pill pill-ok">{money(s.pot)}</span> : null}
            </div>
          </li>
        ))}
      </ul>
    </section>
  );
}

function Leaderboard({ frame }: { frame: HostFrame }) {
  const sorted = [...frame.teams].sort((a, b) => b.score - a.score);
  return (
    <section className="panel">
      <h2>Standings</h2>
      <ul className="card-list">
        {sorted.map((t, i) => {
          const d = frame.scoring?.deltas?.[t.id];
          return (
            <li key={t.id} className="card">
              <div className="card-main">
                <span className="card-title">{i + 1}. {t.name}</span>
                {d ? <span className="card-desc">{d > 0 ? '+' : ''}{money(d)} this round</span> : null}
              </div>
              <div className="card-side"><span className="pill pill-ok">{money(t.score)}</span></div>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
