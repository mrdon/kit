import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { api, type TriviaGame } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { useHostStream } from './useStream';
import { PHASE_LABEL, money, type HostFrame, type Phase } from './common';
import { StatusPanel } from './live_panel';
import { LastRoundRecap } from './live_lastround';

// The live driver: the page a host runs the night from.
//
// Board on the left, status panel on the right, one big primary button
// labelled by what happens next. The host is a CONTROLLER, not an authority —
// closing this tab does not stop the clock — so nothing here holds state the
// game depends on. Every click carries the phase it was made from, and a 409
// just means the stream already moved on.
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

  const secs = msLeft === null ? null : Math.ceil(msLeft / 1000);
  // The cards earn the full width from the moment they go up until the round
  // is put away — that is the stretch where the host is reading them out,
  // watching chips land on them and calling the winner. Outside it they
  // belong to a round that is over, and the recap under the board already
  // says what happened to them.
  const showCards = frame.slots.length > 0 && CARD_PHASES.has(frame.phase);

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

      {/* Three children, not two, and the cards come LAST in the document —
          stacked on a narrow screen that puts the panel above them, which is
          the order a host needs when the button is the thing they are
          reaching for. Two columns wide, the stylesheet puts the cards back
          under the board and lets the panel span both rows, which is what
          makes it possible for the panel to stay on screen at all. */}
      <div className="trivia-live">
        <section className="trivia-boardcol">
          <BoardGrid frame={frame} busy={busy} onPick={(cellId) => void act({ action: 'pick_cell', cell_id: cellId })} />
          {/* Directly under the board: the recap is the thing the host reads
              BEFORE asking a table to pick, so it sits where their eyes
              already are rather than below everything else on the page. */}
          {frame.phase === 'board' || frame.phase === 'podium' ? <LastRoundRecap frame={frame} /> : null}
        </section>
        <StatusPanel frame={frame} busy={busy} secs={secs} gameId={id}
          onAct={(body) => void act(body)} />
        {showCards ? <Cards frame={frame} /> : null}
      </div>
    </>
  );
}

// The phases where the answer cards are worth a full-width block of their
// own: up on the screen, being bet on, or just scored — plus the podium,
// where the final's cards are the last thing anyone argues about.
const CARD_PHASES = new Set<Phase>(['reveal', 'betting', 'scoring', 'podium']);

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

function Cards({ frame }: { frame: HostFrame }) {
  return (
    <section className="panel trivia-cards">
      <h2>Answers</h2>
      <ul className="card-list">
        {frame.slots.map((s) => (
          <li key={s.id} className={frame.scoring?.winningSlot === s.id ? 'card trivia-win' : 'card'}>
            <div className="card-main">
              <span className="card-title">
                {s.label}
                {frame.scoring?.winningSlot === s.id ? ' · WINNER' : ''}
              </span>
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
