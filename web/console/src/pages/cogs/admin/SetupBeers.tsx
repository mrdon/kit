import { useState } from 'react';
import { api, type BrewBeer } from '../../../api';
import { num } from './common';

// Beers: one summary row each, a full form for the one being edited.
// Saving clears "needs setup": a beer the brewer typed on the iPad shows
// highlighted until someone has looked at it here.

interface Props {
  beers: BrewBeer[];
  reload: () => void;
  onError: (m: string | null) => void;
}

const NEW_BEER: BrewBeer = {
  id: '',
  name: '',
  code: '',
  aliases: [],
  family: '',
  style: '',
  active: true,
  staple: false,
  needs_setup: false,
  ferment_days: 14,
  dry_hop: false,
  dry_hop_days: 4,
  condition_days: 0,
  brite_days: 3,
  uses_brite: true,
  keg_shelf_weeks: 12,
  pkg_shelf_weeks: 16,
};

function timeline(b: BrewBeer): string {
  const parts = [`${b.ferment_days}d ferment`];
  if (b.dry_hop) parts.push(`${b.dry_hop_days}d dry hop`);
  if (b.condition_days) parts.push(`${b.condition_days}d conditioning`);
  if (b.uses_brite) parts.push(`${b.brite_days}d brite`);
  return parts.join(' · ');
}

export default function SetupBeers({ beers, reload, onError }: Props) {
  const [editing, setEditing] = useState<string | null>(null);
  const [pasted, setPasted] = useState('');
  const [note, setNote] = useState<string | null>(null);

  const addPasted = async () => {
    onError(null);
    setNote(null);
    const names = pasted.split('\n').map((s) => s.trim()).filter(Boolean);
    if (names.length === 0) return;
    try {
      const r = await api.brewAddBeers(names);
      setPasted('');
      setNote(r.added === names.length ? `Added ${r.added}.` : `Added ${r.added}; the rest were already listed.`);
      reload();
    } catch (e) {
      onError((e as Error).message);
    }
  };

  return (
    <section className="panel">
      <h2 className="panel-title">Beers</h2>
      {beers.length > 0 && (
        <ul className="brew-beer-list">
          {beers.map((b) => (
            <li key={b.id} className={b.needs_setup ? 'brew-beer brew-needs-setup' : 'brew-beer'}>
              {editing === b.id ? (
                <BeerForm
                  beer={b}
                  onDone={() => {
                    setEditing(null);
                    reload();
                  }}
                  onCancel={() => setEditing(null)}
                  onError={onError}
                />
              ) : (
                <div className="brew-beer-row">
                  <div>
                    <strong>{b.name}</strong> <span className="muted">{b.code}</span>
                    {b.needs_setup && <span className="pill pill-error">Needs setup</span>}
                    {!b.active && <span className="pill pill-off">Inactive</span>}
                    {b.staple && <span className="pill pill-ok">Staple</span>}
                    <div className="muted brew-small">
                      {b.style && `${b.style} · `}
                      {timeline(b)}
                      {b.family && ` · counts as ${b.family}`}
                    </div>
                  </div>
                  <button className="btn btn-ghost btn-sm" type="button" onClick={() => setEditing(b.id)}>
                    Edit
                  </button>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      {editing === 'new' ? (
        <div className="brew-beer">
          <BeerForm
            beer={NEW_BEER}
            onDone={() => {
              setEditing(null);
              reload();
            }}
            onCancel={() => setEditing(null)}
            onError={onError}
          />
        </div>
      ) : (
        <div className="brew-row-actions">
          <button className="btn btn-sm" type="button" onClick={() => setEditing('new')}>
            Add a beer
          </button>
        </div>
      )}
      <div className="stack-form brew-subform">
        <h3 className="brew-subtitle">Add several</h3>
        <label className="field">
          Paste names, one per line
          <textarea rows={5} value={pasted} onChange={(e) => setPasted(e.target.value)} />
          <span className="field-note">Each gets the default durations. Names already listed are skipped.</span>
        </label>
        <div className="brew-row-actions">
          <button className="btn btn-sm" type="button" onClick={() => void addPasted()}>
            Add these
          </button>
          {note && <span className="muted">{note}</span>}
        </div>
      </div>
    </section>
  );
}

type NumKey =
  | 'ferment_days'
  | 'dry_hop_days'
  | 'condition_days'
  | 'brite_days'
  | 'keg_shelf_weeks'
  | 'pkg_shelf_weeks';

function BeerForm({
  beer,
  onDone,
  onCancel,
  onError,
}: {
  beer: BrewBeer;
  onDone: () => void;
  onCancel: () => void;
  onError: (m: string | null) => void;
}) {
  const [b, setB] = useState<BrewBeer>(beer);
  const [nums, setNums] = useState<Record<NumKey, string>>({
    ferment_days: String(beer.ferment_days),
    dry_hop_days: String(beer.dry_hop_days),
    condition_days: String(beer.condition_days),
    brite_days: String(beer.brite_days),
    keg_shelf_weeks: String(beer.keg_shelf_weeks),
    pkg_shelf_weeks: String(beer.pkg_shelf_weeks),
  });
  const [aliases, setAliases] = useState(beer.aliases.join(', '));
  const [busy, setBusy] = useState(false);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    onError(null);
    setBusy(true);
    const body: Partial<BrewBeer> = {
      ...b,
      aliases: aliases.split(',').map((s) => s.trim()).filter(Boolean),
      ferment_days: Math.round(num(nums.ferment_days)),
      dry_hop_days: Math.round(num(nums.dry_hop_days)),
      condition_days: Math.round(num(nums.condition_days)),
      brite_days: Math.round(num(nums.brite_days)),
      keg_shelf_weeks: num(nums.keg_shelf_weeks),
      pkg_shelf_weeks: num(nums.pkg_shelf_weeks),
    };
    try {
      await api.brewSaveBeer(beer.id || null, body);
      onDone();
    } catch (e) {
      onError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const text = (key: 'name' | 'code' | 'style' | 'family', label: string, note?: string) => (
    <label className="field">
      {label}
      <input value={b[key]} onChange={(e) => setB({ ...b, [key]: e.target.value })} />
      {note && <span className="field-note">{note}</span>}
    </label>
  );
  const number = (key: NumKey, label: string) => (
    <label className="field">
      {label}
      <input inputMode="decimal" value={nums[key]} onChange={(e) => setNums({ ...nums, [key]: e.target.value })} />
    </label>
  );
  const check = (key: 'active' | 'staple' | 'dry_hop' | 'uses_brite', label: string) => (
    <label className="brew-check">
      <input type="checkbox" checked={b[key]} onChange={(e) => setB({ ...b, [key]: e.target.checked })} />
      {label}
    </label>
  );

  return (
    <form className="stack-form" onSubmit={save}>
      <div className="field-row">
        {text('name', 'Name')}
        {text('code', 'Batch code', 'Blank makes one from the initials: LL makes LL-07.')}
        {text('style', 'Style', 'A style with "lager" in it reads as lagering.')}
      </div>
      <div className="field-row">
        <label className="field">
          Other spellings
          <input value={aliases} onChange={(e) => setAliases(e.target.value)} />
          <span className="field-note">Comma separated, as Square or the wholesale sheet write it.</span>
        </label>
        {text('family', 'Counts toward', 'For a variant: the beer whose demand it rolls into.')}
      </div>
      <div className="field-row">
        {number('ferment_days', 'Ferment (days)')}
        {number('dry_hop_days', 'Dry hop (days)')}
        {number('condition_days', 'Condition or lager (days)')}
        {number('brite_days', 'Brite (days)')}
      </div>
      <div className="field-row">
        {number('keg_shelf_weeks', 'Keg shelf life (weeks)')}
        {number('pkg_shelf_weeks', 'Can and bottle shelf life (weeks)')}
      </div>
      <div className="brew-checks">
        {check('dry_hop', 'Dry hopped')}
        {check('uses_brite', 'Goes through the brite tank')}
        {check('active', 'Active')}
        {check('staple', 'Staple')}
      </div>
      <div className="brew-row-actions">
        <button className="btn" type="submit" disabled={busy}>
          {busy ? 'Saving…' : 'Save'}
        </button>
        <button className="btn btn-ghost" type="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
