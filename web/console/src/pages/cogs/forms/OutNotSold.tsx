import { useState } from 'react';
import { useBrew } from '../context';
import { Chip, Field, QtyInput, SaveBar, Sheet, WhenChips, qtyLine, useSave, zeroQty, type Qty } from '../controls';

// Out, not sold: packaged beer that left without a sale (dumped, an event
// or a gift), or a keg that came back full.

const REASONS = [
  { key: 'dumped', label: 'Dumped' },
  { key: 'event', label: 'Event or gift' },
  { key: 'came_back', label: 'Came back full' },
  { key: 'other', label: 'Other' },
];

export default function OutNotSold() {
  const { state, close } = useBrew();
  const beers = state.beers.filter((b) => b.active || state.packaged.some((r) => r.beer_id === b.id));
  const [reason, setReason] = useState<string | undefined>();
  const [beer, setBeer] = useState<string | undefined>();
  const [format, setFormat] = useState<string | undefined>(state.formats[0]?.id);
  const [qty, setQty] = useState<Qty>(zeroQty);
  const [note, setNote] = useState('');
  const [date, setDate] = useState('');
  const { saving, error, save } = useSave();
  const f = state.formats.find((x) => x.id === format);

  return (
    <Sheet title="Out, not sold" onClose={close}>
      <Field label="Why">
        <div className="brew-chips">
          {REASONS.map((r) => (
            <Chip key={r.key} on={reason === r.key} onClick={() => setReason(r.key)}>
              {r.label}
            </Chip>
          ))}
        </div>
      </Field>
      <Field label="Beer">
        <div className="brew-chips">
          {beers.map((b) => (
            <Chip key={b.id} on={beer === b.id} onClick={() => setBeer(b.id)}>
              {b.name}
            </Chip>
          ))}
        </div>
      </Field>
      <Field label="Format">
        <div className="brew-chips">
          {state.formats.map((x) => (
            <Chip
              key={x.id}
              on={format === x.id}
              onClick={() => {
                setFormat(x.id);
                setQty(zeroQty);
              }}
            >
              {x.name}
            </Chip>
          ))}
        </div>
      </Field>
      {f && (
        <Field label="How many">
          <QtyInput format={f} value={qty} onChange={setQty} />
        </Field>
      )}
      {reason === 'other' && (
        <Field label="What happened">
          <input className="brew-input" value={note} onChange={(e) => setNote(e.target.value)} />
        </Field>
      )}
      <WhenChips today={state.today} value={date} onChange={setDate} />
      <SaveBar
        onCancel={close}
        saving={saving}
        error={error}
        disabled={!reason || !beer || !f || (qty.full + qty.cases + qty.loose <= 0)}
        onSave={() =>
          f &&
          void save({ kind: 'out_not_sold', date, reason, note, beer_id: beer, lines: [qtyLine(qty, f, { beer_id: beer })] })
        }
      />
    </Sheet>
  );
}
