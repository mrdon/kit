import { useEffect, useState } from 'react';
import { api, type BrewVessel, type HistoryEntry } from '../../../api';
import { useSetChatContext } from '../../../chatContext';
import { Crumbs } from './common';

// Corrections. Every entry in the log stays; reversing one adds an entry
// that cancels it, and everything it touched is worked out again without
// it. An admin can reverse any entry at any age, as long as what came after
// still adds up.
export default function CogsHistory() {
  useSetChatContext('the brewing history page');
  const [vessels, setVessels] = useState<BrewVessel[]>([]);
  const [vessel, setVessel] = useState('');
  const [entries, setEntries] = useState<HistoryEntry[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .brewSetup()
      .then((s) => setVessels(s.vessels))
      .catch((e) => setErr((e as Error).message));
  }, []);

  const load = (v: string) =>
    api
      .brewHistory(v || undefined, 200)
      .then((r) => setEntries(r.entries))
      .catch((e) => setErr((e as Error).message));
  useEffect(() => {
    void load(vessel);
  }, [vessel]);

  const reverse = async (id: string) => {
    setErr(null);
    setOk(null);
    setBusy(true);
    try {
      const r = await api.brewUndo(id);
      setOk(r.message);
      setConfirm(null);
      void load(vessel);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="page brew-admin">
      <div className="page-head">
        <Crumbs page="History" />
        <h1>COGS history</h1>
        <p className="page-sub">
          To change an entry, reverse it and log it again. The console can log with any past date, from the brewer
          screens' date picker or the day-one load.
        </p>
      </div>
      {err && <p className="banner banner-error">{err}</p>}
      {ok && <p className="banner banner-ok">{ok}</p>}
      <div className="field brew-filter">
        Vessel
        <select value={vessel} onChange={(e) => setVessel(e.target.value)}>
          <option value="">All</option>
          {vessels.map((v) => (
            <option key={v.id} value={v.id}>
              {v.name}
            </option>
          ))}
        </select>
      </div>
      {entries && entries.length === 0 && <p className="muted">Nothing logged yet.</p>}
      <ul className="card-list">
        {entries?.map((e) => (
          <li key={e.action_id} className={e.reversed ? 'card brew-reversed' : 'card'}>
            <div className="card-main">
              <div className="card-title">{e.text}</div>
              <div className="muted brew-small">
                {new Date(e.at).toLocaleString(undefined, {
                  weekday: 'short',
                  month: 'short',
                  day: 'numeric',
                  hour: 'numeric',
                  minute: '2-digit',
                })}
                {e.by && ` · ${e.by}`}
              </div>
            </div>
            <div className="brew-row-actions">
              {e.reversed && <span className="pill pill-off">Reversed</span>}
              {!e.reversed &&
                e.can_undo &&
                (confirm === e.action_id ? (
                  <>
                    <button
                      className="btn btn-danger btn-sm"
                      type="button"
                      disabled={busy}
                      onClick={() => void reverse(e.action_id)}
                    >
                      Reverse it
                    </button>
                    <button className="btn btn-ghost btn-sm" type="button" onClick={() => setConfirm(null)}>
                      Keep
                    </button>
                  </>
                ) : (
                  <button className="btn btn-ghost btn-sm" type="button" onClick={() => setConfirm(e.action_id)}>
                    Reverse
                  </button>
                ))}
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}
