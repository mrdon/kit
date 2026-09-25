import { useEffect, useRef, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { api, type TriviaGame } from '../../api';
import { useSetChatContext } from '../../chatContext';
import { BoardPanel } from './board_panel';
import { NumberField } from './NumberField';
import {
  defaultSettings, estimateMinutes, PHASE, sameSettings,
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
            Players join at <code>{game.short_url}</code>, or by scanning the QR on the TV.
          </p>
          {/* The stable address leads, because it is the one that should end
              up on the screen. Advertising the per-game URL as "the TV URL"
              sends a host to retype it at the television every week, which is
              exactly the chore the stable one removes. */}
          <p className="page-sub">
            Put the TV on <code>{game.screen_url}</code> once and leave it. It always shows the
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
          No question sets yet. Add one on the{' '}
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
  const locked = game.phase !== PHASE.SETUP && game.phase !== PHASE.LOBBY;
  // What the server last told us it has. The save below fires only when the
  // form has drifted from THIS, which is the only reliable way to tell an
  // edit from an echo.
  const onServer = useRef<TriviaSettings>(game.settings ?? defaultSettings());
  // Held in a ref so re-rendering the parent cannot re-arm the save. It used
  // to sit in the dependency array, where a new closure every render was
  // enough on its own to fire it again.
  const notify = useRef(onSaved);
  notify.current = onSaved;

  useEffect(() => {
    const next = game.settings ?? defaultSettings();
    onServer.current = next;
    setS(next);
  }, [game]);

  // Settings save themselves. There is nothing to confirm here — every field
  // is a number or a checkbox, and a host who changes the timer and walks off
  // to start the game should not lose it to a button they did not know about.
  // Debounced so typing in a number field is one write, not one per keystroke.
  //
  // THE GUARD IS A VALUE COMPARISON, not a dirty flag, because a dirty flag
  // cannot tell an edit from the echo of one. This used to be `dirty.current`,
  // set on the first edit and never cleared, and the result was a save loop:
  // the PATCH reloaded the game, the reload handed back a fresh settings
  // object, the new identity re-fired this effect, and the tab wrote to the
  // database every 600ms for as long as it stayed open — bumping
  // state_version, redrawing the board, and flashing "Saved." the whole time.
  useEffect(() => {
    if (locked || sameSettings(s, onServer.current)) return;
    const t = window.setTimeout(() => {
      setErr(null);
      api.updateTriviaGame(game.id, s)
        .then((g) => {
          onServer.current = g.settings ?? s;
          notify.current(g);
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
  }, [s, locked, game.id]);

  const edit = (next: TriviaSettings) => setS(next);

  const setRows = (rows: number) => {
    // Every cell in a round is worth the SAME, so the values follow the row
    // count by repeating, not by climbing.
    //
    // The escalation lives on the round axis instead (round two is worth
    // double, chips included), and that is the only place it makes sense: a
    // question carries no difficulty grade and the board fills each column by
    // topic and a shuffle, so the row a question lands in is chance. A ladder
    // down the rows would tell the room the bottom one is harder when it is
    // not -- and once a cell outruns the biggest chip it also inverts the
    // game, because only the table that WROTE the winning answer takes a
    // cell while every table bets every round.
    //
    // Rows are therefore pure length: five rows is a longer round, not a
    // steeper one.
    const value = s.cell_values[0] ?? 100;
    edit({ ...s, board_rows: rows, cell_values: Array.from({ length: rows }, () => value) });
  };

  return (
    <section className="panel">
      <h2>Settings</h2>
      {locked ? (
        <p className="page-sub">The game has started. Settings are frozen so scores cannot be restated.</p>
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
          <span>Board rounds (not counting the final)</span>
          <NumberField min={1} max={5} value={s.board_rounds} disabled={locked}
            onCommit={(n) => edit({ ...s, board_rounds: n })} />
        </label>
      </div>
      {/* A host is not choosing three numbers, they are choosing a length.
          This is the only line on the page that answers the question they
          actually have. */}
      <p className="page-sub">
        <strong>About {estimateMinutes(s).minutes} minutes.</strong>{' '}
        {estimateMinutes(s).questions} question{estimateMinutes(s).questions === 1 ? '' : 's'}
        {' '}across {s.board_rounds + (s.final_wager ? 1 : 0)} rounds
        {s.final_wager ? ' counting the final' : ''}
        {s.board_rounds > 1 ? ', with 8 minutes a break' : ''}. Rows are what make a round longer;
        rounds are what add a break and step the money up.
      </p>

      {/* ONE field, not one per row. Every cell in a round is worth the same,
          so a column of per-row boxes was offering a ladder the game does not
          have — and the first thing anybody did with it was build one. */}
      <div className="field-row">
        <label className="field">
          <span>Cell value</span>
          <NumberField min={1} value={s.cell_values[0] ?? 100} disabled={locked}
            onCommit={(n) => edit({
              ...s,
              cell_values: Array.from({ length: s.board_rows }, () => n),
            })} />
        </label>
      </div>
      <p className="page-sub">
        Cells and chips are the same size by default, which makes betting the larger half of the
        game: only the table that wrote the winning answer takes a cell, but every table places
        chips every round. Raise the cell values if you want knowing the answer to outweigh reading
        the room — but keep them near the chips, or the betting stops mattering.
      </p>
      <p className="page-sub">
        Every cell in a round is worth this. Rows are <em>length</em>, not difficulty: questions
        carry no grade and the board fills each column by topic and a shuffle, so a dearer bottom
        row would look harder without being harder. The escalation lives on the rounds instead —
        round two is worth double, chips included.
      </p>

      <div className="field-row field-row-bottom">
        <label className="field">
          <span>Answering (s)</span>
          <NumberField min={5} max={600} value={s.answer_seconds} disabled={locked}
            onCommit={(n) => edit({ ...s, answer_seconds: n })} />
        </label>
        <label className="field">
          <span>Deal: cards shown before betting opens (s, 0 skips it)</span>
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
          <span>Wager: the final only (s)</span>
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
        On by default. The only round where a table stakes its own money, and the bet comes
        first: the room sees the category, puts an amount up blind, and only then gets the
        question. Switch it off for a first night and scores only ever go up, with no stake
        control on any phone — the board still waits for you at the end either way.
      </p>

      {s.final_wager ? (
        <>
          <label className="field">
            <span>
              <input type="checkbox" checked={s.break_before_final} disabled={locked}
                onChange={(e) => edit({ ...s, break_before_final: e.target.checked })} />
              {' '}Break before the final
            </span>
          </label>
          <p className="page-sub">
            Off by default. On a long night the final is worth a run-up, so the room gets the
            standings and a last drink before it. On a short one it stops everybody dead at the
            point they have finally stopped talking, one question from the end. You can always
            call a break yourself instead.
          </p>
        </>
      ) : null}

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
