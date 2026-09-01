import { useEffect, useRef, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { api, type TriviaGame } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { defaultSettings, money, type Dataset, type HostFrame, type TopicCount, type TriviaSettings } from './common';

// Everything a host does before the doors open: upload a question sheet, set
// the shape of the game, choose the board's columns, and check the preview.
export default function TriviaSetup() {
  useSetChatContext('the Trivia setup page');
  const { id = '' } = useParams();
  const [game, setGame] = useState<TriviaGame | null>(null);
  const [topics, setTopics] = useState<TopicCount[]>([]);
  const [datasets, setDatasets] = useState<Dataset[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [state, setState] = useState<HostFrame | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = () => {
    api.triviaGame(id)
      .then((r) => {
        setGame(r.game);
        setTopics(r.topics ?? []);
        setDatasets(r.datasets ?? []);
        setSelected(r.selected ?? []);
        setState(r.state);
      })
      .catch((e) => setErr(e.message));
  };
  useEffect(load, [id]);

  if (!game) {
    return <p className="page-sub">{err ?? 'Loading…'}</p>;
  }

  return (
    <>
      <div className="crumbs"><Link to="/trivia">Trivia</Link> / {game.title}</div>
      <div className="page-head page-head-row">
        <div>
          <h1>{game.title}</h1>
          <p className="page-sub">
            Players join at <code>{game.join_url}</code> — or by scanning the QR code on the TV.
          </p>
          {/* The stable address leads, because it is the one that should end
              up on the screen. Advertising the per-game URL as "the TV URL"
              sends a host to retype it at the television every week, which is
              exactly the chore the stable one removes. */}
          <p className="page-sub">
            Put the TV on <code>{game.screen_url}</code> once and leave it — it always shows the
            newest game. <a href={game.tv_url} target="_blank" rel="noreferrer">Open just this
            game&rsquo;s screen</a> if you need to pin one night.
          </p>
        </div>
        <Link className="btn" to={`/trivia/${game.id}/live`}>Run it</Link>
      </div>

      {err ? <p className="banner banner-error">{err}</p> : null}

      <DatasetPicker datasets={datasets} selected={selected} onChanged={load} />
      <SettingsPanel game={game} onSaved={(g) => setGame(g)} />
      <BoardPanel game={game} topics={topics} state={state} onBuilt={(s) => { setState(s); load(); }} />
    </>
  );
}

// Which sets THIS GAME draws from. Managing the sets themselves — adding,
// uploading, deleting — lives on the Trivia admin page, because a set belongs
// to the workspace and outlives any one night; only the choice is per game.
function DatasetPicker({
  datasets, selected, onChanged,
}: {
  datasets: Dataset[];
  selected: string[];
  onChanged: () => void;
}) {
  const { id = '' } = useParams();
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // No ticks means every set. Ticking is how you narrow.
  const usingAll = selected.length === 0;
  const isOn = (d: Dataset) => usingAll || selected.includes(d.id);

  const toggle = async (d: Dataset) => {
    setBusy(true);
    setErr(null);
    try {
      // Turning one off while "all" is implied has to become an explicit list
      // of the rest, or the click would appear to do nothing.
      const base = usingAll ? datasets.map((x) => x.id) : selected;
      const next = base.includes(d.id) ? base.filter((x) => x !== d.id) : [...base, d.id];
      await api.setTriviaGameDatasets(id, next.length === datasets.length ? [] : next);
      onChanged();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="panel">
      <h2>Question sets</h2>
      {datasets.length === 0 ? (
        <p className="page-sub">
          No question sets yet — add one on the{' '}
          <Link to="/admin/trivia">Trivia questions</Link> page.
        </p>
      ) : (
        <>
          <p className="page-sub">
            This game builds its board from the ticked sets. With none ticked it uses everything.{' '}
            <Link to="/admin/trivia">Manage sets</Link>.
          </p>
          <ul className="card-list">
            {datasets.map((d) => (
              <li key={d.id} className="card">
                <div className="card-main">
                  <label className="card-title">
                    <input type="checkbox" checked={isOn(d)} disabled={busy}
                      onChange={() => void toggle(d)} />{' '}
                    {d.name}
                  </label>
                  <span className="card-desc">
                    {d.questions} question{d.questions === 1 ? '' : 's'} · {d.topics} topic
                    {d.topics === 1 ? '' : 's'}
                  </span>
                </div>
              </li>
            ))}
          </ul>
        </>
      )}
      {err ? <p className="banner banner-error">{err}</p> : null}
    </section>
  );
}

// The knobs. Board size, values and the final are SETTINGS, never a game
// mode: a quick game and a long game are the same code with different
// numbers.
function SettingsPanel({ game, onSaved }: { game: TriviaGame; onSaved: (g: TriviaGame) => void }) {
  const [s, setS] = useState<TriviaSettings>(game.settings ?? defaultSettings());
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const locked = game.phase !== 'setup' && game.phase !== 'lobby';
  const dirty = useRef(false);

  useEffect(() => setS(game.settings ?? defaultSettings()), [game]);

  // Settings save themselves. There is nothing to confirm here — every field
  // is a number or a checkbox, and a host who changes the timer and walks off
  // to start the game should not lose it to a button they did not know about.
  // Debounced so typing in a number field is one write, not one per keystroke.
  useEffect(() => {
    if (!dirty.current || locked) return;
    const t = window.setTimeout(() => {
      setErr(null);
      api.updateTriviaGame(game.id, s)
        .then((g) => {
          onSaved(g);
          setSaved(true);
          window.setTimeout(() => setSaved(false), 1500);
        })
        .catch((e) => setErr((e as Error).message));
    }, 600);
    return () => window.clearTimeout(t);
  }, [s, locked, game.id, onSaved]);

  // edit marks the form dirty so the effect above only fires for real edits,
  // not for the initial load or a refresh from the server.
  const edit = (next: TriviaSettings) => {
    dirty.current = true;
    setS(next);
  };

  const setRows = (rows: number) => {
    // Cell values follow the row count, cheapest first, so the two can never
    // disagree — the server rejects a mismatch and the host should never see
    // that error.
    const values = Array.from({ length: rows }, (_, i) => s.cell_values[i] ?? (i + 1) * 100);
    edit({ ...s, board_rows: rows, cell_values: values });
  };

  return (
    <section className="panel">
      <h2>Settings</h2>
      {locked ? (
        <p className="page-sub">The game has started — settings are frozen so scores can&rsquo;t be restated.</p>
      ) : null}
      <div className="field-row">
        <label className="field">
          <span>Name on the TV</span>
          <input value={s.title} disabled={locked} onChange={(e) => edit({ ...s, title: e.target.value })} />
        </label>
        <label className="field">
          <span>Categories</span>
          <input type="number" min={1} max={8} value={s.board_columns} disabled={locked}
            onChange={(e) => edit({ ...s, board_columns: Number(e.target.value) })} />
        </label>
        <label className="field">
          <span>Rows</span>
          <input type="number" min={1} max={5} value={s.board_rows} disabled={locked}
            onChange={(e) => setRows(Number(e.target.value))} />
        </label>
      </div>

      <div className="field-row">
        {s.cell_values.map((v, i) => (
          <label className="field" key={i}>
            <span>Row {i + 1} cell value</span>
            <input type="number" min={1} value={v} disabled={locked}
              onChange={(e) => {
                const next = s.cell_values.slice();
                next[i] = Number(e.target.value);
                edit({ ...s, cell_values: next });
              }} />
          </label>
        ))}
      </div>
      <p className="page-sub">
        Cells and chips are the same size by default, which makes betting the larger half of the
        game: only the table that wrote the winning answer takes a cell, but every table places
        chips every round. Raise the cell values if you want knowing the answer to outweigh reading
        the room.
      </p>

      <div className="field-row">
        <label className="field">
          <span>Answering (s)</span>
          <input type="number" min={5} max={600} value={s.answer_seconds} disabled={locked}
            onChange={(e) => edit({ ...s, answer_seconds: Number(e.target.value) })} />
        </label>
        <label className="field">
          <span>Reveal (s)</span>
          <input type="number" min={5} max={600} value={s.reveal_seconds} disabled={locked}
            onChange={(e) => edit({ ...s, reveal_seconds: Number(e.target.value) })} />
        </label>
        <label className="field">
          <span>Betting (s)</span>
          <input type="number" min={5} max={600} value={s.bet_seconds} disabled={locked}
            onChange={(e) => edit({ ...s, bet_seconds: Number(e.target.value) })} />
        </label>
      </div>

      <label className="field">
        <span>
          <input type="checkbox" checked={s.final_wager} disabled={locked}
            onChange={(e) => edit({ ...s, final_wager: e.target.checked })} />
          {' '}Final wager
        </span>
      </label>
      <p className="page-sub">
        The only round where a table stakes its own money. Switch it off for a first night and
        scores only ever go up — the emptied board goes straight to the podium and no stake
        control appears on any phone.
      </p>

      {err ? <p className="banner banner-error">{err}</p> : null}
      {!locked ? <p className="page-sub">{saved ? 'Saved.' : 'Changes save themselves.'}</p> : null}
    </section>
  );
}

// The column picker and the board preview.
//
// Columns are a HOST decision, defaulted rather than imposed, because
// "Sports" and "Sportsball" arriving from a CSV as two topics is a real thing
// and the host has to see it and fix it. Auto rerolls among the viable ones.
function BoardPanel({
  game, topics, state, onBuilt,
}: {
  game: TriviaGame;
  topics: TopicCount[];
  state: HostFrame | null;
  onBuilt: (s: HostFrame) => void;
}) {
  const [chosen, setChosen] = useState<string[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const cols = game.settings?.board_columns ?? 5;
  const rows = game.settings?.board_rows ?? 2;
  const viable = topics.filter((t) => t.total >= rows);

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

  const board = state?.board ?? [];
  const byPos = new Map(board.map((c) => [`${c.col}:${c.row}`, c]));
  const headers: string[] = [];
  board.forEach((c) => { headers[c.col] = c.topic; });

  return (
    <section className="panel">
      <h2>The board</h2>
      <p className="page-sub">
        Pick {cols} categor{cols === 1 ? 'y' : 'ies'}, or hit Auto. Questions the room hasn&rsquo;t
        heard recently are preferred, so a weekly quiz doesn&rsquo;t repeat itself.
      </p>

      {viable.length === 0 ? (
        <p className="page-sub">
          No category has {rows} question{rows === 1 ? '' : 's'} yet. Upload a sheet above.
        </p>
      ) : (
        <div className="teamlist">
          {viable.map((t) => (
            <button
              key={t.key}
              className={chosen.includes(t.key) ? 'pill pill-ok' : 'pill'}
              onClick={() => toggle(t.key)}
            >
              {t.label} · {t.total} ({t.unused} unused)
            </button>
          ))}
        </div>
      )}

      {err ? <p className="banner banner-error">{err}</p> : null}
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
    </section>
  );
}
