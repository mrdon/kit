import { useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { api, type TriviaGame } from '../api';
import { useSetChatContext } from '../chatContext';
import { SLUG } from '../workspace';
import { PHASE_LABEL } from './trivia/common';

// The game list, and the one button that starts a night.
//
// Creating a game asks for nothing. The name is drawn server-side because it
// is the public URL contract, and every setting has a default that IS the
// shipped game — a host who wants a different shape changes it on the setup
// page, where they can see the board they are describing.
export default function Trivia() {
  useSetChatContext('the Trivia page');
  const tvURL = `${window.location.origin}/${SLUG}/trivia/tv`;

  const nav = useNavigate();
  const [games, setGames] = useState<TriviaGame[] | null>(null);
  const [bank, setBank] = useState<{ total: number } | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState<string | null>(null);

  const remove = async (id: string) => {
    setBusy(true);
    setErr(null);
    try {
      await api.deleteTriviaGame(id);
      setConfirming(null);
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const load = () => {
    api.triviaGames().then((r) => setGames(r.games)).catch((e) => setErr(e.message));
    api.triviaQuestions().then(setBank).catch(() => undefined);
  };
  useEffect(load, []);

  const create = async () => {
    setBusy(true);
    setErr(null);
    try {
      // No settings: the server carries last week's setup forward.
      const g = await api.createTriviaGame();
      nav(`/trivia/${g.id}`);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="page-head page-head-row">
        <div>
          <h1>Trivia</h1>
          <p className="page-sub">
            A live pub quiz on the TV, with every table playing from their phone.
            {bank ? ` ${bank.total} question${bank.total === 1 ? '' : 's'} available.` : ''}
          </p>
          {/* One address for the screen, forever: it always shows the newest
              game, so nobody has to retype a URL at the TV each week. */}
          <p className="page-sub">
            Point the TV at <code>{tvURL}</code> once and leave it — it follows the newest game.
          </p>
        </div>
        <button className="btn" onClick={() => void create()} disabled={busy}>
          New game
        </button>
      </div>

      {err ? <p className="banner banner-error">{err}</p> : null}
      {games === null ? <p className="page-sub">Loading…</p> : null}
      {games !== null && games.length === 0 ? (
        <p className="page-sub">
          No games yet. Create one, upload a question sheet, then put the TV URL on a screen.
        </p>
      ) : null}

      <ul className="card-list">
        {(games ?? []).map((g) => (
          <li key={g.id} className="card">
            <div className="card-main">
              <span className="card-title">{g.title}</span>
              <span className="card-desc">
                {g.teams} team{g.teams === 1 ? '' : 's'}
                {g.cells ? ` · ${g.played}/${g.cells} played` : ' · no board yet'}
                {g.leader ? ` · leading: ${g.leader}` : ''}
              </span>
            </div>
            <div className="card-side">
              <span className={g.phase === 'podium' ? 'pill pill-off' : 'pill pill-ok'}>
                {PHASE_LABEL[g.phase]}
              </span>
              <Link className="card-manage" to={`/trivia/${g.id}`}>Set up</Link>
              <Link className="card-manage" to={`/trivia/${g.id}/live`}>Run it</Link>
              {/* Two taps, because deleting a game takes its scores with it
                  and there is no undo. */}
              {confirming === g.id ? (
                <>
                  <button className="btn btn-danger" disabled={busy}
                    onClick={() => void remove(g.id)}>Really delete</button>
                  <button className="btn btn-danger" disabled={busy}
                    onClick={() => setConfirming(null)}>Cancel</button>
                </>
              ) : (
                <button className="btn btn-danger" disabled={busy}
                  onClick={() => setConfirming(g.id)}>Delete</button>
              )}
            </div>
          </li>
        ))}
      </ul>
    </>
  );
}
