import { useEffect, useState } from 'react';
import { api, type BrewState, type BrewTile, type BrewFormat } from '../../../api';
import { useSetChatContext } from '../../../chatContext';
import { Crumbs, KIND_LABELS, STAGES, defaultStage, isEmptyVessel, num, today } from './common';

// The day-one load: what is already in each vessel, and the packaged beer
// already in the chiller, so the log starts from the truth. Each load is an
// entry like any other and can be reversed from History.
export default function CogsLoad() {
  useSetChatContext('the brewing day-one load page');
  const [st, setSt] = useState<BrewState | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);

  const load = () =>
    api
      .brewState()
      .then(setSt)
      .catch((e) => setErr((e as Error).message));
  useEffect(() => {
    void load();
  }, []);

  const done = (msg: string) => {
    setErr(null);
    setOk(msg);
    setOpen(null);
    void load();
  };

  const empty = st?.vessels.filter(isEmptyVessel) ?? [];
  const full = st?.vessels.filter((v) => !isEmptyVessel(v)) ?? [];

  return (
    <div className="page brew-admin">
      <div className="page-head">
        <Crumbs page="Day-one load" />
        <h1>Day-one load</h1>
        <p className="page-sub">
          Enter what is in each vessel and on the shelf today. After this, the brewer's log keeps it current.
        </p>
      </div>
      {err && <p className="banner banner-error">{err}</p>}
      {ok && <p className="banner banner-ok">{ok}</p>}
      {st && (
        <>
          <section className="panel">
            <h2 className="panel-title">Vessels</h2>
            {st.vessels.length === 0 && <p className="muted">Add vessels on the Setup page first.</p>}
            <ul className="brew-load-list">
              {empty.map((v) => (
                <li key={v.id}>
                  <div className="brew-beer-row">
                    <span>
                      <strong>{v.name}</strong>{' '}
                      <span className="muted">
                        {KIND_LABELS[v.kind]}, {v.size_bbl} bbl, empty
                      </span>
                    </span>
                    {open !== v.id && (
                      <button className="btn btn-ghost btn-sm" type="button" onClick={() => setOpen(v.id)}>
                        Load
                      </button>
                    )}
                  </div>
                  {open === v.id && (
                    <VesselLoad vessel={v} st={st} onDone={done} onError={setErr} onCancel={() => setOpen(null)} />
                  )}
                </li>
              ))}
            </ul>
            {full.length > 0 && (
              <>
                <h3 className="brew-subtitle">Already holding beer</h3>
                <ul className="brew-load-list">
                  {full.map((v) => (
                    <li key={v.id} className="muted">
                      {v.name}: {v.beer} {v.batch_code}, {v.volume_bbl} bbl, {v.stage_label.toLowerCase()}
                    </li>
                  ))}
                </ul>
              </>
            )}
          </section>
          <PackagedLoad st={st} onDone={done} onError={setErr} />
        </>
      )}
    </div>
  );
}

interface FormProps {
  st: BrewState;
  onDone: (msg: string) => void;
  onError: (m: string | null) => void;
}

// BeerPick is a beer from the list, or a new name typed in.
function BeerPick({ st, value, onChange }: { st: BrewState; value: string; onChange: (v: string) => void }) {
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">Pick a beer…</option>
      {st.beers.map((b) => (
        <option key={b.id} value={b.id}>
          {b.name}
        </option>
      ))}
      <option value="__new">New beer…</option>
    </select>
  );
}

function VesselLoad({ vessel, onCancel, ...p }: FormProps & { vessel: BrewTile; onCancel: () => void }) {
  const [beer, setBeer] = useState('');
  const [newName, setNewName] = useState('');
  const [brewedOn, setBrewedOn] = useState(today());
  const [vol, setVol] = useState(String(vessel.size_bbl));
  const [stage, setStage] = useState(defaultStage(vessel.kind));
  const [onTap, setOnTap] = useState(false);
  const [source, setSource] = useState('brewed');
  const [busy, setBusy] = useState(false);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    p.onError(null);
    setBusy(true);
    try {
      const r = await api.brewRecord({
        kind: 'load_vessel',
        to_vessel_id: vessel.id,
        beer_id: beer && beer !== '__new' ? beer : undefined,
        new_beer_name: beer === '__new' ? newName.trim() : undefined,
        brewed_on: brewedOn,
        volume_bbl: num(vol),
        stage,
        on_tap: vessel.kind === 'serving' && onTap,
        source,
      });
      p.onDone(r.message);
    } catch (e) {
      p.onError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="stack-form brew-subform" onSubmit={save}>
      <div className="field-row">
        <label className="field">
          Beer
          <BeerPick st={p.st} value={beer} onChange={setBeer} />
        </label>
        {beer === '__new' && (
          <label className="field">
            New beer's name
            <input required value={newName} onChange={(e) => setNewName(e.target.value)} />
          </label>
        )}
        <label className="field">
          Brewed on
          <input type="date" value={brewedOn} onChange={(e) => setBrewedOn(e.target.value)} />
        </label>
        <label className="field">
          Volume (bbl)
          <input inputMode="decimal" value={vol} onChange={(e) => setVol(e.target.value)} />
        </label>
      </div>
      <div className="field-row">
        <label className="field">
          Stage
          <select value={stage} onChange={(e) => setStage(e.target.value)}>
            {STAGES.map((s) => (
              <option key={s.code} value={s.code}>
                {s.label}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          Source
          <select value={source} onChange={(e) => setSource(e.target.value)}>
            <option value="brewed">Brewed here</option>
            <option value="acquired">Bought in</option>
          </select>
        </label>
      </div>
      {vessel.kind === 'serving' && (
        <label className="brew-check">
          <input type="checkbox" checked={onTap} onChange={(e) => setOnTap(e.target.checked)} />
          On tap now
        </label>
      )}
      <div className="brew-row-actions">
        <button className="btn" type="submit" disabled={busy || !beer}>
          {busy ? 'Saving…' : `Load ${vessel.name}`}
        </button>
        <button className="btn btn-ghost" type="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function PackagedLoad(p: FormProps) {
  const formats = p.st.formats;
  const [beer, setBeer] = useState('');
  const [newName, setNewName] = useState('');
  const [formatId, setFormatId] = useState(formats[0]?.id ?? '');
  const [count, setCount] = useState('');
  const [loose, setLoose] = useState('');
  const [on, setOn] = useState(today());
  const [busy, setBusy] = useState(false);
  const format: BrewFormat | undefined = formats.find((f) => f.id === formatId);
  const isKeg = format?.kind === 'keg';

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    p.onError(null);
    setBusy(true);
    try {
      // A typed name goes as new_beer_name: the server makes the beer with
      // its defaults and queues it for setup, like any other new name.
      const isNew = beer === '__new';
      const beerId = isNew ? undefined : beer;
      const line = isKeg
        ? { beer_id: beerId, format_id: formatId, full: num(count) }
        : { beer_id: beerId, format_id: formatId, cases: num(count), loose: num(loose) };
      const r = await api.brewRecord({
        kind: 'load_packaged',
        beer_id: beerId,
        new_beer_name: isNew ? newName.trim() : undefined,
        brewed_on: on,
        lines: [line],
      });
      setCount('');
      setLoose('');
      p.onDone(r.message);
    } catch (e) {
      p.onError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="panel">
      <h2 className="panel-title">Packaged beer</h2>
      {formats.length === 0 ? (
        <p className="muted">Add package formats on the Setup page first.</p>
      ) : (
        <form className="stack-form" onSubmit={save}>
          <div className="field-row">
            <label className="field">
              Beer
              <BeerPick st={p.st} value={beer} onChange={setBeer} />
            </label>
            {beer === '__new' && (
              <label className="field">
                New beer's name
                <input required value={newName} onChange={(e) => setNewName(e.target.value)} />
              </label>
            )}
            <label className="field">
              Format
              <select value={formatId} onChange={(e) => setFormatId(e.target.value)}>
                {formats.map((f) => (
                  <option key={f.id} value={f.id}>
                    {f.name}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <div className="field-row">
            <label className="field">
              {isKeg ? 'Kegs' : 'Cases'}
              <input inputMode="decimal" value={count} onChange={(e) => setCount(e.target.value)} />
            </label>
            {!isKeg && (
              <label className="field">
                Loose units
                <input inputMode="numeric" value={loose} onChange={(e) => setLoose(e.target.value)} />
              </label>
            )}
            <label className="field">
              Packaged on
              <input type="date" value={on} onChange={(e) => setOn(e.target.value)} />
              <span className="field-note">Roughly is fine. It sets the age for shelf life.</span>
            </label>
          </div>
          <div>
            <button className="btn" type="submit" disabled={busy || !beer || !formatId}>
              {busy ? 'Saving…' : 'Add to the shelf'}
            </button>
          </div>
        </form>
      )}
      {p.st.packaged.length > 0 && (
        <>
          <h3 className="brew-subtitle">On the shelf now</h3>
          <ul className="brew-load-list">
            {p.st.packaged.map((r) => (
              <li key={`${r.beer_id}-${r.format_id}`} className="muted">
                {r.beer}, {r.format}: {r.units}
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}
