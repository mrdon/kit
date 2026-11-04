import { useEffect, useState } from 'react';
import { api, type BrewState, type PlanEntry } from '../../../api';
import { useSetChatContext } from '../../../chatContext';
import { Crumbs, fmtDate, num, today, vesselName } from './common';

// The brew plan: planned brews onto real fermenters. Owned here; the
// brewer sees it on the Schedule and can only flag a date.
export default function CogsPlan() {
  useSetChatContext('the brewing plan page');
  const [st, setSt] = useState<BrewState | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [confirmCancel, setConfirmCancel] = useState<string | null>(null);

  const load = () =>
    api
      .brewState()
      .then(setSt)
      .catch((e) => setErr((e as Error).message));
  useEffect(() => {
    void load();
  }, []);

  const cancel = async (id: string) => {
    setErr(null);
    try {
      await api.brewCancelPlan(id);
      setConfirmCancel(null);
      void load();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  const beerName = (id: string) => st?.beers.find((b) => b.id === id)?.name ?? 'a beer';

  return (
    <div className="page brew-admin">
      <div className="page-head">
        <Crumbs page="Plan" />
        <h1>Brew plan</h1>
        <p className="page-sub">
          Planned brews show on the brewer's Schedule and in This week. The brewer can flag a date that won't work;
          those land in the Queue.
        </p>
      </div>
      {err && <p className="banner banner-error">{err}</p>}
      {st && (
        <>
          <section className="panel">
            <h2 className="panel-title">Coming up</h2>
            {st.plan.length === 0 && <p className="muted">Nothing planned from today on.</p>}
            <ul className="card-list">
              {st.plan.map((p) =>
                editing === p.id ? (
                  <li key={p.id} className="card">
                    <PlanForm
                      st={st}
                      entry={p}
                      onDone={() => {
                        setEditing(null);
                        void load();
                      }}
                      onCancel={() => setEditing(null)}
                      onError={setErr}
                    />
                  </li>
                ) : (
                  <li key={p.id} className="card">
                    <div className="card-main">
                      <div className="card-title">
                        {fmtDate(p.brew_on)}: {beerName(p.beer_id)} into {vesselName(st.vessels, p.vessel_id)},{' '}
                        {p.volume_bbl} bbl {p.batch_id && <span className="pill pill-ok">Brewed</span>}
                      </div>
                      {p.why && <div className="card-desc">{p.why}</div>}
                    </div>
                    {!p.batch_id && (
                      <div className="brew-row-actions">
                        <button className="btn btn-ghost btn-sm" type="button" onClick={() => setEditing(p.id)}>
                          Edit
                        </button>
                        {confirmCancel === p.id ? (
                          <button className="btn btn-danger btn-sm" type="button" onClick={() => void cancel(p.id)}>
                            Really drop it?
                          </button>
                        ) : (
                          <button
                            className="btn btn-ghost btn-sm"
                            type="button"
                            onClick={() => setConfirmCancel(p.id)}
                          >
                            Drop
                          </button>
                        )}
                      </div>
                    )}
                  </li>
                ),
              )}
            </ul>
          </section>
          <section className="panel">
            <h2 className="panel-title">Plan a brew</h2>
            <PlanForm st={st} onDone={() => void load()} onError={setErr} />
          </section>
        </>
      )}
    </div>
  );
}

function PlanForm({
  st,
  entry,
  onDone,
  onCancel,
  onError,
}: {
  st: BrewState;
  entry?: PlanEntry;
  onDone: () => void;
  onCancel?: () => void;
  onError: (m: string | null) => void;
}) {
  const fermenters = st.vessels.filter((v) => v.kind === 'fermenter');
  const [beer, setBeer] = useState(entry?.beer_id ?? '');
  const [vessel, setVessel] = useState(entry?.vessel_id ?? fermenters[0]?.id ?? '');
  const [on, setOn] = useState(entry?.brew_on ?? today());
  const [vol, setVol] = useState(String(entry?.volume_bbl ?? st.settings.brewhouse_bbl));
  const [why, setWhy] = useState(entry?.why ?? '');
  const [busy, setBusy] = useState(false);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    onError(null);
    setBusy(true);
    try {
      await api.brewSavePlan(entry?.id ?? null, {
        beer_id: beer,
        vessel_id: vessel,
        brew_on: on,
        volume_bbl: num(vol),
        why: why.trim(),
      });
      if (!entry) {
        setBeer('');
        setWhy('');
      }
      onDone();
    } catch (e) {
      onError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="stack-form" onSubmit={save}>
      <div className="field-row">
        <label className="field">
          Beer
          <select required value={beer} onChange={(e) => setBeer(e.target.value)}>
            <option value="">Pick a beer…</option>
            {st.beers
              .filter((b) => b.active || b.id === beer)
              .map((b) => (
                <option key={b.id} value={b.id}>
                  {b.name}
                </option>
              ))}
          </select>
        </label>
        <label className="field">
          Fermenter
          <select required value={vessel} onChange={(e) => setVessel(e.target.value)}>
            {fermenters.map((v) => (
              <option key={v.id} value={v.id}>
                {v.name} ({v.size_bbl} bbl)
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          Brew on
          <input type="date" required value={on} onChange={(e) => setOn(e.target.value)} />
        </label>
        <label className="field">
          Size (bbl)
          <input inputMode="decimal" value={vol} onChange={(e) => setVol(e.target.value)} />
        </label>
      </div>
      <label className="field">
        Why
        <input placeholder="Old batch runs out around Nov 22" value={why} onChange={(e) => setWhy(e.target.value)} />
        <span className="field-note">The brewer sees this when they tap the planned brew.</span>
      </label>
      <div className="brew-row-actions">
        <button className="btn" type="submit" disabled={busy || !beer || !vessel}>
          {busy ? 'Saving…' : entry ? 'Save' : 'Add to the plan'}
        </button>
        {onCancel && (
          <button className="btn btn-ghost" type="button" onClick={onCancel}>
            Cancel
          </button>
        )}
      </div>
    </form>
  );
}
