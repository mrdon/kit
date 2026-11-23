import { useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { api, type HoppersGame, type HoppersPack, type HoppersRules } from '../api';
import { useSetChatContext } from '../chatContext';
import { SLUG } from '../workspace';
import { NumberField } from './trivia/NumberField';
import { HOP } from '../hoppers/consts.js';

// The Hoppers game list, and the form that starts a night: a pack and the
// few rules worth changing at the bar. Everything is decided before the night
// is created; to change a night, delete it and make another, which costs
// nothing while it is still in the lobby.

export const PHASE_LABEL: Record<string, string> = {
  [HOP.phase.lobby]: 'Lobby',
  [HOP.phase.racing]: 'Racing',
  [HOP.phase.final]: 'Finished',
};

export function packLabel(key: string) {
  if (key === 'generated') return 'A fresh random cup';
  return key.charAt(0).toUpperCase() + key.slice(1).replace(/-/g, ' ');
}

export default function Hoppers() {
  useSetChatContext('the Hoppers page');
  const tvURL = `${window.location.origin}/${SLUG}/hoppers/tv`;
  const nav = useNavigate();
  const [games, setGames] = useState<HoppersGame[] | null>(null);
  const [packs, setPacks] = useState<HoppersPack[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState<string | null>(null);

  const load = () => {
    api.hoppersGames().then((r) => setGames(r.games)).catch((e) => setErr(e.message));
  };
  useEffect(() => {
    load();
    api.hoppersPacks().then((r) => setPacks(r.packs)).catch((e) => setErr(e.message));
  }, []);

  const remove = async (id: string) => {
    setBusy(true);
    setErr(null);
    try {
      await api.deleteHoppersGame(id);
      setConfirming(null);
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="page-head">
        <h1>Hoppers</h1>
        <p className="page-sub">A frog race on the TV, with everyone flicking their frog from their phone.</p>
        {/* One address for the screen, forever: it follows the newest game. */}
        <p className="page-sub">
          Point the TV at <code>{tvURL}</code> once and leave it. It follows the newest game.
        </p>
      </div>

      {err ? <p className="banner banner-error">{err}</p> : null}

      <NewGameForm packs={packs} busy={busy} setBusy={setBusy} setErr={setErr}
        onCreated={(g) => nav(`/hoppers/${g.id}/live`)} />

      <h2>Games</h2>
      {games === null ? <p className="page-sub">Loading…</p> : null}
      {games !== null && games.length === 0 ? <p className="page-sub">No games yet.</p> : null}
      <ul className="card-list">
        {(games ?? []).map((g) => (
          <li key={g.id} className="card">
            <div className="card-main">
              <span className="card-title">{g.title}</span>
              <span className="card-desc">
                {new Date(g.created_at).toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' })}
                {` · ${packLabel(g.pack)} · ${g.players} frog${g.players === 1 ? '' : 's'}`}
                {g.leader ? (g.phase === HOP.phase.final ? ` · winner: ${g.leader}` : ` · leading: ${g.leader}`) : ''}
              </span>
            </div>
            <div className="card-side">
              <span className={g.phase === HOP.phase.final ? 'pill pill-off' : 'pill pill-ok'}>
                {PHASE_LABEL[g.phase] ?? g.phase}
              </span>
              <Link className="card-manage" to={`/hoppers/${g.id}/live`}>
                {g.phase === HOP.phase.final ? 'Results' : g.phase === HOP.phase.lobby ? 'Run it' : 'Resume'}
              </Link>
              {/* Two taps: deleting a night takes its scores with it. */}
              {confirming === g.id ? (
                <>
                  <button className="btn btn-danger" disabled={busy} onClick={() => void remove(g.id)}>Really delete</button>
                  <button className="btn btn-danger" disabled={busy} onClick={() => setConfirming(null)}>Cancel</button>
                </>
              ) : (
                <button className="btn btn-danger" disabled={busy} onClick={() => setConfirming(g.id)}>Delete</button>
              )}
            </div>
          </li>
        ))}
      </ul>
    </>
  );
}

function NewGameForm({ packs, busy, setBusy, setErr, onCreated }: {
  packs: HoppersPack[];
  busy: boolean;
  setBusy: (b: boolean) => void;
  setErr: (e: string | null) => void;
  onCreated: (g: HoppersGame) => void;
}) {
  const [title, setTitle] = useState('');
  const [packKey, setPackKey] = useState('generated');
  const [rules, setRules] = useState<HoppersRules>({});
  const pack = packs.find((p) => p.key === packKey);
  // The pack's own rules are the starting point; a tweak overrides one.
  const value = (k: keyof HoppersRules) => rules[k] ?? pack?.rules[k] ?? 1;
  const set = (k: keyof HoppersRules) => (n: number) => setRules((r) => ({ ...r, [k]: n }));

  const create = async () => {
    setBusy(true);
    setErr(null);
    try {
      onCreated(await api.createHoppersGame({ title, pack: packKey, rules }));
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="hop-new">
      <h2>New game</h2>
      <div className="field-row">
        <label className="field">
          <span>Name on the TV</span>
          <input value={title} placeholder="Frog race" onChange={(e) => setTitle(e.target.value)} />
        </label>
        <label className="field">
          <span>Tracks</span>
          <select value={packKey} onChange={(e) => { setPackKey(e.target.value); setRules({}); }}>
            {packs.map((p) => <option key={p.key} value={p.key}>{packLabel(p.key)}</option>)}
          </select>
        </label>
      </div>
      {pack?.levels?.length ? (
        <p className="page-sub">{pack.levels.map((l) => `${l.name} (${l.length})`).join(' · ')}</p>
      ) : null}
      <div className="field-row">
        <label className="field">
          <span>Races</span>
          <NumberField min={1} max={10} value={value('rounds')} onCommit={set('rounds')} />
        </label>
        <label className="field">
          <span>Most frogs per race</span>
          <NumberField min={2} max={20} value={value('heatSize')} onCommit={set('heatSize')} />
        </label>
        <label className="field">
          <span>Fill each race with bots up to</span>
          <NumberField min={1} max={8} value={value('minFrogs')} onCommit={set('minFrogs')} />
        </label>
        <label className="field">
          <span>Seconds to flick</span>
          <NumberField min={3} max={30} value={value('flickSeconds')} onCommit={set('flickSeconds')} />
        </label>
      </div>
      <button className="btn" disabled={busy || !pack} onClick={() => void create()}>Create game</button>
    </section>
  );
}
