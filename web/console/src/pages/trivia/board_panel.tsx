import { useState } from 'react';
import { api } from '../../api';
import { money, type BoardQuestion, type HostFrame, type TopicCount, type TriviaGame } from './common';

// The column picker, the board preview, and the questions behind it.
//
// Columns are a HOST decision, defaulted rather than imposed, because
// "Sports" and "Sportsball" arriving from a CSV as two topics is a real thing
// and the host has to see it and fix it. Auto rerolls among the viable ones.
export function BoardPanel({
  game, topics, state, cells, onBuilt, onCells,
}: {
  game: TriviaGame;
  topics: TopicCount[];
  state: HostFrame | null;
  cells: BoardQuestion[];
  onBuilt: (s: HostFrame) => void;
  onCells: (cells: BoardQuestion[]) => void;
}) {
  const [chosen, setChosen] = useState<string[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const cols = game.settings?.board_columns ?? 5;
  const rows = game.settings?.board_rows ?? 2;
  const repeats = game.settings?.repeat_questions ?? false;
  const locked = game.phase !== 'setup' && game.phase !== 'lobby';
  // What a category can actually field. Viability is measured in FRESH
  // questions rather than total, because fresh is what the builder will be
  // handed: offering a category with nine questions the room has heard and
  // none it has not would walk the host straight into a shortfall.
  //
  // The `repeats ? total` branch is not redundant with the server, which
  // reports unused === total for a game that allows them. It is what makes
  // ticking the box update these numbers on the spot: the counts in hand
  // were fetched under the OLD setting, and a host who ticks "allow repeats"
  // to fix a shortfall should not have to reload to see it fixed.
  const avail = (t: TopicCount) => (repeats ? t.total : t.unused);
  const viable = topics.filter((t) => avail(t) >= rows);

  const toggle = (key: string) => {
    setChosen((c) =>
      c.includes(key) ? c.filter((k) => k !== key) : c.length >= cols ? c : [...c, key],
    );
  };

  const build = async (auto: boolean) => {
    setBusy(true);
    setErr(null);
    try {
      onBuilt(await api.buildTriviaBoard(game.id, auto ? [] : chosen, auto));
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const swap = async (cellID: string) => {
    setBusy(true);
    setErr(null);
    try {
      const r = await api.swapTriviaCell(game.id, cellID);
      onCells(r.cells);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const board = state?.board ?? [];
  const byPos = new Map(board.map((c) => [`${c.col}:${c.row}`, c]));
  const headers: string[] = [];
  board.forEach((c) => { headers[c.col] = c.topic; });

  return (
    <section className="panel">
      <h2>The board</h2>
      <p className="page-sub">
        Pick {cols} categor{cols === 1 ? 'y' : 'ies'}, or hit Auto.{' '}
        {repeats
          ? 'This game may reuse questions from past nights; the ones the room heard longest ago come first.'
          : 'Only questions no game has asked yet are on offer — the count in brackets is what each category has left.'}
      </p>

      {viable.length === 0 ? (
        <p className="page-sub">
          No category has {rows} {repeats ? '' : 'fresh '}question{rows === 1 ? '' : 's'} left.{' '}
          {repeats
            ? 'Upload a sheet above.'
            : 'Upload more questions, delete an old game to free the ones it asked, or allow repeats above.'}
        </p>
      ) : (
        <div className="teamlist">
          {viable.map((t) => (
            <button
              key={t.key}
              className={chosen.includes(t.key) ? 'pill pill-ok' : 'pill'}
              onClick={() => toggle(t.key)}
            >
              {t.label} · {t.total}
              {repeats ? '' : ` (${t.unused} fresh)`}
            </button>
          ))}
        </div>
      )}

      {/* A shortfall counted in FRESH questions is a different problem from
          a shortfall counted in questions, and it has a different fix. Say
          which one it is rather than leaving the host to hunt for questions
          that are sitting in the set, already asked. */}
      {err ? (
        <p className="banner banner-error">
          {err}
          {err.includes('fresh question')
            ? ' — allow repeats on this game, or upload more questions.'
            : ''}
        </p>
      ) : null}
      <div className="page-head-actions">
        <button className="btn btn-spaced" onClick={() => void build(false)}
          disabled={busy || chosen.length !== cols}>
          Build with these {cols}
        </button>
        <button className="btn btn-spaced btn-danger" onClick={() => void build(true)} disabled={busy}>
          Auto
        </button>
      </div>

      {board.length ? (
        <div className="trivia-board" style={{ gridTemplateColumns: `repeat(${cols}, 1fr)` }}>
          {headers.map((h, i) => <div key={`h${i}`} className="trivia-cat">{h}</div>)}
          {Array.from({ length: rows }, (_, r) =>
            Array.from({ length: cols }, (_, c) => {
              const cell = byPos.get(`${c}:${r}`);
              return (
                <div key={`${c}:${r}`} className={cell?.played ? 'trivia-cell played' : 'trivia-cell'}>
                  {cell ? money(cell.points) : '—'}
                </div>
              );
            }),
          )}
        </div>
      ) : null}

      <QuestionList cells={cells} locked={locked} busy={busy} repeats={repeats} onSwap={swap} />
    </section>
  );
}

// The questions the grid above is hiding.
//
// Building a board used to be a leap of faith: ten cells of money with no way
// to see what was behind them until the room did. A category of ten good
// questions still has one that landed wrong, or one the regulars had last
// month somewhere else, and the host is the only person who can tell — so
// they have to be able to READ the board, and to change their mind about one
// tile without throwing away the nine they liked.
//
// Grouped by category rather than in grid order, because that is how a host
// reads a board they are checking: one column at a time, cheapest first.
function QuestionList({
  cells, locked, busy, repeats, onSwap,
}: {
  cells: BoardQuestion[];
  locked: boolean;
  busy: boolean;
  repeats: boolean;
  onSwap: (cellID: string) => void;
}) {
  if (cells.length === 0) return null;

  const columns = [...new Set(cells.map((c) => c.col))].sort((a, b) => a - b);
  // Spares are per category, and every cell in a column reports the same
  // number, so the header is where it belongs.
  const sparesOf = (col: number) => cells.find((c) => c.col === col)?.spares ?? 0;

  // Why a Swap button is off, in the words the host needs to fix it.
  const whyNot = (c: BoardQuestion) => {
    if (locked) return 'The game has started — questions are frozen.';
    if (c.played) return 'That cell has already been asked.';
    if (c.spares === 0) {
      return repeats
        ? `Nothing else in ${c.topic} that this board isn't already using.`
        : `No other fresh question in ${c.topic} — upload more, or allow repeats above.`;
    }
    return '';
  };

  return (
    <>
      <h3 className="trivia-qhead">What it will ask</h3>
      <p className="page-sub">
        {locked
          ? 'The game has started, so the board is frozen — this is what is left to come.'
          : 'Read it before the room does. Swap rotates one tile to the next question in ' +
            'that category and leaves the rest of the board alone.'}
      </p>
      {columns.map((col) => {
        const inCol = cells.filter((c) => c.col === col).sort((a, b) => a.row - b.row);
        const spare = sparesOf(col);
        return (
          <div key={col} className="trivia-qgroup">
            <div className="trivia-qcat">
              {inCol[0]?.topic}
              <span className="trivia-qspare">
                {spare === 0 ? 'nothing spare' : `${spare} spare`}
              </span>
            </div>
            <ul className="card-list">
              {inCol.map((c) => {
                const reason = whyNot(c);
                return (
                  <li key={c.id} className={c.played ? 'card trivia-q played' : 'card trivia-q'}>
                    <div className="card-main">
                      <span className="card-title">{money(c.points)} · {c.prompt}</span>
                      <span className="card-desc">
                        Answer: {c.answer}
                        {c.played ? ' · already asked' : ''}
                      </span>
                    </div>
                    <div className="card-side">
                      <button className="btn btn-ghost btn-sm" title={reason}
                        disabled={busy || reason !== ''} onClick={() => onSwap(c.id)}>
                        Swap
                      </button>
                    </div>
                  </li>
                );
              })}
            </ul>
          </div>
        );
      })}
    </>
  );
}
