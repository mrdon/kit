import { useMemo, useState } from 'react';
import { api, type BrewState, type BrewTile, type PlanEntry } from '../../../api';
import { beerName, bbl, useBrew } from '../context';
import { Chip, Field, SaveBar, Sheet, Stepper, WhenChips, useSave } from '../controls';
import BeerPicker, { type BeerChoice } from './BeerPicker';

// Brew day, logged once at knockout. Pre-filled from the plan: the brewer
// changes only what's different. A fermenter brewed into today that still
// has room offers "Add to today's", for the double brew into a big tank.

function plannedEntry(state: BrewState, planEntryId?: string, vesselId?: string): PlanEntry | undefined {
  const open = state.plan.filter((p) => !p.batch_id);
  if (planEntryId) return open.find((p) => p.id === planEntryId);
  const soon = open.filter((p) => !vesselId || p.vessel_id === vesselId);
  return soon.find((p) => p.brew_on <= state.today) ?? soon[0];
}

function brewedToday(state: BrewState, t: BrewTile): boolean {
  return t.brewed_on === state.today && t.stage === 'fermenting' && t.volume_bbl < t.size_bbl - 0.1;
}

export default function BrewDay({ vesselId, planEntryId }: { vesselId?: string; planEntryId?: string }) {
  const { state, close } = useBrew();
  const plan = useMemo(() => plannedEntry(state, planEntryId, vesselId), [state, planEntryId, vesselId]);
  const [beer, setBeer] = useState<BeerChoice>(plan ? { id: plan.beer_id } : {});
  const [vessel, setVessel] = useState<string | undefined>(vesselId ?? plan?.vessel_id);
  const [volume, setVolume] = useState(
    Math.min(plan?.volume_bbl ?? state.settings.brewhouse_bbl, state.settings.brewhouse_bbl),
  );
  const [date, setDate] = useState('');
  const [photo, setPhoto] = useState<{ id: string; url: string } | null>(null);
  const [uploading, setUploading] = useState(false);
  const { saving, error, setError, save } = useSave();

  const fermenters = state.vessels.filter((v) => v.kind === 'fermenter');
  // Brewing the planned beer closes the plan entry, even in another tank.
  const usingPlan = plan && beer.id === plan.beer_id;

  const pickPhoto = async (file: File | undefined) => {
    if (!file) return;
    setUploading(true);
    setError(null);
    try {
      const blob = await downscale(file);
      const r = await api.brewUploadPhoto(blob);
      setPhoto({ id: r.id, url: URL.createObjectURL(blob) });
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setUploading(false);
    }
  };

  const submit = () =>
    save({
      kind: 'brew',
      date,
      vessel_id: vessel,
      beer_id: beer.id,
      new_beer_name: beer.newName,
      volume_bbl: volume,
      photo_id: photo?.id,
      plan_entry_id: usingPlan ? plan?.id : undefined,
    });

  const sub = plan
    ? `${beerName(state, plan.beer_id)} (planned) → ${state.vessels.find((v) => v.id === plan.vessel_id)?.name ?? ''}`
    : 'Log it once, at knockout.';

  return (
    <Sheet title="Brew day" sub={sub} onClose={close}>
      <BeerPicker state={state} value={beer} onChange={setBeer} planned={plan?.beer_id} />
      <Field label="Fermenter">
        <div className="brew-chips">
          {fermenters.map((t) => {
            const addTo = !!t.batch_id && brewedToday(state, t) && (!beer.id || t.beer_id === beer.id);
            const free = !t.batch_id;
            return (
              <Chip
                key={t.id}
                on={vessel === t.id}
                disabled={!free && !addTo}
                onClick={() => setVessel(t.id)}
                sub={
                  addTo
                    ? `Add to today's ${t.beer} (${bbl(t.volume_bbl)} bbl)`
                    : free
                      ? t.dirty
                        ? 'Dirty: logs a clean too'
                        : `Empty · ${t.size_bbl} bbl`
                      : t.beer
                }
              >
                {t.name}
              </Chip>
            );
          })}
        </div>
      </Field>
      <Field label="Volume knocked out">
        <Stepper label="Volume" value={volume} step={0.5} big={1} unit="bbl" decimals={1} onChange={setVolume} />
      </Field>
      <Field label="Brew sheet photo" hint="Optional. The paper sheet stays the record; this keeps a copy with the batch.">
        <input
          type="file"
          accept="image/*"
          capture="environment"
          onChange={(e) => void pickPhoto(e.target.files?.[0])}
        />
        {uploading && <p className="brew-hint">Uploading…</p>}
        {photo && <img className="brew-photo" src={photo.url} alt="Brew sheet" />}
      </Field>
      <WhenChips today={state.today} value={date} onChange={setDate} />
      <SaveBar
        onCancel={close}
        onSave={() => void submit()}
        saving={saving || uploading}
        disabled={!vessel || (!beer.id && !beer.newName) || volume <= 0}
        error={error}
      />
    </Sheet>
  );
}

// downscale turns a phone photo into a JPEG no wider than 2000px: plenty
// to read a brew sheet, a fraction of the upload.
async function downscale(file: File): Promise<Blob> {
  const img = await createImageBitmap(file).catch(() => null);
  if (!img) return file;
  const scale = Math.min(1, 2000 / Math.max(img.width, img.height));
  const canvas = document.createElement('canvas');
  canvas.width = Math.round(img.width * scale);
  canvas.height = Math.round(img.height * scale);
  canvas.getContext('2d')?.drawImage(img, 0, 0, canvas.width, canvas.height);
  return new Promise((resolve) => canvas.toBlob((b) => resolve(b ?? file), 'image/jpeg', 0.85));
}
