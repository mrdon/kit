import { useState } from 'react';
import type { FormatKind } from '../../../api';
import { bbl, tileById, useBrew } from '../context';
import {
  Check,
  Chip,
  Field,
  QtyInput,
  SaveBar,
  Sheet,
  Stepper,
  WhenChips,
  qtyLine,
  qtyUnits,
  useSave,
  zeroQty,
  type Qty,
} from '../controls';

// Packaged it (Kegged it, on a serving tank). Every count starts at 0: the
// units are the ground truth cost and losses rest on, so nothing is
// pre-filled to be rubber-stamped. "Is now empty" ticks itself once the
// units account for most of the tank.

const EMPTY_SHARE = 0.85;

const KIND_LABEL: Record<FormatKind, string> = { keg: 'Kegs', can: 'Cans', bottle: 'Bottles' };

export default function PackageForm({ vesselId }: { vesselId: string }) {
  const { state, close } = useBrew();
  const tile = tileById(state, vesselId);
  const kegsOnly = tile?.kind === 'serving';
  const kinds = (['keg', 'can', 'bottle'] as FormatKind[]).filter(
    (k) => state.formats.some((f) => f.kind === k) && (!kegsOnly || k === 'keg'),
  );
  const [picked, setPicked] = useState<Set<FormatKind>>(new Set(kinds.slice(0, 1)));
  const [qty, setQty] = useState<Record<string, Qty>>({});
  const [lineLoss, setLineLoss] = useState(0);
  const [emptyTouched, setEmptyTouched] = useState(false);
  const [emptyBox, setEmptyBox] = useState(false);
  const [date, setDate] = useState('');
  const { saving, error, save } = useSave();
  if (!tile) return null;

  const formats = state.formats.filter((f) => picked.has(f.kind));
  const pf = state.settings.partial_fullness;
  const packaged = formats.reduce((sum, f) => sum + qtyUnits(qty[f.id] ?? zeroQty, f, pf) * f.bbl_per_unit, 0);
  const left = tile.volume_bbl - packaged;
  const autoEmpty = tile.volume_bbl > 0 && packaged >= tile.volume_bbl * EMPTY_SHARE;
  const empty = emptyTouched ? emptyBox : autoEmpty;
  const smallFormats = formats.some((f) => f.kind !== 'keg');

  const toggle = (k: FormatKind) => {
    const next = new Set(picked);
    if (next.has(k)) next.delete(k);
    else next.add(k);
    setPicked(next);
  };

  const submit = () =>
    save({
      kind: 'package',
      date,
      vessel_id: vesselId,
      lines: formats.map((f) => qtyLine(qty[f.id] ?? zeroQty, f)),
      line_loss_units: smallFormats ? lineLoss : 0,
      empty,
    });

  return (
    <Sheet
      title={kegsOnly ? `Kegged ${tile.name}` : `Packaged ${tile.name}`}
      sub={`${tile.beer} ${tile.batch_code} · ${bbl(tile.volume_bbl)} bbl in the tank`}
      onClose={close}
    >
      {kinds.length > 1 && (
        <Field label="What was packaged">
          <div className="brew-chips">
            {kinds.map((k) => (
              <Chip key={k} on={picked.has(k)} onClick={() => toggle(k)}>
                {KIND_LABEL[k]}
              </Chip>
            ))}
          </div>
        </Field>
      )}
      <div className="brew-section brew-rows">
        {formats.map((f) => (
          <div key={f.id} className="brew-row">
            <span className="brew-row-name">{f.name}</span>
            <QtyInput format={f} value={qty[f.id] ?? zeroQty} onChange={(q) => setQty({ ...qty, [f.id]: q })} />
          </div>
        ))}
      </div>
      {smallFormats && (
        <Field label="Lost on the line" hint="Optional. It shows up anyway when the tank is marked empty.">
          <Stepper label="Units lost" value={lineLoss} big={5} unit="units" onChange={setLineLoss} />
        </Field>
      )}
      <p className="brew-summary">
        {bbl(packaged)} bbl packaged, {bbl(Math.max(0, left))} bbl left
      </p>
      {left < -0.05 && (
        <p className="brew-warn">
          That's more than {tile.name} is recorded as holding. Tick “is now empty” and the extra is noted as a gain.
        </p>
      )}
      <div className="brew-section">
        <Check
          checked={empty}
          onChange={(v) => {
            setEmptyTouched(true);
            setEmptyBox(v);
          }}
        >
          {tile.name} is now empty
        </Check>
        {empty && <p className="brew-hint">What's left is written off and {tile.name} is marked dirty.</p>}
      </div>
      <WhenChips today={state.today} value={date} onChange={setDate} />
      <SaveBar onCancel={close} onSave={() => void submit()} saving={saving} disabled={packaged <= 0} error={error} />
    </Sheet>
  );
}
