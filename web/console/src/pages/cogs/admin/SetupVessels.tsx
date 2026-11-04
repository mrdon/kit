import { useState } from 'react';
import { api, type BrewVessel, type VesselArea, type VesselInput, type VesselKind } from '../../../api';
import { KIND_LABELS, num } from './common';

const KINDS: VesselKind[] = ['fermenter', 'brite', 'serving', 'barrel'];
const AREAS: { code: VesselArea; label: string }[] = [
  { code: 'brewhouse', label: 'Brewhouse' },
  { code: 'chiller', label: 'Chiller' },
  { code: 'barrels', label: 'Barrels' },
];

function areaFor(kind: VesselKind): VesselArea {
  if (kind === 'serving') return 'chiller';
  if (kind === 'barrel') return 'barrels';
  return 'brewhouse';
}

interface Props {
  vessels: BrewVessel[];
  reload: () => void;
  onError: (m: string | null) => void;
}

interface Draft {
  name: string;
  kind: VesselKind;
  area: VesselArea;
  size: string;
  sort: string;
  retired: boolean;
}

function draftOf(v: BrewVessel): Draft {
  return {
    name: v.name,
    kind: v.kind,
    area: v.area,
    size: String(v.size_bbl),
    sort: String(v.sort),
    retired: !!v.retired_at,
  };
}

function inputOf(d: Draft): VesselInput {
  return {
    name: d.name.trim(),
    kind: d.kind,
    area: d.area,
    size_bbl: num(d.size),
    sort: Math.round(num(d.sort)),
    retired: d.retired,
  };
}

export default function SetupVessels({ vessels, reload, onError }: Props) {
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);

  const save = async (id: string | null, d: Draft) => {
    onError(null);
    try {
      await api.brewSaveVessel(id, inputOf(d));
      setEditing(null);
      reload();
      return true;
    } catch (e) {
      onError((e as Error).message);
      return false;
    }
  };

  return (
    <section className="panel">
      <h2 className="panel-title">Vessels</h2>
      {vessels.length === 0 && <p className="muted">No vessels yet. Add a run of them below.</p>}
      {vessels.length > 0 && (
        <table className="brew-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Kind</th>
              <th>Area</th>
              <th>Size (bbl)</th>
              <th>Order</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {vessels.map((v) =>
              editing === v.id && draft ? (
                <tr key={v.id}>
                  <td>
                    <input value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
                  </td>
                  <td>
                    <KindSelect value={draft.kind} onChange={(kind) => setDraft({ ...draft, kind })} />
                  </td>
                  <td>
                    <AreaSelect value={draft.area} onChange={(area) => setDraft({ ...draft, area })} />
                  </td>
                  <td>
                    <input
                      className="brew-num"
                      inputMode="decimal"
                      value={draft.size}
                      onChange={(e) => setDraft({ ...draft, size: e.target.value })}
                    />
                  </td>
                  <td>
                    <input
                      className="brew-num"
                      inputMode="numeric"
                      value={draft.sort}
                      onChange={(e) => setDraft({ ...draft, sort: e.target.value })}
                    />
                  </td>
                  <td className="brew-row-actions">
                    <label className="brew-check">
                      <input
                        type="checkbox"
                        checked={draft.retired}
                        onChange={(e) => setDraft({ ...draft, retired: e.target.checked })}
                      />
                      Retired
                    </label>
                    <button className="btn btn-sm" type="button" onClick={() => void save(v.id, draft)}>
                      Save
                    </button>
                    <button className="btn btn-ghost btn-sm" type="button" onClick={() => setEditing(null)}>
                      Cancel
                    </button>
                  </td>
                </tr>
              ) : (
                <tr key={v.id} className={v.retired_at ? 'brew-retired' : undefined}>
                  <td>{v.name}</td>
                  <td>{KIND_LABELS[v.kind]}</td>
                  <td>{AREAS.find((a) => a.code === v.area)?.label}</td>
                  <td>{v.size_bbl}</td>
                  <td>{v.sort}</td>
                  <td className="brew-row-actions">
                    {v.retired_at && <span className="pill pill-off">Retired</span>}
                    <button
                      className="btn btn-ghost btn-sm"
                      type="button"
                      onClick={() => {
                        setEditing(v.id);
                        setDraft(draftOf(v));
                      }}
                    >
                      Edit
                    </button>
                  </td>
                </tr>
              ),
            )}
          </tbody>
        </table>
      )}
      <BulkAdd reload={reload} onError={onError} />
    </section>
  );
}

function KindSelect({ value, onChange }: { value: VesselKind; onChange: (k: VesselKind) => void }) {
  return (
    <select value={value} onChange={(e) => onChange(e.target.value as VesselKind)}>
      {KINDS.map((k) => (
        <option key={k} value={k}>
          {KIND_LABELS[k]}
        </option>
      ))}
    </select>
  );
}

function AreaSelect({ value, onChange }: { value: VesselArea; onChange: (a: VesselArea) => void }) {
  return (
    <select value={value} onChange={(e) => onChange(e.target.value as VesselArea)}>
      {AREAS.map((a) => (
        <option key={a.code} value={a.code}>
          {a.label}
        </option>
      ))}
    </select>
  );
}

// BulkAdd adds "FV-1 to FV-6" in one go; a single vessel is a run of one,
// or a plain name with From left blank.
function BulkAdd({ reload, onError }: { reload: () => void; onError: (m: string | null) => void }) {
  const [d, setD] = useState({
    prefix: 'FV-',
    from: '1',
    to: '6',
    kind: 'fermenter' as VesselKind,
    area: 'brewhouse' as VesselArea,
    size: '10',
  });
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState<string | null>(null);

  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    onError(null);
    setDone(null);
    setBusy(true);
    try {
      if (d.from.trim() === '') {
        await api.brewSaveVessel(null, { name: d.prefix.trim(), kind: d.kind, area: d.area, size_bbl: num(d.size) });
        setDone(`Added ${d.prefix.trim()}.`);
      } else {
        const r = await api.brewAddVessels({
          prefix: d.prefix,
          from: Math.round(num(d.from)),
          to: Math.round(num(d.to || d.from)),
          name: '',
          kind: d.kind,
          area: d.area,
          size_bbl: num(d.size),
        });
        setDone(`Added ${r.added} ${r.added === 1 ? 'vessel' : 'vessels'}.`);
      }
      reload();
    } catch (e) {
      onError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="stack-form brew-subform" onSubmit={add}>
      <h3 className="brew-subtitle">Add vessels</h3>
      <div className="field-row">
        <label className="field">
          Name
          <input value={d.prefix} onChange={(e) => setD({ ...d, prefix: e.target.value })} />
          <span className="field-note">The prefix for a numbered run, or the whole name when From is blank.</span>
        </label>
        <label className="field">
          From
          <input inputMode="numeric" value={d.from} onChange={(e) => setD({ ...d, from: e.target.value })} />
        </label>
        <label className="field">
          To
          <input inputMode="numeric" value={d.to} onChange={(e) => setD({ ...d, to: e.target.value })} />
        </label>
      </div>
      <div className="field-row">
        <label className="field">
          Kind
          <KindSelect value={d.kind} onChange={(kind) => setD({ ...d, kind, area: areaFor(kind) })} />
        </label>
        <label className="field">
          Area
          <AreaSelect value={d.area} onChange={(area) => setD({ ...d, area })} />
        </label>
        <label className="field">
          Size (bbl)
          <input inputMode="decimal" value={d.size} onChange={(e) => setD({ ...d, size: e.target.value })} />
        </label>
      </div>
      <div className="brew-row-actions">
        <button className="btn" type="submit" disabled={busy}>
          {busy ? 'Adding…' : 'Add'}
        </button>
        {done && <span className="muted">{done}</span>}
      </div>
    </form>
  );
}
