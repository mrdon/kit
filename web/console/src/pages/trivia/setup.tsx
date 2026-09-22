import { useEffect, useRef, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { api, type TriviaGame } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { BoardPanel } from './board_panel';
import { NumberField } from './NumberField';
import {
  defaultSettings,
  type BoardQuestion, type Dataset, type TopicCount, type TriviaSettings,
} from './common';

// Everything a host does before the doors open: upload a question sheet, set
// the shape of the game, choose the board's columns, and check the preview.
export default function TriviaSetup() {
  useSetChatContext('the Trivia setup page');
  const { id = '' } = useParams();
  const [game, setGame] = useState<TriviaGame | null>(null);
  const [topics, setTopics] = useState<TopicCount[]>([]);
  const [datasets, setDatasets] = useState<Dataset[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  // The questions behind the board, which the SSE frames deliberately do not
  // carry — see board_panel.tsx.
  const [cells, setCells] = useState<BoardQuestion[]>([]);
  const [err, setErr] = useState<string | null>(null);

  const load = () => {
    api.triviaGame(id)
      .then((r) => {
        setGame(r.game);
        setTopics(r.topics ?? []);
        setDatasets(r.datasets ?? []);
        setSelected(r.selected ?? []);
        setCells(r.cells ?? []);
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
            Players join at <code>{game.short_url}</code> — or by scanning the QR on the TV.
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
      {/* A saved shape redraws the board server-side, so the preview and the
          topic counts are reloaded along with the game. */}
      <SettingsPanel game={game} onSaved={(g) => { setGame(g); load(); }} />
      {/* A rebuild changes the board, the topic counts and the spare counts
          at once, so the page reloads rather than patching three of them. */}
      <BoardPanel game={game} topics={topics} cells={cells}
        onBuilt={load} onCells={setCells} />
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
                    {d.fresh} fresh of {d.questions} question{d.questions === 1 ? '' : 's'} ·{' '}
                    {d.topics} topic{d.topics === 1 ? '' : 's'}
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
          if (g.board_error) {
            setErr(`Saved, but the board could not be redrawn: ${g.board_error}`);
            return;
          }
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
    //
    // They are drawn from the CHIP values rather than climbing in hundreds,
    // which is what the ladder here used to do: (i + 1) * 100 put a $500 cell
    // on a five-row board while the biggest chip was still $200, and that
    // quietly inverts the game. Only the table that wrote the winning answer
    // takes a cell; every table bets every round. Once a cell outruns the
    // chips, knowing beats reading the room and the betting stops mattering.
    //
    // Escalating also implies a difficulty ladder that does not exist. A
    // question carries a prompt, an answer and its topics — no grade — and
    // the board builder fills a column by topic and a shuffle, so the row a
    // question lands in is chance. A $500 bottom row tells the room the
    // question is harder, and it is not.
    //
    // So the rows spread across the chip vocabulary, cheapest first: two rows
    // give 100/200 (unchanged, the shipped default), five give
    // 100/100/100/200/200. The host can still set any row by hand below.
    const chips = s.token_values.length > 0 ? s.token_values : [100, 200];
    const values = Array.from(
      { length: rows },
      (_, i) => chips[Math.min(Math.floor((i * chips.length) / rows), chips.length - 1)],
    );
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
          <NumberField min={1} max={8} value={s.board_columns} disabled={locked}
            onCommit={(n) => edit({ ...s, board_columns: n })} />
        </label>
        <label className="field">
          <span>Rows</span>
          <NumberField min={1} max={5} value={s.board_rows} disabled={locked}
            onCommit={setRows} />
        </label>
        <label className="field">
          <span>Board rounds</span>
          <NumberField min={1} max={3} value={s.board_rounds} disabled={locked}
            onCommit={(n) => edit({ ...s, board_rounds: n })} />
        </label>
      </div>

      <div className="field-row">
        {s.cell_values.map((v, i) => (
          <label className="field" key={i}>
            <span>Row {i + 1} cell value</span>
            <NumberField min={1} value={v} disabled={locked}
              onCommit={(n) => {
                const next = s.cell_values.slice();
                next[i] = n;
                edit({ ...s, cell_values: next });
              }} />
          </label>
        ))}
      </div>
      <p className="page-sub">
        Cells and chips are the same size by default, which makes betting the larger half of the
        game: only the table that wrote the winning answer takes a cell, but every table places
        chips every round. Raise the cell values if you want knowing the answer to outweigh reading
        the room — but keep them near the chips, or the betting stops mattering.
      </p>
      <p className="page-sub">
        Rows are not difficulty. Questions carry no grade, and the board fills a column by topic
        and a shuffle, so a dearer row is worth more but is no harder — adding rows repeats the
        chip values rather than climbing, and a row you set by hand stays until you change the
        row count.
      </p>

      <div className="field-row field-row-bottom">
        <label className="field">
          <span>Answering (s)</span>
          <NumberField min={5} max={600} value={s.answer_seconds} disabled={locked}
            onCommit={(n) => edit({ ...s, answer_seconds: n })} />
        </label>
        <label className="field">
          <span>Deal — cards shown before betting opens (s, 0 skips it)</span>
          <NumberField min={0} max={600} value={s.reveal_seconds} disabled={locked}
            onCommit={(n) => edit({ ...s, reveal_seconds: n })} />
        </label>
        <label className="field">
          <span>Betting (s)</span>
          <NumberField min={5} max={600} value={s.bet_seconds} disabled={locked}
            onCommit={(n) => edit({ ...s, bet_seconds: n })} />
        </label>
        {/* Only reachable with the final on — the phase never opens otherwise
            — so it says so rather than sitting there looking universal. */}
        <label className="field">
          <span>Wager — the final only (s)</span>
          <NumberField min={5} max={600} value={s.wager_seconds} disabled={locked}
            onCommit={(n) => edit({ ...s, wager_seconds: n })} />
        </label>
        {/* Zero is a real choice here and the old behaviour, so the floor is
            0 rather than the 5 every other timer carries. */}
        <label className="field">
          <span>Grace after everyone&rsquo;s in (s)</span>
          <NumberField min={0} max={60} value={s.grace_seconds} disabled={locked}
            onCommit={(n) => edit({ ...s, grace_seconds: n })} />
        </label>
      </div>
      <p className="page-sub">
        The deal is the beat between the last answer landing and the chips coming out: the cards
        fly up on the screen with nobody&rsquo;s money on them yet. Off by default (0), so betting
        opens the moment the cards do and the room reads them while it bets; a few seconds is
        plenty if you want the beat, and longer just sags.
      </p>
      <p className="page-sub">
        The grace is what the last table gets. When everyone is in, the clock drops to this instead
        of the phase ending on the spot, so whoever answered or placed last still gets a few seconds
        to look at the room and change their mind. Set it to 0 to close the instant the last table
        is in.
      </p>

      <label className="field">
        <span>
          <input type="checkbox" checked={s.final_wager} disabled={locked}
            onChange={(e) => edit({ ...s, final_wager: e.target.checked })} />
          {' '}Final wager
        </span>
      </label>
      <p className="page-sub">
        The only round where a table stakes its own money, and the bet comes first: the room
        sees the category, puts an amount up blind, and only then gets the question. Switch it
        off for a first night and scores only ever go up — the emptied board goes straight to
        the podium and no stake control appears on any phone.
      </p>

      {/* Locked with the rest once the game starts: the board is already
          built by then, so flipping this halfway through a night would
          change nothing except what the final draws from. */}
      <label className="field">
        <span>
          <input type="checkbox" checked={s.repeat_questions} disabled={locked}
            onChange={(e) => edit({ ...s, repeat_questions: e.target.checked })} />
          {' '}Allow questions from past games
        </span>
      </label>
      <p className="page-sub">
        Off by default: a question this workspace has already asked will not come back, and the
        regulars are exactly the people who would notice. Only questions that were actually read
        out count — a cell nobody opened is still fresh — and deleting an old game puts its
        questions back in the pot. Turn this on if your bank has run thin.
      </p>

      {err ? <p className="banner banner-error">{err}</p> : null}
      {!locked ? <p className="page-sub">{saved ? 'Saved.' : 'Changes save themselves.'}</p> : null}
    </section>
  );
}
