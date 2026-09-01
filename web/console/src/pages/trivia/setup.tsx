import { useEffect, useRef, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { API_BASE, api, type TriviaGame } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { balanceWarning, defaultSettings, money, type HostFrame, type ImportReport, type TopicCount, type TriviaSettings } from './common';

// Everything a host does before the doors open: upload a question sheet, set
// the shape of the game, choose the board's columns, and check the preview.
export default function TriviaSetup() {
  useSetChatContext('the Trivia setup page');
  const { id = '' } = useParams();
  const [game, setGame] = useState<TriviaGame | null>(null);
  const [topics, setTopics] = useState<TopicCount[]>([]);
  const [state, setState] = useState<HostFrame | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = () => {
    api.triviaGame(id)
      .then((r) => {
        setGame(r.game);
        setTopics(r.topics ?? []);
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
      <div className="crumbs"><Link to="/trivia">Trivia</Link> / {game.name}</div>
      <div className="page-head page-head-row">
        <div>
          <h1>{game.title || game.name}</h1>
          <p className="page-sub">
            Players join at <code>{game.join_url}</code> · TV at <code>{game.tv_url}</code>
          </p>
        </div>
        <Link className="btn" to={`/trivia/${game.id}/live`}>Run it</Link>
      </div>

      {err ? <p className="banner banner-error">{err}</p> : null}

      <ImportPanel onImported={(r) => { setTopics(r.topics ?? []); load(); }} />
      <SettingsPanel game={game} onSaved={(g) => setGame(g)} />
      <BoardPanel game={game} topics={topics} state={state} onBuilt={(s) => { setState(s); load(); }} />
    </>
  );
}

// The CSV drop and its report. A three-hundred-row sheet with two typos is not
// a total failure, so the good rows land and the bad ones come back by line
// number — and the topic histogram comes back from the SAME call, because
// that is what the column picker below needs.
function ImportPanel({ onImported }: { onImported: (r: ImportReport) => void }) {
  const { id = '' } = useParams();
  const [report, setReport] = useState<ImportReport | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const loadStarter = async () => {
    setBusy(true);
    setErr(null);
    try {
      const r = await api.loadTriviaStarter();
      setReport(r);
      onImported(r);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const upload = async (file: File) => {
    setBusy(true);
    setErr(null);
    try {
      const form = new FormData();
      form.append('csv', file);
      // Multipart, so this one goes around the JSON helper — but it still
      // carries the CSRF header the server requires on every mutation.
      const res = await fetch(`${API_BASE}/trivia/questions/import`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'X-Kit-Web': '1' },
        body: form,
      });
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
      const r = (await res.json()) as ImportReport;
      setReport(r);
      onImported(r);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
      if (fileRef.current) fileRef.current.value = '';
    }
  };
  void id;

  return (
    <section className="panel">
      <h2>Question bank</h2>
      <p className="page-sub">
        A CSV with <code>question</code>, <code>topics</code> and <code>answer</code> columns, in any
        order. Topics are separated by semicolons. Answers must be numbers — the whole game is
        &ldquo;closest without going over&rdquo;. Re-uploading a corrected sheet updates in place
        rather than duplicating.
      </p>
      <div className="page-head-actions">
        <button className="btn" disabled={busy} onClick={() => void loadStarter()}>
          Load the starter pack
        </button>
        <a className="card-manage" href={`${API_BASE}/trivia/questions/sample`}>
          Download it as a template
        </a>
      </div>
      <p className="page-sub">
        62 questions across eleven topics — enough to build the default board several times over.
        Loading it twice is harmless: rows are matched on the question text and updated in place.
      </p>
      <input
        ref={fileRef}
        type="file"
        accept=".csv,text/csv"
        disabled={busy}
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) void upload(f);
        }}
      />
      {err ? <p className="banner banner-error">{err}</p> : null}
      {report ? (
        <div className="banner banner-ok">
          {report.imported} added, {report.updated} updated, {report.skipped_duplicates} duplicate
          {report.skipped_duplicates === 1 ? '' : 's'} skipped.
          {report.errors.length ? (
            <ul>
              {report.errors.map((e) => (
                <li key={e.line}>Line {e.line}: {e.message}</li>
              ))}
              {report.truncated ? <li>…and more.</li> : null}
            </ul>
          ) : null}
        </div>
      ) : null}
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

  useEffect(() => setS(game.settings ?? defaultSettings()), [game]);

  const save = async () => {
    setErr(null);
    try {
      onSaved(await api.updateTriviaGame(game.id, s));
      setSaved(true);
      window.setTimeout(() => setSaved(false), 2000);
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  const setRows = (rows: number) => {
    // Cell values follow the row count, cheapest first, so the two can never
    // disagree — the server rejects a mismatch and the host should never see
    // that error.
    const values = Array.from({ length: rows }, (_, i) => s.cell_values[i] ?? (i + 1) * 500);
    setS({ ...s, board_rows: rows, cell_values: values });
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
          <input value={s.title} disabled={locked} onChange={(e) => setS({ ...s, title: e.target.value })} />
        </label>
        <label className="field">
          <span>Categories</span>
          <input type="number" min={1} max={8} value={s.board_columns} disabled={locked}
            onChange={(e) => setS({ ...s, board_columns: Number(e.target.value) })} />
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
                setS({ ...s, cell_values: next });
              }} />
          </label>
        ))}
      </div>
      {balanceWarning(s) ? (
        <p className="banner">{balanceWarning(s)}</p>
      ) : (
        <p className="page-sub">
          Cell values sit well above chip values on purpose. Only the tables that WROTE the winning
          answer take the cell, so with a full room betting is the only income most teams reliably
          have — and the forced spread caps that at one chip, {money(Math.max(...s.token_values))} a
          round.
        </p>
      )}

      <div className="field-row">
        <label className="field">
          <span>Answering (s)</span>
          <input type="number" min={5} max={600} value={s.answer_seconds} disabled={locked}
            onChange={(e) => setS({ ...s, answer_seconds: Number(e.target.value) })} />
        </label>
        <label className="field">
          <span>Reveal (s)</span>
          <input type="number" min={5} max={600} value={s.reveal_seconds} disabled={locked}
            onChange={(e) => setS({ ...s, reveal_seconds: Number(e.target.value) })} />
        </label>
        <label className="field">
          <span>Betting (s)</span>
          <input type="number" min={5} max={600} value={s.bet_seconds} disabled={locked}
            onChange={(e) => setS({ ...s, bet_seconds: Number(e.target.value) })} />
        </label>
      </div>

      <label className="field">
        <span>
          <input type="checkbox" checked={s.final_wager} disabled={locked}
            onChange={(e) => setS({ ...s, final_wager: e.target.checked })} />
          {' '}Final wager
        </span>
      </label>
      <p className="page-sub">
        The only round where a table stakes its own money. Switch it off for a first night and
        scores only ever go up — the emptied board goes straight to the podium and no stake
        control appears on any phone.
      </p>

      {err ? <p className="banner banner-error">{err}</p> : null}
      <button className="btn btn-spaced" onClick={() => void save()} disabled={locked}>
        {saved ? 'Saved' : 'Save settings'}
      </button>
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
