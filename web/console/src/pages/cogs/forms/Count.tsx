import { useEffect, useState } from 'react';
import { useBrew } from '../context';
import { Chip, Field, QtyInput, SaveBar, Sheet, qtyLine, useSave, zeroQty, type Qty } from '../controls';

// The weekly count, blind. Every row starts as "–" and the expected number
// is never shown: a pre-filled count turns into a rubber stamp, and this
// count is the ground truth losses and cost rest on. Save stays locked until
// every row has been touched; the variance goes to the console.

interface Row {
  key: string;
  beerId: string;
  beer: string;
  formatId: string;
  qty?: Qty;
  none?: boolean;
}

export default function Count() {
  const { state, close } = useBrew();
  const [rows, setRows] = useState<Row[]>(() =>
    state.packaged.map((r) => ({
      key: `${r.beer_id}:${r.format_id}`,
      beerId: r.beer_id,
      beer: r.beer,
      formatId: r.format_id,
    })),
  );
  // The screen polls while the brewer counts. Stock packaged meanwhile
  // shows up as a new untouched row rather than as a refused save.
  useEffect(() => {
    setRows((cur) => {
      const have = new Set(cur.map((r) => r.key));
      const added = state.packaged
        .filter((r) => !have.has(`${r.beer_id}:${r.format_id}`))
        .map((r) => ({ key: `${r.beer_id}:${r.format_id}`, beerId: r.beer_id, beer: r.beer, formatId: r.format_id }));
      return added.length ? [...cur, ...added] : cur;
    });
  }, [state.packaged]);
  const [adding, setAdding] = useState(false);
  const [addBeer, setAddBeer] = useState<string | undefined>();
  const [addFormat, setAddFormat] = useState<string | undefined>();
  const { saving, error, save } = useSave();

  const toGo = rows.filter((r) => !r.qty && !r.none).length;
  const update = (key: string, patch: Partial<Row>) =>
    setRows(rows.map((r) => (r.key === key ? { ...r, ...patch } : r)));

  const addRow = () => {
    const b = state.beers.find((x) => x.id === addBeer);
    if (!b || !addFormat) return;
    const key = `${b.id}:${addFormat}`;
    if (!rows.some((r) => r.key === key)) {
      setRows([...rows, { key, beerId: b.id, beer: b.name, formatId: addFormat, qty: { ...zeroQty } }]);
    }
    setAdding(false);
    setAddBeer(undefined);
  };

  const submit = () =>
    save({
      kind: 'count',
      lines: rows.flatMap((r) => {
        const f = state.formats.find((x) => x.id === r.formatId);
        if (!f) return [];
        if (r.none) return [{ beer_id: r.beerId, format_id: r.formatId, none: true }];
        return [qtyLine(r.qty ?? zeroQty, f, { beer_id: r.beerId })];
      }),
    });

  return (
    <Sheet title="Count the packaged beer" sub="Count what's in the chiller. Tap None for anything that isn't there." onClose={close}>
      <div className="brew-section brew-rows">
        {rows.length === 0 && <p className="brew-empty">Nothing is expected in the chiller. Add a row for anything you find.</p>}
        {rows.map((r) => {
          const f = state.formats.find((x) => x.id === r.formatId);
          if (!f) return null;
          const touched = !!r.qty || !!r.none;
          return (
            <div key={r.key} className={`brew-row ${touched ? 'brew-row-touched' : ''}`}>
              <span className="brew-row-name">
                {r.beer}
                <span className="brew-row-sub">{f.name}{touched ? '' : ' · –'}</span>
              </span>
              <span className="brew-chips">
                {r.none ? (
                  <Chip on onClick={() => update(r.key, { none: false })}>
                    None
                  </Chip>
                ) : (
                  <>
                    <QtyInput format={f} partial value={r.qty ?? zeroQty} onChange={(q) => update(r.key, { qty: q, none: false })} />
                    <Chip on={false} onClick={() => update(r.key, { none: true, qty: undefined })}>
                      None
                    </Chip>
                  </>
                )}
              </span>
            </div>
          );
        })}
      </div>
      {adding ? (
        <Field label="Add a row">
          <div className="brew-chips">
            {state.beers
              .filter((b) => b.active)
              .map((b) => (
                <Chip key={b.id} on={addBeer === b.id} onClick={() => setAddBeer(b.id)}>
                  {b.name}
                </Chip>
              ))}
          </div>
          <div className="brew-chips" style={{ marginTop: '0.5rem' }}>
            {state.formats.map((f) => (
              <Chip key={f.id} on={addFormat === f.id} onClick={() => setAddFormat(f.id)}>
                {f.name}
              </Chip>
            ))}
            <button type="button" className="brew-btn brew-btn-primary" disabled={!addBeer || !addFormat} onClick={addRow}>
              Add
            </button>
          </div>
        </Field>
      ) : (
        <div className="brew-section">
          <button type="button" className="brew-btn" onClick={() => setAdding(true)}>
            Found something else
          </button>
        </div>
      )}
      <SaveBar
        onCancel={close}
        onSave={() => void submit()}
        saving={saving}
        error={error}
        disabled={toGo > 0 || rows.length === 0}
        label={toGo > 0 ? `Save count (${toGo} to go)` : 'Save count'}
      />
    </Sheet>
  );
}
