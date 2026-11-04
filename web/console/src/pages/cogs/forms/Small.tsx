import { useState } from 'react';
import { api } from '../../../api';
import { beerName, bbl, tileById, useBrew } from '../context';
import { Check, Field, SaveBar, Sheet, Stepper, WhenChips, useSave } from '../controls';
import BeerPicker, { type BeerChoice } from './BeerPicker';

// The short forms: a spill, a level reading, the one-tap stage changes, a
// variant made in the tank, and a planned brew's "this date won't work".

const GALLONS_PER_BBL = 31;

export function Spill({ vesselId }: { vesselId: string }) {
  const { state, close } = useBrew();
  const tile = tileById(state, vesselId);
  const [gallons, setGallons] = useState(0);
  const [all, setAll] = useState(false);
  const [date, setDate] = useState('');
  const { saving, error, save } = useSave();
  if (!tile) return null;
  return (
    <Sheet title={`Spilled or dumped: ${tile.name}`} sub={`${tile.beer} · ${bbl(tile.volume_bbl)} bbl`} onClose={close}>
      {!all && (
        <Field label="How much" hint={`≈ ${bbl(gallons / GALLONS_PER_BBL)} bbl`}>
          <Stepper label="Gallons" value={gallons} big={5} unit="gallons" onChange={setGallons} />
        </Field>
      )}
      <div className="brew-section">
        <Check checked={all} onChange={setAll}>
          Dumped the whole thing
        </Check>
      </div>
      <WhenChips today={state.today} value={date} onChange={setDate} />
      <SaveBar
        onCancel={close}
        saving={saving}
        error={error}
        disabled={!all && gallons <= 0}
        onSave={() => void save({ kind: 'loss', date, vessel_id: vesselId, gallons, all })}
      />
    </Sheet>
  );
}

export function Level({ vesselId }: { vesselId: string }) {
  const { state, close } = useBrew();
  const tile = tileById(state, vesselId);
  const [reading, setReading] = useState(tile?.volume_bbl ?? 0);
  const [date, setDate] = useState('');
  const { saving, error, save } = useSave();
  if (!tile) return null;
  const diff = tile.volume_bbl - reading;
  return (
    <Sheet title={`Read the level: ${tile.name}`} sub={`Expected ${bbl(tile.volume_bbl)} bbl of ${tile.beer}`} onClose={close}>
      <Field
        label="Reading"
        hint={
          Math.abs(diff) < 0.05
            ? 'Matches what was expected.'
            : diff > 0
              ? `${bbl(diff)} bbl less than expected; it's written off.`
              : `${bbl(-diff)} bbl more than expected.`
        }
      >
        <Stepper label="Reading" value={reading} step={0.1} big={1} decimals={1} unit="bbl" max={tile.size_bbl} onChange={setReading} />
      </Field>
      <WhenChips today={state.today} value={date} onChange={setDate} />
      <SaveBar
        onCancel={close}
        saving={saving}
        error={error}
        onSave={() => void save({ kind: 'level', date, vessel_id: vesselId, reading_bbl: reading })}
      />
    </Sheet>
  );
}

const CONFIRM: Record<string, { title: string; body: (name: string, beer: string) => string; label: string }> = {
  dry_hop: { title: 'Dry hopped', body: (n, b) => `${b} in ${n} was dry hopped.`, label: 'Dry hopped' },
  clean: { title: 'Cleaned it', body: (n) => `${n} is clean and ready to fill.`, label: 'Cleaned it' },
  on_tap: { title: 'On tap now', body: (n, b) => `${b} is pouring from ${n}.`, label: 'On tap now' },
  kick: {
    title: 'It kicked',
    body: (n, b) => `${n} is empty and marked dirty. If another serving tank has ${b}, it goes on tap.`,
    label: 'It kicked',
  },
};

export function Confirm({ action, vesselId }: { action: 'dry_hop' | 'clean' | 'on_tap' | 'kick'; vesselId: string }) {
  const { state, close } = useBrew();
  const tile = tileById(state, vesselId);
  const [date, setDate] = useState('');
  const { saving, error, save } = useSave();
  if (!tile) return null;
  const c = CONFIRM[action];
  return (
    <Sheet title={`${c.title}: ${tile.name}`} onClose={close}>
      <p>{c.body(tile.name, tile.beer)}</p>
      <WhenChips today={state.today} value={date} onChange={setDate} />
      <SaveBar
        onCancel={close}
        saving={saving}
        error={error}
        label={c.label}
        onSave={() => void save({ kind: action, date, vessel_id: vesselId })}
      />
    </Sheet>
  );
}

export function Variant({ vesselId }: { vesselId: string }) {
  const { state, close } = useBrew();
  const tile = tileById(state, vesselId);
  const [beer, setBeer] = useState<BeerChoice>({});
  const [added, setAdded] = useState('');
  const [date, setDate] = useState('');
  const { saving, error, save } = useSave();
  if (!tile) return null;
  return (
    <Sheet title={`Became a variant: ${tile.name}`} sub={`From ${tile.beer} ${tile.batch_code}`} onClose={close}>
      <BeerPicker state={state} value={beer} onChange={setBeer} label="It's now" exclude={tile.beer_id ?? undefined} />
      <Field label="What did you add?" hint="Optional. Cashew, grapefruit… the cost is filled in later.">
        <input className="brew-input" value={added} onChange={(e) => setAdded(e.target.value)} />
      </Field>
      <WhenChips today={state.today} value={date} onChange={setDate} />
      <SaveBar
        onCancel={close}
        saving={saving}
        error={error}
        disabled={!beer.id && !beer.newName}
        onSave={() =>
          void save({ kind: 'variant', date, vessel_id: vesselId, beer_id: beer.id, new_beer_name: beer.newName, added })
        }
      />
    </Sheet>
  );
}

// PlanInfo is a planned brew tapped on the Schedule: why it's planned, and
// a note to the console if the date won't work. The brewer can't move it.
export function PlanInfo({ planEntryId }: { planEntryId: string }) {
  const { state, close, notify } = useBrew();
  const p = state.plan.find((e) => e.id === planEntryId);
  const [flagging, setFlagging] = useState(false);
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  if (!p) return null;
  const vessel = state.vessels.find((v) => v.id === p.vessel_id)?.name ?? '';
  const on = new Date(`${p.brew_on}T12:00:00`).toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' });
  const send = async () => {
    setBusy(true);
    setErr(null);
    try {
      await api.brewFlagPlan(p.id, note);
      close();
      notify('Sent. The plan stays as it is until it’s changed on the console.');
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet title={`Brew ${beerName(state, p.beer_id)}`} sub={`${on} · ${vessel} · ${bbl(p.volume_bbl)} bbl`} onClose={close}>
      <p>{p.why || 'No reason noted.'}</p>
      {flagging ? (
        <Field label="What's wrong with it?">
          <textarea className="brew-input" rows={3} value={note} onChange={(e) => setNote(e.target.value)} />
        </Field>
      ) : null}
      {err && <p className="brew-error">{err}</p>}
      <div className="brew-sheet-foot">
        {flagging ? (
          <button type="button" className="brew-btn brew-btn-primary brew-btn-big" disabled={busy} onClick={() => void send()}>
            {busy ? 'Sending…' : 'Send it'}
          </button>
        ) : (
          <button type="button" className="brew-btn brew-btn-big" onClick={() => setFlagging(true)}>
            This date won't work
          </button>
        )}
      </div>
    </Sheet>
  );
}
