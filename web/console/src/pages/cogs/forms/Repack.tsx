import { useState } from 'react';
import { bbl, beerName, useBrew } from '../context';
import { Check, Chip, Field, QtyInput, SaveBar, Sheet, Stepper, WhenChips, qtyLine, qtyUnits, useSave, zeroQty, type Qty } from '../controls';
import BeerPicker, { type BeerChoice } from './BeerPicker';

// Repack or make a variant: packaged units in, packaged units out. A ½
// broken into sixtels, a keg canned off, a sixtel made into a new beer.
// What goes in comes off the oldest batch first; the difference in bbl is
// written off.

export default function Repack() {
  const { state, close } = useBrew();
  const onHand = Array.from(new Set(state.packaged.filter((r) => r.units > 0).map((r) => r.beer_id)));
  const [takeBeer, setTakeBeer] = useState<string | undefined>(onHand[0]);
  const formatsFor = (beer?: string) =>
    state.formats.filter((f) => state.packaged.some((r) => r.beer_id === beer && r.format_id === f.id && r.units > 0));
  const [takeFormat, setTakeFormat] = useState<string | undefined>(formatsFor(onHand[0])[0]?.id);
  const [takeCount, setTakeCount] = useState(1);
  const [variant, setVariant] = useState(false);
  const [made, setMade] = useState<BeerChoice>({});
  const [qty, setQty] = useState<Record<string, Qty>>({});
  const [added, setAdded] = useState('');
  const [keepRest, setKeepRest] = useState(false);
  const [date, setDate] = useState('');
  const { saving, error, save } = useSave();

  const tf = state.formats.find((f) => f.id === takeFormat);
  const pf = state.settings.partial_fullness;
  const inBBL = tf ? takeCount * tf.bbl_per_unit : 0;
  const outBBL = state.formats.reduce((s, f) => s + qtyUnits(qty[f.id] ?? zeroQty, f, pf) * f.bbl_per_unit, 0);
  const takeFormats = takeBeer && formatsFor(takeBeer).length > 0 ? formatsFor(takeBeer) : state.formats;

  const submit = () =>
    save({
      kind: 'repack',
      date,
      take: { beer_id: takeBeer, format_id: takeFormat ?? '', units: takeCount },
      beer_id: variant ? made.id : undefined,
      new_beer_name: variant ? made.newName : undefined,
      added: variant ? added : undefined,
      keep_rest: keepRest,
      lines: state.formats.map((f) => qtyLine(qty[f.id] ?? zeroQty, f)),
    });

  return (
    <Sheet title="Repack or make a variant" onClose={close}>
      <Field label="Took">
        <div className="brew-chips">
          {(onHand.length > 0 ? onHand : state.beers.filter((b) => b.active).map((b) => b.id)).map((id) => (
            <Chip
              key={id}
              on={takeBeer === id}
              onClick={() => {
                setTakeBeer(id);
                setTakeFormat(formatsFor(id)[0]?.id ?? state.formats[0]?.id);
              }}
            >
              {beerName(state, id)}
            </Chip>
          ))}
        </div>
        <div className="brew-chips" style={{ marginTop: '0.5rem' }}>
          {takeFormats.map((f) => (
            <Chip key={f.id} on={takeFormat === f.id} onClick={() => setTakeFormat(f.id)}>
              {f.name}
            </Chip>
          ))}
        </div>
        <div style={{ marginTop: '0.5rem' }}>
          <Stepper label="How many taken" value={takeCount} min={0} onChange={setTakeCount} unit={tf?.name} />
        </div>
      </Field>
      <Field label="Made">
        <div className="brew-chips">
          <Chip on={!variant} onClick={() => setVariant(false)}>
            Same beer{takeBeer ? `: ${beerName(state, takeBeer)}` : ''}
          </Chip>
          <Chip on={variant} onClick={() => setVariant(true)}>
            A variant
          </Chip>
        </div>
      </Field>
      {variant && (
        <>
          <BeerPicker state={state} value={made} onChange={setMade} label="Variant" exclude={takeBeer} />
          <Field label="What did you add?" hint="Optional. The cost of it is filled in later.">
            <input className="brew-input" value={added} onChange={(e) => setAdded(e.target.value)} />
          </Field>
        </>
      )}
      <div className="brew-section brew-rows">
        {state.formats.map((f) => (
          <div key={f.id} className="brew-row">
            <span className="brew-row-name">{f.name}</span>
            <QtyInput format={f} value={qty[f.id] ?? zeroQty} onChange={(q) => setQty({ ...qty, [f.id]: q })} />
          </div>
        ))}
      </div>
      {tf && inBBL - outBBL > 0.005 && (
        <div className="brew-section">
          <Check checked={keepRest} onChange={setKeepRest}>
            Put the rest back in the {tf.name}
          </Check>
          <p className="brew-hint">
            {keepRest
              ? `It goes back on the shelf as ${((inBBL - outBBL) / tf.bbl_per_unit).toFixed(2)} of a ${tf.name}, same batch and date.`
              : 'Leave unticked if the rest was poured out or used up.'}
          </p>
        </div>
      )}
      <p className="brew-summary">
        {bbl(inBBL)} bbl in → {bbl(outBBL)} bbl out ·{' '}
        {keepRest ? `${bbl(Math.max(0, inBBL - outBBL))} bbl back on the shelf` : `${bbl(Math.max(0, inBBL - outBBL))} bbl written off`}
      </p>
      {outBBL > inBBL + 0.01 && <p className="brew-warn">More came out than went in. Check the counts.</p>}
      <WhenChips today={state.today} value={date} onChange={setDate} />
      <SaveBar
        onCancel={close}
        onSave={() => void submit()}
        saving={saving}
        error={error}
        disabled={!takeBeer || !takeFormat || takeCount <= 0 || outBBL <= 0 || (variant && !made.id && !made.newName)}
      />
    </Sheet>
  );
}
