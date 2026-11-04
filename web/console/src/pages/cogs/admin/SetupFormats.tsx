import { useState } from 'react';
import { api, type BrewFormat, type FormatInput, type FormatKind } from '../../../api';
import { num } from './common';

// Generic US package formats, offered as one-click starts. A brewery adds
// its own beyond these; nothing here is specific to any one of them.
const PRESETS: FormatInput[] = [
  { name: '½ bbl keg', kind: 'keg', bbl_per_unit: 0.5, units_per_case: 0, returnable: true },
  { name: '¼ bbl keg', kind: 'keg', bbl_per_unit: 0.25, units_per_case: 0, returnable: true },
  { name: '⅙ bbl keg', kind: 'keg', bbl_per_unit: 1 / 6, units_per_case: 0, returnable: true },
  { name: '16oz can', kind: 'can', bbl_per_unit: 16 / 128 / 31, units_per_case: 24, returnable: false },
  { name: '12oz can', kind: 'can', bbl_per_unit: 12 / 128 / 31, units_per_case: 24, returnable: false },
  { name: '750ml bottle', kind: 'bottle', bbl_per_unit: 0.75 / 117.348, units_per_case: 12, returnable: false },
];

const KIND_LABELS: Record<FormatKind, string> = { keg: 'Keg', can: 'Can', bottle: 'Bottle' };

interface Draft {
  name: string;
  kind: FormatKind;
  bbl: string;
  perCase: string;
  returnable: boolean;
  sort: string;
  retired: boolean;
}

const blank: Draft = { name: '', kind: 'keg', bbl: '', perCase: '0', returnable: true, sort: '0', retired: false };

function draftOf(f: BrewFormat): Draft {
  return {
    name: f.name,
    kind: f.kind,
    bbl: String(f.bbl_per_unit),
    perCase: String(f.units_per_case),
    returnable: f.returnable,
    sort: String(f.sort),
    retired: !!f.retired_at,
  };
}

function inputOf(d: Draft): FormatInput {
  return {
    name: d.name.trim(),
    kind: d.kind,
    bbl_per_unit: num(d.bbl),
    units_per_case: d.kind === 'keg' ? 0 : Math.round(num(d.perCase)),
    returnable: d.returnable,
    sort: Math.round(num(d.sort)),
    retired: d.retired,
  };
}

// gallons shows a unit's size the way people read it.
function gallons(bbl: number): string {
  const g = bbl * 31;
  return g >= 1 ? `${g.toFixed(1)} gal` : `${(g * 128).toFixed(1)} oz`;
}

interface Props {
  formats: BrewFormat[];
  reload: () => void;
  onError: (m: string | null) => void;
}

export default function SetupFormats({ formats, reload, onError }: Props) {
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState<Draft>(blank);
  const [adding, setAdding] = useState<Draft>(blank);

  const save = async (id: string | null, body: FormatInput) => {
    onError(null);
    try {
      await api.brewSaveFormat(id, body);
      setEditing(null);
      if (!id) setAdding(blank);
      reload();
    } catch (e) {
      onError((e as Error).message);
    }
  };

  const have = new Set(formats.map((f) => f.name.toLowerCase()));
  const presets = PRESETS.filter((p) => !have.has(p.name.toLowerCase()));

  return (
    <section className="panel">
      <h2 className="panel-title">Package formats</h2>
      {formats.length > 0 && (
        <table className="brew-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Kind</th>
              <th>bbl per unit</th>
              <th>Per case</th>
              <th>Returnable</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {formats.map((f) =>
              editing === f.id ? (
                <tr key={f.id}>
                  <FormatFields d={draft} setD={setDraft} />
                  <td className="brew-row-actions">
                    <label className="brew-check">
                      <input
                        type="checkbox"
                        checked={draft.retired}
                        onChange={(e) => setDraft({ ...draft, retired: e.target.checked })}
                      />
                      Retired
                    </label>
                    <button className="btn btn-sm" type="button" onClick={() => void save(f.id, inputOf(draft))}>
                      Save
                    </button>
                    <button className="btn btn-ghost btn-sm" type="button" onClick={() => setEditing(null)}>
                      Cancel
                    </button>
                  </td>
                </tr>
              ) : (
                <tr key={f.id} className={f.retired_at ? 'brew-retired' : undefined}>
                  <td>{f.name}</td>
                  <td>{KIND_LABELS[f.kind]}</td>
                  <td>
                    {+f.bbl_per_unit.toFixed(5)} <span className="muted">({gallons(f.bbl_per_unit)})</span>
                  </td>
                  <td>{f.kind === 'keg' ? '–' : f.units_per_case}</td>
                  <td>{f.returnable ? 'Yes' : 'No'}</td>
                  <td className="brew-row-actions">
                    {f.retired_at && <span className="pill pill-off">Retired</span>}
                    <button
                      className="btn btn-ghost btn-sm"
                      type="button"
                      onClick={() => {
                        setEditing(f.id);
                        setDraft(draftOf(f));
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
      {presets.length > 0 && (
        <div className="brew-presets">
          <span className="muted">Quick add:</span>
          {presets.map((p) => (
            <button key={p.name} className="btn btn-ghost btn-sm" type="button" onClick={() => void save(null, p)}>
              + {p.name}
            </button>
          ))}
        </div>
      )}
      <form
        className="brew-subform"
        onSubmit={(e) => {
          e.preventDefault();
          void save(null, inputOf(adding));
        }}
      >
        <h3 className="brew-subtitle">Add a format</h3>
        <table className="brew-table">
          <tbody>
            <tr>
              <FormatFields d={adding} setD={setAdding} />
              <td>
                <button className="btn btn-sm" type="submit">
                  Add
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </form>
    </section>
  );
}

function FormatFields({ d, setD }: { d: Draft; setD: (d: Draft) => void }) {
  return (
    <>
      <td>
        <input placeholder="Name" value={d.name} onChange={(e) => setD({ ...d, name: e.target.value })} />
      </td>
      <td>
        <select value={d.kind} onChange={(e) => setD({ ...d, kind: e.target.value as FormatKind })}>
          <option value="keg">Keg</option>
          <option value="can">Can</option>
          <option value="bottle">Bottle</option>
        </select>
      </td>
      <td>
        <input
          className="brew-num"
          inputMode="decimal"
          placeholder="bbl"
          value={d.bbl}
          onChange={(e) => setD({ ...d, bbl: e.target.value })}
        />
      </td>
      <td>
        <input
          className="brew-num"
          inputMode="numeric"
          disabled={d.kind === 'keg'}
          value={d.kind === 'keg' ? '' : d.perCase}
          onChange={(e) => setD({ ...d, perCase: e.target.value })}
        />
      </td>
      <td>
        <input type="checkbox" checked={d.returnable} onChange={(e) => setD({ ...d, returnable: e.target.checked })} />
      </td>
    </>
  );
}
